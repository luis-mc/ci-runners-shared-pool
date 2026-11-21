package store

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsdata"
)

// followJob is a finished job of a run, for the follow-up link tests.
func followJob(id, run int64, name string, state jobsdata.State, created, started, done time.Time) jobsdata.Job {
	j := jobsdata.Job{ID: id, RepositoryID: 1, Repository: "o/r", RunID: run, Attempt: 1, Workflow: "w.yml", Name: name, JobType: name,
		JobKey: "o/r/w.yml/" + name, HeadSHA: "abc", State: state, CreatedAt: created, StartedAt: started, CompletedAt: done}
	if state == jobsdata.Done {
		j.Result = jobsdata.Success
	}
	return j
}

// The Installers run (event workflow_run) is created before crsp has stored the last job of the CI run that
// started it: the link appears when that job finishes, and the CI run stays follow_up_running until Installers ends.
func TestIntegrationFollowUpLinkedWhenTheChildArrivesFirst(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	ci := jobsdata.Run{RepositoryID: 1, Repository: "o/r", RunID: 100, Attempt: 1, Workflow: "ci.yml", WorkflowName: "CI", Event: "push", HeadSHA: "abc", CreatedAt: at(0)}
	inst := jobsdata.Run{RepositoryID: 1, Repository: "o/r", RunID: 200, Attempt: 1, Workflow: "installers.yml", WorkflowName: "Installers",
		Event: jobsdata.EventWorkflowRun, HeadSHA: "abc", CreatedAt: at(10), HTMLURL: "https://github.com/o/r/actions/runs/200"}
	must(t, s.UpsertRun(ctx, ci))
	must(t, s.UpsertJob(ctx, followJob(1, 100, "build", jobsdata.Done, at(0), at(1), at(5))))
	must(t, s.UpsertJob(ctx, followJob(2, 100, "test", jobsdata.Running, at(0), at(1), time.Time{})))
	must(t, s.UpsertRun(ctx, inst))
	must(t, s.UpsertJob(ctx, followJob(3, 200, "msi", jobsdata.Queued, at(10), time.Time{}, time.Time{})))
	// CI has one open run, so the single candidate is taken at once.
	d, ok, err := s.GetRun(ctx, 100, 0)
	must(t, err)
	if !ok || len(d.StartedNext) != 1 || d.StartedNext[0].RunID != 200 || d.StartedNext[0].WorkflowName != "Installers" || d.StartedNext[0].State != jobsdata.Queued {
		t.Fatalf("started next before CI finished: %+v", d.StartedNext)
	}
	// the last CI job finishes: the link stays, CI is done on its own, Installers still open
	must(t, s.UpsertJob(ctx, followJob(2, 100, "test", jobsdata.Done, at(0), at(1), at(9))))
	rows, _, err := s.ListRuns(ctx, jobsdata.RunFilter{})
	must(t, err)
	var gotCI, gotInst jobsdata.RunRow
	for _, r := range rows {
		switch r.RunID {
		case 100:
			gotCI = r
		case 200:
			gotInst = r
		}
	}
	if gotCI.FollowUps != 1 || gotCI.FollowUpsOpen != 1 || gotInst.ParentRunID != 100 {
		t.Fatalf("counts: ci %+v installers %+v", gotCI, gotInst)
	}
	// Installers finishes
	must(t, s.UpsertJob(ctx, followJob(3, 200, "msi", jobsdata.Done, at(10), at(16), at(24))))
	d, _, err = s.GetRun(ctx, 100, 0)
	must(t, err)
	fu := d.StartedNext[0]
	if fu.State != jobsdata.Done || fu.Result != jobsdata.Success || fu.CompletedAt.IsZero() || d.FollowUpsOpen != 0 {
		t.Fatalf("after Installers: %+v open %d", fu, d.FollowUpsOpen)
	}
}

func TestIntegrationFollowUpPayloadParentWinsOverInference(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	for _, id := range []int64{100, 150} {
		must(t, s.UpsertRun(ctx, jobsdata.Run{RepositoryID: 1, Repository: "o/r", RunID: id, Attempt: 1, Event: "push", HeadSHA: "abc",
			CreatedAt: at(0), Conclusion: jobsdata.Success, CompletedAt: at(int(id) / 10)}))
	}
	// inference alone would pick 150 (finished last); the payload names 100
	must(t, s.UpsertRun(ctx, jobsdata.Run{RepositoryID: 1, Repository: "o/r", RunID: 200, Attempt: 1, Event: jobsdata.EventWorkflowRun,
		HeadSHA: "abc", CreatedAt: at(20), TriggeredBy: 100}))
	must(t, s.UpsertRun(ctx, jobsdata.Run{RepositoryID: 1, Repository: "o/r", RunID: 150, Attempt: 1, Event: "push", HeadSHA: "abc", CreatedAt: at(0)}))
	d, _, err := s.GetRun(ctx, 100, 0)
	must(t, err)
	e, _, err := s.GetRun(ctx, 150, 0)
	must(t, err)
	if len(d.StartedNext) != 1 || len(e.StartedNext) != 0 {
		t.Fatalf("payload link lost: 100 has %d, 150 has %d", len(d.StartedNext), len(e.StartedNext))
	}
}
