package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

// CRSP290: the batch reads answer exactly what the one-at-a-time reads answer.

func TestIntegrationSamplesManyMatchesSamples(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	for i := 1; i <= 12; i++ {
		name := "test"
		if i%3 == 0 {
			name = "build (linux)"
		}
		must(t, s.UpsertJob(ctx, mkJob(int64(i), name, jobsdata.Done, jobsdata.Success, now.Add(-time.Duration(13-i)*time.Hour), 5, 10*i)))
	}
	must(t, s.UpsertJob(ctx, mkJob(40, "test", jobsdata.Done, jobsdata.Failure, now.Add(-time.Hour), 5, 10)))
	key := jobsdata.MakeKey("o/r", ".github/workflows/ci.yml", "test")
	since := now.Add(-30 * 24 * time.Hour)
	qs := []jobsdata.SampleQuery{
		{RepositoryID: 1, JobKey: key, Since: since, Limit: 3},
		{RepositoryID: 1, Workflow: ".github/workflows/ci.yml", JobType: "build", Since: since, Limit: 30},
		{RepositoryID: 1, Since: since, Limit: 30},
		{RepositoryID: 9, JobKey: key, Since: since, Limit: 30},
		{RepositoryID: 1, JobKey: key, StepName: "nope", Since: since, Limit: 30},
	}
	got, err := s.SamplesMany(ctx, qs)
	must(t, err)
	for i, q := range qs {
		want, err := s.Samples(ctx, q)
		must(t, err)
		if len(want) == 0 && len(got[i]) == 0 {
			continue
		}
		if !reflect.DeepEqual(got[i], want) {
			t.Errorf("query %d: batch %+v, single %+v", i, got[i], want)
		}
	}
	if len(got[0]) != 3 || len(got[2]) != 12 {
		t.Errorf("sizes %d %d", len(got[0]), len(got[2]))
	}
}

func TestIntegrationRunsWithJobsMatchesGetRun(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	for run := int64(100); run < 103; run++ {
		must(t, s.UpsertRun(ctx, jobsdata.Run{RepositoryID: 1, Repository: "o/r", RunID: run, Attempt: 1, Workflow: "w.yml", HeadSHA: "abc", Branch: "b", CreatedAt: at(int(run))}))
		a := mkJob(run*10+1, "build", jobsdata.Done, jobsdata.Success, at(int(run)), 1, 2)
		b := mkJob(run*10+2, "test", jobsdata.Running, "", at(int(run)), 1, 0)
		a.RunID, b.RunID = run, run
		must(t, s.UpsertJob(ctx, a))
		must(t, s.UpsertJob(ctx, b))
	}
	must(t, s.PutGraph(ctx, jobsdata.Graph{RepositoryID: 1, Workflow: "w.yml", HeadSHA: "abc", OK: true, Needs: map[string][]string{"test": {"build"}}}))
	rows, _, err := s.ListRuns(ctx, jobsdata.RunFilter{Limit: 10})
	must(t, err)
	got, err := s.RunsWithJobs(ctx, rows)
	must(t, err)
	if len(got) != 3 {
		t.Fatalf("%d details", len(got))
	}
	for _, d := range got {
		want, ok, err := s.GetRun(ctx, d.RunID, d.Attempt)
		must(t, err)
		if !ok || !reflect.DeepEqual(d.Jobs, want.Jobs) || !reflect.DeepEqual(d.Graph, want.Graph) || d.State != want.State {
			t.Errorf("run %d: batch %+v / %+v, single %+v / %+v", d.RunID, d.Jobs, d.Graph, want.Jobs, want.Graph)
		}
	}
}

func TestIntegrationFinishedBetweenReadsOnlyTheWindow(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	for i := 1; i <= 6; i++ { // job i completed i hours ago
		j := mkJob(int64(i), "test", jobsdata.Done, jobsdata.Success, now.Add(-time.Duration(i)*time.Hour-time.Minute), 1, 20)
		j.MachineID = "m1"
		if i == 5 {
			j.MachineID = "m2"
		}
		must(t, s.UpsertJob(ctx, j))
	}
	must(t, s.UpsertJob(ctx, mkJob(9, "test", jobsdata.Running, "", now.Add(-time.Hour), 1, 0)))
	got, err := s.FinishedBetween(ctx, now.Add(-4*time.Hour), now, 10)
	must(t, err)
	if len(got) != 3 || got[0].ID != 1 || got[2].ID != 3 {
		t.Errorf("window: %+v", got)
	}
	if got, _ = s.FinishedBetween(ctx, now.Add(-6*time.Hour), now, 2); len(got) != 2 {
		t.Errorf("limit: %d", len(got))
	}
	any, err := s.AnyFinishedBetween(ctx, now.Add(-24*time.Hour), now.Add(-4*time.Hour), "")
	must(t, err)
	onM2, _ := s.AnyFinishedBetween(ctx, now.Add(-24*time.Hour), now.Add(-4*time.Hour), "m2")
	m2Later, _ := s.AnyFinishedBetween(ctx, now.Add(-24*time.Hour), now.Add(-6*time.Hour+30*time.Minute), "m2")
	noneOnM3, _ := s.AnyFinishedBetween(ctx, now.Add(-24*time.Hour), now.Add(-4*time.Hour), "m3")
	empty, _ := s.AnyFinishedBetween(ctx, now.Add(-24*time.Hour), now.Add(-7*time.Hour), "")
	if !any || !onM2 || m2Later || noneOnM3 || empty {
		t.Errorf("any %v onM2 %v m2Later %v noneOnM3 %v empty %v", any, onM2, m2Later, noneOnM3, empty)
	}
}
