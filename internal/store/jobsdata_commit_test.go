package store

import (
	"context"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsdata"
)

var _ jobsdata.CommitReader = (*JobsStore)(nil)

// R5.13: every run of one head commit comes back with its jobs, the latest attempt of each, oldest first, and
// no run of another commit.
func TestIntegrationRunsOfCommit(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	const sha = "abc1234def5678abc1234def5678abc1234def56"
	run := func(id int64, attempt int, head string, minute int) jobsdata.Run {
		return jobsdata.Run{RepositoryID: 1, Repository: "o/r", RunID: id, Attempt: attempt, Workflow: "w.yml", Branch: "b", HeadSHA: head, CreatedAt: at(minute)}
	}
	must(t, s.UpsertRun(ctx, run(200, 1, sha, 5)))
	must(t, s.UpsertRun(ctx, run(100, 1, sha, 1)))
	must(t, s.UpsertRun(ctx, run(100, 2, sha, 2))) // a re-run: only attempt 2 counts
	must(t, s.UpsertRun(ctx, run(300, 1, "ffffffffffffffffffffffffffffffffffffffff", 3)))
	j := mkJob(1, "build", jobsdata.Done, jobsdata.Success, at(1), 1, 30)
	j.RunID, j.Attempt, j.HeadSHA = 100, 2, sha
	must(t, s.UpsertJob(ctx, j))

	got, err := s.RunsOfCommit(ctx, 0, sha)
	must(t, err)
	if len(got) != 2 || got[0].RunID != 100 || got[0].Attempt != 2 || got[1].RunID != 200 {
		t.Fatalf("runs %+v", got)
	}
	if len(got[0].Jobs) != 1 || got[0].Jobs[0].Name != "build" {
		t.Errorf("jobs of run 100: %+v", got[0].Jobs)
	}
	if short, _ := s.RunsOfCommit(ctx, 1, sha[:7]); len(short) != 2 {
		t.Errorf("a prefix finds %d runs, want 2", len(short))
	}
	if other, _ := s.RunsOfCommit(ctx, 2, sha); len(other) != 0 {
		t.Errorf("another repository found %d runs", len(other))
	}
	if none, err := s.RunsOfCommit(ctx, 0, "0000000"); err != nil || len(none) != 0 {
		t.Errorf("unknown commit: %v %v", none, err)
	}
}
