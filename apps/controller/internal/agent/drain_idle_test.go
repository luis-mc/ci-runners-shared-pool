package agent_test

import (
	"sync"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// R5.32: the idle check and the hold are one step. After Hold has said "idle",
// no job can start; before it, a job that started is named.
func TestHoldIsTheIdleCheckAndKeepsNewJobsOut(t *testing.T) {
	g, _, _ := gateWith(t, "700000", nil)
	if d := g.Start("a", job, "persistent"); !d.Go {
		t.Fatal(d.Reasons)
	}
	idle, waiting := g.Hold()
	if idle || len(waiting) != 1 || waiting[0].Repository != "o/r" {
		t.Fatalf("hold with a running job: idle=%v waiting=%v", idle, waiting)
	}
	if d := g.Start("b", job, "persistent"); d.Go || d.Reasons[0] != "machine_draining" {
		t.Fatalf("a job started during the drain: %+v", d)
	}
	g.Finish("a")
	idle, waiting = g.Hold() // a fresh check, never a cached one
	if !idle || len(waiting) != 0 {
		t.Fatalf("idle = %v waiting = %v", idle, waiting)
	}
	if d := g.Start("c", job, "persistent"); d.Go {
		t.Fatal("a job started after the idle check")
	}
	// Resume lets jobs start again (R5.34).
	g.Release()
	if d := g.Start("c", job, "persistent"); !d.Go {
		t.Fatalf("after resume: %v", d.Reasons)
	}
}

// Racing starts against Hold: once Hold reported idle, nothing may be running,
// whatever the interleaving.
func TestHoldAndStartNeverRace(t *testing.T) {
	for i := 0; i < 200; i++ {
		g, _, _ := gateWith(t, "700000", func(s *machines.Settings) { s.JobSlots = 64 })
		var wg sync.WaitGroup
		wg.Add(2)
		var idle bool
		go func() { defer wg.Done(); idle, _ = g.Hold() }()
		go func() { defer wg.Done(); g.Start("x", job, "persistent") }()
		wg.Wait()
		if idle && g.SlotsUsed() != 0 {
			t.Fatalf("round %d: Hold said idle but a job started", i)
		}
	}
}

// CRSP38, machines R5.57: a drain for runner set X waits only for jobs on X and
// keeps new jobs out of X only; a busy runner outside X never holds it, even
// when it belongs to the same repository.
func TestScopedHoldCountsOnlyItsOwnRunners(t *testing.T) {
	g, _, _ := gateWith(t, "700000", func(s *machines.Settings) { s.JobSlots = 8 })
	mine := machines.JobRef{Repository: "o/r", RunID: 1, Job: "build", RunnerName: "runner-1"}
	other := machines.JobRef{Repository: "o/r", RunID: 1, Job: "test", RunnerName: "runner-2"}
	if !g.Start("a", mine, "persistent").Go || !g.Start("b", other, "persistent").Go {
		t.Fatal("setup")
	}
	idle, waiting := g.HoldRunners([]string{"Runner-1"})
	if idle || len(waiting) != 1 || waiting[0].RunnerName != "runner-1" {
		t.Fatalf("idle=%v waiting=%v: only runner-1 counts", idle, waiting)
	}
	if d := g.Start("c", mine, "persistent"); d.Go || d.Reasons[0] != "runner_draining" {
		t.Fatalf("a new job started on the draining runner: %+v", d)
	}
	if d := g.Start("d", machines.JobRef{Repository: "o/r", RunID: 2, Job: "x", RunnerName: "runner-3"}, "persistent"); !d.Go {
		t.Fatalf("a runner outside the set was held: %v", d.Reasons)
	}
	if g.State() != machines.StateActive {
		t.Fatalf("state = %s: the machine stays active", g.State())
	}
	g.Finish("a") // runner-2 is still busy
	if idle, _ := g.HoldRunners([]string{"runner-1"}); !idle {
		t.Fatal("runner-1 is idle; the busy runner-2 must not hold the drain")
	}
	g.Release()
	if d := g.Start("e", mine, "persistent"); !d.Go {
		t.Fatalf("after resume: %v", d.Reasons)
	}
}

// With no runner names the hold is the machine's, as before.
func TestHoldRunnersWithoutNamesIsTheWholeMachine(t *testing.T) {
	g, _, _ := gateWith(t, "700000", nil)
	if !g.Start("a", job, "persistent").Go {
		t.Fatal("setup")
	}
	if idle, _ := g.HoldRunners(nil); idle || g.State() != machines.StateDraining {
		t.Fatalf("idle=%v state=%s", idle, g.State())
	}
}
