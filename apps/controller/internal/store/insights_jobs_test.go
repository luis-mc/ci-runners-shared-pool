package store

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata/reconcile"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

type ijEnv struct {
	s  *Store
	ij *InsightsJobsStore
	js *JobsStore
}

func newIJ(t *testing.T) ijEnv {
	t.Helper()
	s := openTemp(t)
	return ijEnv{s: s, ij: s.InsightsJobs(), js: s.Jobs()}
}

func (e ijEnv) row(t *testing.T, id int64) sqlcgen.Job {
	t.Helper()
	r, err := sqlcgen.New(e.s.db).GetJobsRow(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (e ijEnv) seed(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	must(t, e.js.UpsertRun(ctx, jobsdata.Run{RepositoryID: 1, Repository: "o/r", RunID: 100, Attempt: 1}))
	must(t, e.js.UpsertJob(ctx, mkJob(1, "test", jobsdata.Queued, "", at(0), 0, 0)))
	must(t, e.js.UpsertJob(ctx, mkJob(2, "lint", jobsdata.Running, "", at(0), 5, 0)))
}

func TestIntegrationStaleRunsListed(t *testing.T) { // R5.41
	e := newIJ(t)
	e.seed(t)
	ctx := context.Background()
	runs, err := e.ij.StaleRuns(ctx, time.Now().Add(-time.Hour), time.Now().Add(-5*time.Minute), 100)
	must(t, err)
	if len(runs) != 0 {
		t.Fatalf("fresh rows are stale: %+v", runs)
	}
	runs, err = e.ij.StaleRuns(ctx, time.Now().Add(time.Minute), time.Now().Add(-5*time.Minute), 100)
	must(t, err)
	if len(runs) != 1 || runs[0].RunID != 100 || runs[0].Repository != "o/r" || runs[0].RunAttempt != 1 {
		t.Fatalf("stale runs %+v", runs)
	}
	open, err := e.ij.OpenJobs(ctx, 1, 100, 1)
	must(t, err)
	if len(open) != 2 {
		t.Fatalf("open jobs %v", open)
	}
}

func TestIntegrationCompletedRunListedWhenFresh(t *testing.T) { // R5.41: a run GitHub completed
	e := newIJ(t)
	e.seed(t)
	ctx := context.Background()
	must(t, e.js.UpsertRun(ctx, jobsdata.Run{RepositoryID: 1, Repository: "o/r", RunID: 100, Attempt: 1,
		CompletedAt: at(5), Conclusion: jobsdata.Success}))
	runs, err := e.ij.StaleRuns(ctx, time.Now().Add(-time.Hour), time.Now().Add(-5*time.Minute), 100)
	must(t, err)
	if len(runs) != 1 || runs[0].RunID != 100 {
		t.Fatalf("completed run with open jobs not listed: %+v", runs)
	}
}

func TestIntegrationApplyDoneAndOrigin(t *testing.T) { // R5.39, R5.41
	e := newIJ(t)
	e.seed(t)
	ctx := context.Background()
	checked := time.Now().UTC()
	must(t, e.ij.Apply(ctx, 1, reconcile.RemoteJob{ID: 1, Status: "completed", Conclusion: "success",
		RunnerName: "old-pool-3", RunnerGroupName: "Default", Labels: []string{"self-hosted"},
		StartedAt: at(1), CompletedAt: at(3),
		Steps: []reconcile.RemoteStep{{Number: 1, Name: "build", Status: "completed", Conclusion: "success"}}},
		reconcile.Other, checked))
	r := e.row(t, 1)
	if r.State != "done" || r.Result != "success" || r.RunnerOrigin != "other" || r.RunnerGroup != "Default" ||
		r.RunnerLabels != `["self-hosted"]` || r.StepsSource != "rest" || r.CheckedAt == "" || r.RunnerName != "old-pool-3" {
		t.Fatalf("row %+v", r)
	}
	if r.CompletedAt == "" || r.StartedAt == "" {
		t.Fatalf("times not set: %+v", r)
	}
	steps, err := sqlcgen.New(e.s.db).ListJobSteps(ctx, 1)
	must(t, err)
	if len(steps) != 1 || steps[0].State != "done" || steps[0].Result != "success" {
		t.Fatalf("steps %+v", steps)
	}
	// Done jobs leave the stale list, and state never goes backward.
	open, _ := e.ij.OpenJobs(ctx, 1, 100, 1)
	if len(open) != 1 || open[0] != 2 {
		t.Fatalf("open after apply %v", open)
	}
	must(t, e.ij.Apply(ctx, 1, reconcile.RemoteJob{ID: 1, Status: "queued"}, reconcile.Unknown, checked))
	if r := e.row(t, 1); r.State != "done" || r.Result != "success" || r.RunnerOrigin != "other" {
		t.Fatalf("a stale read moved the job back: %+v", r)
	}
}

func TestIntegrationApplyKeepsEventSteps(t *testing.T) { // R5.40
	e := newIJ(t)
	e.seed(t)
	ctx := context.Background()
	must(t, e.js.ReplaceSteps(ctx, 2, []jobsdata.Step{{JobID: 2, Number: 1, Name: "from events", State: "done"}}))
	must(t, e.ij.SetStepsSource(ctx, 2, "events"))
	must(t, e.ij.Apply(ctx, 1, reconcile.RemoteJob{ID: 2, Status: "in_progress",
		Steps: []reconcile.RemoteStep{{Number: 1, Name: "from rest", Status: "in_progress"}}}, reconcile.Unknown, time.Now()))
	steps, err := sqlcgen.New(e.s.db).ListJobSteps(ctx, 2)
	must(t, err)
	if len(steps) != 1 || steps[0].Name != "from events" || e.row(t, 2).StepsSource != "events" {
		t.Fatalf("event steps replaced: %+v", steps)
	}
	ref, ok, err := e.ij.JobRef(ctx, 2)
	must(t, err)
	if !ok || ref.StepsSource != "events" || !ref.HasSteps || ref.Repository != "o/r" {
		t.Fatalf("ref %+v", ref)
	}
}

func TestIntegrationMarkLost(t *testing.T) { // R5.41
	e := newIJ(t)
	e.seed(t)
	ctx := context.Background()
	at1 := time.Now().UTC()
	must(t, e.ij.MarkLost(ctx, 1, reconcile.GoneReason, at1))
	r := e.row(t, 1)
	if r.State != "done" || r.Result != "lost" || r.WaitText != reconcile.GoneReason || r.CheckedAt == "" {
		t.Fatalf("row %+v", r)
	}
	// A finished job keeps its result.
	must(t, e.js.UpsertJob(ctx, mkJob(3, "build", jobsdata.Done, jobsdata.Success, at(0), 1, 10)))
	must(t, e.ij.MarkLost(ctx, 3, "x", at1))
	if r := e.row(t, 3); r.Result != "success" {
		t.Fatalf("done job overwritten: %+v", r)
	}
}

func TestIntegrationSetRunnerOriginKeepsStored(t *testing.T) { // R5.39
	e := newIJ(t)
	e.seed(t)
	ctx := context.Background()
	must(t, e.ij.SetRunnerOrigin(ctx, 1, reconcile.Crsp, "Default", []string{"crsp"}))
	must(t, e.ij.SetRunnerOrigin(ctx, 1, reconcile.Unknown, "", nil))
	must(t, e.ij.SetRunnerOrigin(ctx, 404, reconcile.Crsp, "", nil)) // not stored: skipped
	if r := e.row(t, 1); r.RunnerOrigin != "crsp" || r.RunnerGroup != "Default" || r.RunnerLabels != `["crsp"]` {
		t.Fatalf("row %+v", r)
	}
	// The next webhook upsert of the job does not wipe it.
	must(t, e.js.UpsertJob(ctx, mkJob(1, "test", jobsdata.Running, "", at(0), 2, 0)))
	if r := e.row(t, 1); r.RunnerOrigin != "crsp" {
		t.Fatalf("upsert wiped the origin: %+v", r)
	}
}
