package store

import (
	"context"
	"fmt"
	"slices"
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

// R3-5: the samples of a question are read once until a job is written.
func TestSamplesManyKeepsAnswersUntilAWrite(t *testing.T) {
	ctx := context.Background()
	s, n, err := OpenCounted(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	js := s.Jobs()
	qs := []jobsdata.SampleQuery{{RepositoryID: 1, JobKey: "k", Limit: 30}, {RepositoryID: 2, JobKey: "k", Limit: 30}, {RepositoryID: 1, Limit: 30}}
	if _, err := js.SamplesMany(ctx, qs); err != nil {
		t.Fatal(err)
	}
	n.Reset()
	if _, err := js.SamplesMany(ctx, qs); err != nil || n.Load() != 0 {
		t.Fatalf("the second read ran %d queries (%v)", n.Load(), err)
	}
	more := append(slices.Clone(qs), jobsdata.SampleQuery{RepositoryID: 3, JobKey: "k", Limit: 30})
	if _, err := js.SamplesMany(ctx, more); err != nil || n.Load() == 0 {
		t.Fatalf("a new question must be read (%v, %d)", err, n.Load())
	}
	s.touchJobs()
	n.Reset()
	if _, err := js.SamplesMany(ctx, qs); err != nil || n.Load() == 0 {
		t.Fatalf("after a write the samples are read again (%v, %d)", err, n.Load())
	}
}

func TestSampleCacheIsBounded(t *testing.T) {
	var c sampleCache
	for i := range 3 * sampleCacheMax {
		c.put(fmt.Sprint(i), 1, nil)
	}
	if len(c.m) > sampleCacheMax {
		t.Fatalf("the cache holds %d entries", len(c.m))
	}
	c.put("new", 2, nil)
	if _, ok := c.get("new", 1); ok {
		t.Fatal("an entry of another version was served")
	}
}
