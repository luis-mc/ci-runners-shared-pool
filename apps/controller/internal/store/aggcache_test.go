package store

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

// R3-2: the grouped table and the stats are read once for every caller until a job or run is written.
func TestGroupsAndStatsAreCachedUntilAWrite(t *testing.T) {
	ctx := context.Background()
	s, n, err := OpenCounted(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	js := s.Jobs()

	first, err := js.Groups(ctx, jobsdata.ByRepository)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 0 {
		t.Fatalf("empty store has groups: %v", first)
	}
	n.Reset()
	for range 3 {
		if _, err := js.Groups(ctx, jobsdata.ByRepository); err != nil {
			t.Fatal(err)
		}
	}
	if n.Load() != 0 {
		t.Fatalf("cached groups ran %d queries", n.Load())
	}
	if _, err := js.Groups(ctx, jobsdata.ByMachine); err != nil || n.Load() == 0 {
		t.Fatalf("another grouping must read: %v, %d queries", err, n.Load())
	}

	now := fmtTime(time.Now())
	if _, err := s.db.ExecContext(ctx, `INSERT INTO jobs (repository_id, repository, run_id, run_attempt, workflow, name, job_ident, branch, state, result, machine_id, started_at, created_at, completed_at)
		VALUES (1, 'o/r', 1, 1, 'ci.yml', 'j', 'k', 'main', 'done', 'success', 'm1', ?, ?, ?)`, now, now, now); err != nil {
		t.Fatal(err)
	}
	if got, _ := js.Groups(ctx, jobsdata.ByRepository); len(got) != 0 {
		t.Fatalf("a raw write is not a store write; the cache keeps its answer: %v", got)
	}
	s.touchJobs()
	got, err := js.Groups(ctx, jobsdata.ByRepository)
	if err != nil || len(got) == 0 {
		t.Fatalf("after a write the groups are read again: %v %v", got, err)
	}
	// The caller may change what it got without changing the cache.
	got[0].Name = "changed"
	if again, _ := js.Groups(ctx, jobsdata.ByRepository); again[0].Name == "changed" {
		t.Fatal("the cache hands out its own slice")
	}

	n.Reset()
	st, err := js.Stats(ctx, 30, jobsdata.JobFilter{})
	if err != nil || len(st.Days) != 30 {
		t.Fatalf("stats: %v %d days", err, len(st.Days))
	}
	read := n.Load()
	if _, err := js.Stats(ctx, 30, jobsdata.JobFilter{}); err != nil || n.Load() != read {
		t.Fatalf("cached stats ran %d more queries", n.Load()-read)
	}
}
