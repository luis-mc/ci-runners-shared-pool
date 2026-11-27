package scheduler_test

import (
	"context"
	"errors"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

func ephemeral(run int64, name, runner string) scheduler.JobRef {
	j := job("o/r", run, name)
	j.RunnerName = runner
	return j
}

func queued(t *testing.T, e *env) int64 {
	t.Helper()
	n, err := e.st.CountJobs(context.Background(), "queued")
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// R5.50: the slot, its log row and the start-runner work row come together.
func TestPlacementTakesSlotInOneTransaction(t *testing.T) {
	e := newEnv(t)
	before := queued(t, e)
	p, err := e.eng.Place(context.Background(), m1, ephemeral(1, "build", "eph-1"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Grant.Kind != scheduler.SlotCI || p.Grant.Number != 1 || p.Existing {
		t.Fatalf("placement = %+v", p)
	}
	if got := queued(t, e); got != before+1 {
		t.Fatalf("job_queue rows %d -> %d, want one start_runner row", before, got)
	}
	if s := e.status(m1); len(s.Slots) != 1 || s.Slots[0].Job.RunnerName != "eph-1" {
		t.Fatalf("slots = %+v", s.Slots)
	}
}

// R5.50: no free slot, nothing recorded and nothing enqueued.
func TestPlacementWithNoFreeSlotRecordsNothing(t *testing.T) {
	e := newEnv(t)
	e.settings(m1, func(s *slots.Settings) { s.JobSlots = 1 })
	if _, err := e.eng.Place(context.Background(), m1, ephemeral(1, "a", "eph-1")); err != nil {
		t.Fatal(err)
	}
	before := queued(t, e)
	if _, err := e.eng.Place(context.Background(), m1, ephemeral(2, "b", "eph-2")); !errors.Is(err, scheduler.ErrNoFreeSlot) {
		t.Fatalf("err = %v", err)
	}
	if queued(t, e) != before || len(e.status(m1).Slots) != 1 {
		t.Fatal("a refused placement left a row behind")
	}
}

// R5.50: the same job placed twice is one placement.
func TestPlacingTheSameJobTwiceIsOnePlacement(t *testing.T) {
	e := newEnv(t)
	first, _ := e.eng.Place(context.Background(), m1, ephemeral(1, "build", "eph-1"))
	before := queued(t, e)
	again, err := e.eng.Place(context.Background(), m1, ephemeral(1, "build", "eph-1"))
	if err != nil || !again.Existing || again.Grant.Number != first.Grant.Number {
		t.Fatalf("again = %+v %v", again, err)
	}
	if queued(t, e) != before || len(e.status(m1).Slots) != 1 {
		t.Fatal("the second placement added rows")
	}
}

// R5.50: the slot is freed when GitHub reports the job completed.
func TestPlacedSlotFreedWhenGitHubReportsCompleted(t *testing.T) {
	e := newEnv(t)
	if _, err := e.eng.Place(context.Background(), m1, ephemeral(1, "build", "eph-1")); err != nil {
		t.Fatal(err)
	}
	freed, err := e.eng.CompletedOnGitHub(context.Background(), ephemeral(1, "build", "eph-1"))
	if err != nil || len(freed) != 1 || len(e.status(m1).Slots) != 0 {
		t.Fatalf("freed = %+v %v", freed, err)
	}
}

// R5.50: the slot is freed when the runner is gone, and gates are woken.
func TestPlacedSlotFreedWhenTheRunnerIsGone(t *testing.T) {
	e := newEnv(t)
	if _, err := e.eng.Place(context.Background(), m1, ephemeral(1, "build", "eph-1")); err != nil {
		t.Fatal(err)
	}
	before := queued(t, e)
	freed, err := e.eng.RunnerGone(context.Background(), m1, "eph-1")
	if err != nil || len(freed) != 1 || freed[0].Reason != "runner-gone" || len(e.status(m1).Slots) != 0 {
		t.Fatalf("freed = %+v %v", freed, err)
	}
	if queued(t, e) != before+1 {
		t.Fatal("freeing the slot must enqueue a wake_gates row")
	}
	if freed, _ := e.eng.RunnerGone(context.Background(), m1, "eph-1"); len(freed) != 0 {
		t.Fatal("a second call freed something")
	}
}

// A placed job holds a real slot: a gate cannot take it.
func TestPlacedSlotCountsAgainstTheGate(t *testing.T) {
	e := newEnv(t)
	e.settings(m1, func(s *slots.Settings) { s.JobSlots = 1 })
	if _, err := e.eng.Place(context.Background(), m1, ephemeral(1, "a", "eph-1")); err != nil {
		t.Fatal(err)
	}
	waits(t, e.gate(e.req("g", job("o/r", 2, "b"), 1, quiet)), scheduler.WaitFreeSlot)
}
