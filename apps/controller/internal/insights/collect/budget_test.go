package collect

import (
	"context"
	"testing"
	"time"
)

// R6.1: at most a quarter of the hourly limit; a lower priority stops earlier so the work before it keeps
// room; work over the share waits for the next hour; the half-limit stop grants nothing.
func TestQuarterShareAndOrder(t *testing.T) {
	ctx := context.Background()
	clk := &clock{t: time.Date(2026, 11, 20, 10, 30, 0, 0, time.UTC)}
	halted := false
	b := &HourlyBudget{Now: clk.now, Limit: func(int64) int { return 1000 }, Halted: func(int64) bool { return halted }}

	// commit trees (last in order) get 35 percent of the 250 calls of the share = 87
	n := 0
	for ok, _ := b.Take(ctx, 1, PriorityCommitTrees, 1); ok; ok, _ = b.Take(ctx, 1, PriorityCommitTrees, 1) {
		n++
	}
	if n != 87 {
		t.Fatalf("commit trees got %d calls, want 87", n)
	}
	// logs may go on up to 50 percent, then artifacts to 65, workflow files 80, steps 90, reconcile 100
	steps := []struct {
		p    Priority
		want int // used after this priority is exhausted
	}{{PriorityLogs, 125}, {PriorityArtifacts, 162}, {PriorityWorkflowFiles, 200}, {PrioritySteps, 225}, {PriorityReconcile, 250}}
	for _, s := range steps {
		for ok, _ := b.Take(ctx, 1, s.p, 1); ok; ok, _ = b.Take(ctx, 1, s.p, 1) {
		}
		if got := b.Used(1); got != s.want {
			t.Fatalf("after priority %d used = %d, want %d", s.p, got, s.want)
		}
	}
	ok, until := b.Take(ctx, 1, PriorityReconcile, 1)
	if ok || !until.Equal(time.Date(2026, 11, 20, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("over the share: ok=%v until=%v, want the next hour", ok, until)
	}
	// another installation has its own share
	if ok, _ := b.Take(ctx, 2, PriorityLogs, 1); !ok {
		t.Fatal("installation 2 was held back by installation 1")
	}
	// the next hour starts again
	clk.add(31 * time.Minute)
	if ok, _ := b.Take(ctx, 1, PriorityLogs, 5); !ok || b.Used(1) != 5 {
		t.Fatalf("next hour: ok=%v used=%d", ok, b.Used(1))
	}
	// a request that does not fit whole is refused whole
	if ok, _ := b.Take(ctx, 1, PriorityLogs, 500); ok || b.Used(1) != 5 {
		t.Fatalf("a call of 500 was granted or charged: used=%d", b.Used(1))
	}
	halted = true
	if ok, until := b.Take(ctx, 1, PriorityReconcile, 1); ok || until.IsZero() {
		t.Fatal("a call was granted at the half-limit stop")
	}
	b.SetBacklog(4)
	if jobs, until := b.Backlog(); jobs != 4 || until.IsZero() {
		t.Fatalf("backlog = %d %v", jobs, until)
	}
}

// R6.1: when the share is spent the collector keeps its place in the queue and reports the backlog; the next
// hour it goes on.
func TestCollectorWaitsForTheShare(t *testing.T) {
	r := newRig(t)
	b := &HourlyBudget{Now: r.clk.now, Limit: func(int64) int { return 8 }} // share 2, logs may use 1
	r.build(b)
	for id := int64(1); id <= 3; id++ {
		r.gh.logs[id] = "x\n"
		r.svc.JobCompleted(context.Background(), 3, 900, 1, id)
	}
	handled, _, err := r.svc.Step(context.Background())
	if !handled || err != nil {
		t.Fatalf("first step = %v %v", handled, err)
	}
	handled, until, err := r.svc.Step(context.Background())
	if handled || err != nil || until.IsZero() {
		t.Fatalf("second step = %v %v %v, want to wait", handled, until, err)
	}
	if jobs, _ := b.Backlog(); jobs != 2 || r.svc.Pending() != 2 {
		t.Fatalf("backlog = %d, pending = %d, want 2", jobs, r.svc.Pending())
	}
	r.clk.add(time.Hour)
	if handled, _, err := r.svc.Step(context.Background()); !handled || err != nil {
		t.Fatalf("after the hour = %v %v", handled, err)
	}
	if r.gh.logCalls != 2 {
		t.Fatalf("log calls = %d, want 2", r.gh.logCalls)
	}
}
