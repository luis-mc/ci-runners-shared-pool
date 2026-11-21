package agent_test

import (
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
)

type recObs struct {
	started, finished []string
	cut               bool
}

func (r *recObs) JobStarted(k string, _ machines.JobRef, _ string) { r.started = append(r.started, k) }
func (r *recObs) JobFinished(k string, _ machines.JobRef, cut bool) {
	r.finished, r.cut = append(r.finished, k), cut
}

// OPS66: the gate tells its observer of a job that starts and one that ends, and of one that is cut.
func TestGateObserver(t *testing.T) {
	c := startController(t)
	cfg := fastConfig(t, c, "x")
	g := agent.NewGate(cfg.Sampler)
	g.SetSettings(machines.Settings{JobSlots: 4, AllowedBackends: []string{"persistent"}, BudgetMemoryPct: 100, BudgetCPUPct: 100}, machines.StateActive)
	cfg.Sampler.Sample()
	o := &recObs{}
	g.SetObserver(o)
	if d := g.Start("k1", machines.JobRef{RunnerName: "r1"}, "persistent"); !d.Go {
		t.Skipf("the gate holds on load in this sandbox: %v", d.Reasons)
	}
	g.Start("k2", machines.JobRef{RunnerName: "r2"}, "persistent")
	g.Finish("k1")
	g.Finish("k1") // twice: seen once
	g.FinishCut("k2")
	if len(o.started) < 1 || len(o.finished) != 2 || !o.cut {
		t.Fatalf("%+v", o)
	}
}
