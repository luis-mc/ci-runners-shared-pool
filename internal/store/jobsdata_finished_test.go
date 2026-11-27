package store

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsdata"
)

// R5.12: a finished row never changes after its job completes.
func TestIntegrationFinishedRowNeverChanges(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	done := mkJob(1, "build", jobsdata.Done, jobsdata.Success, at(0), 5, 60)
	must(t, s.UpsertJob(ctx, done))
	// The same job redelivered with another result and other times.
	again := mkJob(1, "build", jobsdata.Done, jobsdata.Failure, at(30), 9, 99)
	must(t, s.UpsertJob(ctx, again))
	d, ok, err := s.GetJob(ctx, 1)
	must(t, err)
	if !ok || d.Result != jobsdata.Success || !d.StartedAt.Equal(done.StartedAt) || !d.CompletedAt.Equal(done.CompletedAt) {
		t.Fatalf("finished row changed: %+v", d.Job)
	}
}

// R5.12: GitHub's own end of a job crsp marked lost replaces the guess.
func TestIntegrationLostRowTakesGitHubsEnd(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	run := mkJob(1, "build", jobsdata.Running, "", at(0), 5, 0)
	must(t, s.UpsertJob(ctx, run))
	must(t, s.SetFacts(ctx, 1, jobsdata.Facts{Lost: true}))
	must(t, s.UpsertJob(ctx, mkJob(1, "build", jobsdata.Done, jobsdata.Success, at(0), 5, 60)))
	d, _, err := s.GetJob(ctx, 1)
	must(t, err)
	if d.Result != jobsdata.Success {
		t.Fatalf("result %q, want success", d.Result)
	}
}

// R5.12: the stale pass lists open rows only, so a finished row is never read from GitHub again, however old.
func TestIntegrationFinishedRowNeverListedForRefetch(t *testing.T) {
	e := newIJ(t)
	ctx := context.Background()
	must(t, e.js.UpsertRun(ctx, jobsdata.Run{RepositoryID: 1, Repository: "o/r", RunID: 100, Attempt: 1}))
	must(t, e.js.UpsertJob(ctx, mkJob(1, "build", jobsdata.Done, jobsdata.Success, at(0), 5, 60)))
	must(t, e.js.UpsertJob(ctx, mkJob(2, "lint", jobsdata.Done, jobsdata.Failure, at(0), 5, 60)))
	runs, err := e.ij.StaleRuns(ctx, time.Now().Add(24*time.Hour), time.Now().Add(24*time.Hour), 100)
	must(t, err)
	if len(runs) != 0 {
		t.Fatalf("a run with only finished jobs is listed: %+v", runs)
	}
}
