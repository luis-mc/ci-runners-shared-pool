package agent_test

import (
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/load"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/power"
)

func gateWith(t *testing.T, memAvailKB string, mutate func(*machines.Settings)) (*agent.Gate, *load.Sampler, *fakeSys) {
	t.Helper()
	p := &fakeSys{linuxBox()}
	p.files["/proc/meminfo"] = "MemTotal: 1000000 kB\nMemAvailable: " + memAvailKB + " kB\n"
	sm := load.NewSampler(p)
	sm.Sample()
	g := agent.NewGate(sm)
	g.SetPower(power.Static{}) // mains: the real power source of the test machine must not matter
	s := machines.DefaultSettings()
	s.JobSlots = 2
	if mutate != nil {
		mutate(&s)
	}
	g.SetSettings(s, machines.StateActive)
	return g, sm, p
}

var job = machines.JobRef{Repository: "o/r", RunID: 1, Job: "build", RunnerName: "r1", StartedAt: time.Now()}

// R5.23: a machine starts a new job only when all hold: state active, a slot
// free, measured load under the budget, the job's backend allowed. Ports
// runner-pool R5.2 cases.
func TestGateStartsOnlyWhenEverythingHolds(t *testing.T) {
	g, _, _ := gateWith(t, "700000", nil) // 30% memory used
	if d := g.Start("a", job, "persistent"); !d.Go {
		t.Fatalf("a good start was held: %v", d.Reasons)
	}
	if d := g.Start("b", job, "persistent"); !d.Go {
		t.Fatalf("second slot: %v", d.Reasons)
	}
	d := g.Start("c", job, "persistent") // both slots taken
	if d.Go || d.Reasons[0] != "free_slot" {
		t.Fatalf("third start: %+v, want held for free_slot", d)
	}
	g.Finish("a")
	if d := g.Start("c", job, "persistent"); !d.Go {
		t.Fatalf("after a job finished: %v", d.Reasons)
	}
	if g.SlotsUsed() != 2 || len(g.Running()) != 2 {
		t.Fatalf("slots = %d", g.SlotsUsed())
	}
}

func TestGateHoldsOverBudgetBackendNotAllowedAndNoSettings(t *testing.T) {
	g, _, _ := gateWith(t, "100000", nil) // 90% memory > 85% budget
	if d := g.Start("a", job, "persistent"); d.Go || d.Reasons[0] != "memory 90% > 85%" {
		t.Fatalf("over budget: %+v", d)
	}
	g2, _, _ := gateWith(t, "700000", nil)
	if d := g2.Start("a", job, "docker"); d.Go || d.Reasons[0] != "backend_not_allowed" {
		t.Fatalf("backend: %+v", d)
	}
	g3 := agent.NewGate(load.NewSampler(&fakeSys{linuxBox()}))
	if d := g3.Start("a", job, "persistent"); d.Go {
		t.Fatal("an agent that knows no settings started a job")
	}
	// No sample yet: load unknown holds the job (R5.20).
	g4 := agent.NewGate(load.NewSampler(&fakeSys{linuxBox()}))
	g4.SetSettings(machines.DefaultSettings(), machines.StateActive)
	if d := g4.Start("a", job, "persistent"); d.Go || len(d.Reasons) == 0 {
		t.Fatalf("no sample yet: %+v", d)
	}
}

// R5.25: a settings change takes effect for the next job; running jobs keep
// going even when the new budget is already exceeded.
func TestGateRunningJobsSurviveALowerBudget(t *testing.T) {
	g, _, _ := gateWith(t, "400000", nil) // 60% used
	if d := g.Start("a", job, "persistent"); !d.Go {
		t.Fatal(d.Reasons)
	}
	s, _ := g.Settings()
	s.BudgetMemoryPct = 20
	s.JobSlots = 0
	g.SetSettings(s, machines.StateActive)
	if len(g.Running()) != 1 {
		t.Fatal("the running job was dropped")
	}
	if d := g.Start("b", job, "persistent"); d.Go {
		t.Fatal("the next job ignored the new budget")
	}
}

// R5.21: the gate only holds new jobs; it never stops one (it has no way to).
func TestGateNeverStopsAJob(t *testing.T) {
	g, _, p := gateWith(t, "100000", nil)
	g.Start("a", job, "persistent")
	g.SetSettings(machines.DefaultSettings(), machines.StateDrained)
	if len(p.calls) != 0 {
		t.Fatalf("the gate ran commands: %v", p.calls)
	}
}
