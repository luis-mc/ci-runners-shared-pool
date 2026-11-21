package store

import (
	"context"
	"io/fs"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/luis-mc/ci-runners-shared-pool/internal/insights/capacity"
)

func peakBatch(job int64, cpu, mem int64, final bool, mod func(*capacity.Batch)) capacity.Batch {
	base := time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC).Add(time.Duration(job) * time.Hour)
	var smp []capacity.ResourceSample
	for i := 0; i < 5; i++ {
		smp = append(smp, capacity.ResourceSample{At: base.Add(time.Duration(i) * time.Second), CPUMillis: cpu, MemoryBytes: mem, Known: true})
	}
	b := capacity.Batch{JobID: job, MachineID: "m1", RunnerID: "r1", Repository: "o/r", Job: "build", RunID: job, Source: "cgroup", Samples: smp, Final: final}
	if mod != nil {
		mod(&b)
	}
	return b
}

// OPS66 R5.60, R5.63: the final batch writes the run's peaks keyed by repository, workflow, job key and
// runner; the summary reads the newest good runs of the key and says measured from 5.
func TestPeaksStoreRunAndKeySummary(t *testing.T) {
	ctx := context.Background()
	s, err := OpenForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r := s.Resources()
	// The jobs table knows job 1: workflow and job key come from it, not from the batch.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO jobs (id, repository_id, repository, run_id, run_attempt, workflow, name, job_ident, runner_name, machine_id)
		VALUES (1, 5, 'o/r', 1, 1, 'ci.yml', 'build', 'ci.yml/build', 'runner-a', 'm9')`); err != nil {
		t.Fatal(err)
	}
	if err := r.AddBatch(ctx, peakBatch(1, 1000, 100, false, nil)); err != nil {
		t.Fatal(err)
	}
	if _, _, found, err := r.Peaks(ctx, 1); err != nil || found {
		t.Fatalf("a running job has no peaks yet: %v %v", found, err)
	}
	if err := r.AddBatch(ctx, peakBatch(1, 1000, 100, true, func(b *capacity.Batch) { b.MemoryPeakBytes = 900 })); err != nil {
		t.Fatal(err)
	}
	run, key, found, err := r.Peaks(ctx, 1)
	if err != nil || !found {
		t.Fatalf("%v %v", found, err)
	}
	if run.Repository != "o/r" || run.Workflow != "ci.yml" || run.JobKey != "ci.yml/build" || run.Runner != "runner-a" || run.MachineID != "m9" ||
		run.PeakCPUMillis != 1000 || run.PeakMemoryBytes != 900 || run.Samples != 5 || run.DurationMS != 4000 || !run.Good {
		t.Fatalf("%+v", run)
	}
	if key.Measured || key.Runs != 1 {
		t.Fatalf("one run is not measured: %+v", key)
	}
	// Jobs 2 to 5 are not in the jobs table: the key falls back to the batch (workflow empty), so they form
	// another key and job 1's summary does not move.
	for j := int64(2); j <= 5; j++ {
		if err := r.AddBatch(ctx, peakBatch(j, j*1000, j*100, true, nil)); err != nil {
			t.Fatal(err)
		}
	}
	if _, key, _, _ = r.Peaks(ctx, 1); key.Runs != 1 {
		t.Fatalf("keys mixed: %+v", key)
	}
	run, key, _, _ = r.Peaks(ctx, 5)
	if run.Workflow != "" || run.JobKey != "build" || run.Runner != "r1" || key.Runs != 4 || key.Measured {
		t.Fatalf("%+v %+v", run, key)
	}
	if err := r.AddBatch(ctx, peakBatch(6, 6000, 600, true, nil)); err != nil {
		t.Fatal(err)
	}
	// An OOM killed run and a cut short run are stored but not good: they do not count.
	if err := r.AddBatch(ctx, peakBatch(7, 99000, 9900, true, func(b *capacity.Batch) { b.OOMKilled = true })); err != nil {
		t.Fatal(err)
	}
	if err := r.AddBatch(ctx, peakBatch(8, 99000, 9900, true, func(b *capacity.Batch) { b.CutShort = true })); err != nil {
		t.Fatal(err)
	}
	run, key, _, _ = r.Peaks(ctx, 7)
	if !run.OOMKilled || run.Good || !key.Measured || key.Runs != 5 || key.CPUP90Millis != 6000 || key.MemoryP90Bytes != 600 {
		t.Fatalf("%+v %+v", run, key)
	}
	if run, _, _, _ = r.Peaks(ctx, 8); !run.CutShort || run.Good {
		t.Fatalf("%+v", run)
	}
	// A job the agent could not read keeps the reason and no peaks.
	if err := r.AddBatch(ctx, capacity.Batch{JobID: 9, RunnerID: "r1", Repository: "o/r", Job: "x", Final: true, UnknownReason: "no runner process"}); err != nil {
		t.Fatal(err)
	}
	if run, _, _, _ = r.Peaks(ctx, 9); run.Good || run.UnknownReason != "no runner process" || run.PeakMemoryBytes != 0 {
		t.Fatalf("%+v", run)
	}
}

// OPS66: migration 00034 makes job_run_peaks; its Down drops it; Up makes it again with its indexes.
func TestMigrationJobRunPeaks(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, s.db, sub)
	if err != nil {
		t.Fatal(err)
	}
	count := func(typ, name string) int {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = ? AND name = ?`, typ, name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count("table", "job_run_peaks") != 1 || count("index", "job_run_peaks_key") != 1 || count("index", "job_run_peaks_runner") != 1 {
		t.Fatal("job_run_peaks and its indexes exist after Up")
	}
	if _, err := p.DownTo(ctx, 33); err != nil {
		t.Fatal(err)
	}
	if count("table", "job_run_peaks") != 0 {
		t.Fatal("Down drops the table")
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if count("table", "job_run_peaks") != 1 {
		t.Fatal("Up makes it again")
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO job_run_peaks (job_id, repository, job_key, completed_at, good) VALUES (1, 'o/r', 'k', 'x', 2)`); err == nil {
		t.Fatal("good must be 0 or 1")
	}
}
