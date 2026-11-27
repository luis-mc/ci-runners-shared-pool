package store

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/capacity"
)

// R5.19, R5.20: samples are stored in batches; the final batch writes the summary from every stored sample.
func TestResourcesStoreSummaryAtEnd(t *testing.T) {
	ctx := context.Background()
	s, err := OpenForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r := s.Resources()
	base := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	batch := func(from, to int) []capacity.ResourceSample {
		var out []capacity.ResourceSample
		for i := from; i <= to; i++ {
			out = append(out, capacity.ResourceSample{At: base.Add(time.Duration(i) * 5 * time.Second), CPUMillis: int64(i * 100), MemoryBytes: int64(i) << 20, Known: true})
		}
		return out
	}
	if err := r.Add(ctx, 42, "m1", "r1", batch(1, 10), false, ""); err != nil {
		t.Fatal(err)
	}
	samples, sum, err := r.Job(ctx, 42)
	if err != nil || len(samples) != 10 || sum.JobID != 0 {
		t.Fatalf("running job: %d samples, summary %+v, %v", len(samples), sum, err)
	}
	// The final batch repeats one sample time (ignored) and adds ten more.
	if err := r.Add(ctx, 42, "m1", "r1", batch(10, 20), true, ""); err != nil {
		t.Fatal(err)
	}
	samples, sum, err = r.Job(ctx, 42)
	if err != nil || len(samples) != 20 {
		t.Fatalf("%d samples, %v", len(samples), err)
	}
	if sum.JobID != 42 || sum.Samples != 20 || sum.CPUP50Millis != 1000 || sum.CPUP95Millis != 1900 || sum.CPUPeakMillis != 2000 || sum.MemPeak != 20<<20 {
		t.Fatalf("summary %+v", sum)
	}
	// A job whose agent could not read anything keeps the reason.
	if err := r.Add(ctx, 43, "m1", "r1", []capacity.ResourceSample{{At: base}}, true, "cgroup not readable"); err != nil {
		t.Fatal(err)
	}
	if _, sum, err = r.Job(ctx, 43); err != nil || sum.Samples != 0 || sum.UnknownReason != "cgroup not readable" {
		t.Fatalf("%+v %v", sum, err)
	}
}
