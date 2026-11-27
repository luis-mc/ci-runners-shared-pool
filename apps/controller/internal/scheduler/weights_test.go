package scheduler_test

import (
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

func (e *env) threeSlots() {
	e.settings(m1, func(s *slots.Settings) { s.JobSlots = 3 })
}

// R5.69: a job is admitted when the held weight plus its own fits the budget,
// not when a slot number is free.
func TestAdmissionByWeight(t *testing.T) {
	e := newEnv(t)
	e.threeSlots()
	goes(t, e.gate(e.req("g1", job("o/r", 1, "e2e"), 1, quiet)))  // weighs 2 of 3
	goes(t, e.gate(e.req("g2", job("o/r", 2, "lint"), 1, quiet))) // weighs 1, total 3
	// Two of three slot numbers are used, but the budget is spent.
	d := e.gate(e.req("g3", job("o/r", 3, "unit"), 1, quiet))
	waits(t, d, scheduler.WaitFreeSlot)
	if !strings.Contains(d.WaitText, "weighs 1 CPU and 1 memory") {
		t.Fatalf("wait text %q does not name the weight", d.WaitText)
	}
}

// Counting would let a second e2e job in; weighing does not.
func TestHeavyWeightBlocksWhileSlotNumberFree(t *testing.T) {
	e := newEnv(t)
	e.threeSlots()
	goes(t, e.gate(e.req("g1", job("o/r", 1, "build"), 1, quiet)))
	goes(t, e.gate(e.req("g2", job("o/r", 2, "e2e"), 1, quiet)))
	d := e.gate(e.req("g3", job("o/r", 3, "e2e-web"), 1, quiet))
	waits(t, d, scheduler.WaitFreeSlot)
	if !strings.Contains(d.WaitText, "e2e weighs 2 CPU and 2 memory units; 0 of 3 CPU") {
		t.Fatalf("wait text %q", d.WaitText)
	}
}

// An unknown class weighs one slot, so unclassed jobs are admitted by count as before.
func TestUnknownClassDefaultWeight(t *testing.T) {
	e := newEnv(t)
	e.threeSlots()
	for i, name := range []string{"deploy", "docs", "whatever"} {
		goes(t, e.gate(e.req(name, job("o/r", int64(i+1), name), 1, quiet)))
	}
	d := e.gate(e.req("g4", job("o/r", 4, "extra"), 1, quiet))
	waits(t, d, scheduler.WaitFreeSlot)
	if !strings.Contains(d.WaitText, "no free slot (all 3 busy)") {
		t.Fatalf("wait text %q", d.WaitText)
	}
	w, class := slots.Defaults().WeightOf(slots.JobNames{Job: "deploy"})
	if w != slots.DefaultWeight || class != "default" {
		t.Fatalf("unknown class: %+v %q", w, class)
	}
}

// A weight above the whole budget is cut to it: the job runs alone.
func TestWeightAboveBudgetRunsAlone(t *testing.T) {
	e := newEnv(t) // two slots; vm weighs 2 CPU and 4 memory
	goes(t, e.gate(e.req("g1", job("o/r", 1, "vm"), 1, quiet)))
	waits(t, e.gate(e.req("g2", job("o/r", 2, "lint"), 1, quiet)), scheduler.WaitFreeSlot)
}

// D53 stays: a job whose weight fits still waits while the load is not measured.
func TestWeightFitsButLoadNotMeasured(t *testing.T) {
	e := newEnv(t)
	waits(t, e.gate(e.req("g1", job("o/r", 1, "lint"), 1, scheduler.Load{})), scheduler.WaitLoadNotMeasured)
}

// The reason in words carries the weight (R5.48).
func TestWaitWordsNameTheWeight(t *testing.T) {
	text := scheduler.WeightWaitText("e2e", slots.Weight{CPU: 2, Memory: 2}, slots.Weight{CPU: 2, Memory: 2}, slots.Weight{CPU: 3, Memory: 3})
	got := scheduler.WaitWords(scheduler.WaitFreeSlot, text)
	want := "No free slot: job class e2e weighs 2 CPU and 2 memory units; 1 of 3 CPU and 1 of 3 memory units free."
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
