package agent_test

import (
	"context"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// R5.30 to R5.34 end to end with a real agent: drain holds new jobs, names the
// running one, waits, and when the job finishes the machine is drained; resume
// lets jobs start again. Nothing is ever stopped.
func TestDrainAndResumeWithARealAgent(t *testing.T) {
	c := startController(t)
	ctx := context.Background()
	code, _ := c.Svc.CreateJoinCode(ctx, "owner", 0)
	cfg := fastConfig(t, c, code.Code)
	cfg.Gate = agent.NewGate(cfg.Sampler)
	runAgent(t, cfg)
	var id string
	waitFor(t, "ready", func() bool {
		vs, _ := c.Svc.List(ctx)
		if len(vs) == 1 && vs[0].Machine.Online {
			id = vs[0].Machine.ID
		}
		_, have := cfg.Gate.Settings()
		_, sampled := cfg.Sampler.Latest()
		return id != "" && have && sampled
	})
	jr := machines.JobRef{Repository: "o/r", RunID: 5, Job: "test", RunnerName: "r1"}
	if d := cfg.Gate.Start("j1", jr, "persistent"); !d.Go {
		t.Fatal(d.Reasons)
	}
	ch, err := c.Svc.Drain(ctx, "owner", id, "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the job is named", func() bool {
		cc, _ := c.Svc.GetChange(ctx, id, ch.ID)
		return len(cc.Detail.WaitingFor) == 1 && cc.Detail.WaitingFor[0].RunID == 5
	})
	if d := cfg.Gate.Start("j2", jr, "persistent"); d.Go {
		t.Fatal("a job started during the drain")
	}
	if cfg.Gate.SlotsUsed() != 1 {
		t.Fatal("the running job was touched")
	}
	cfg.Gate.Finish("j1")
	waitFor(t, "drained", func() bool { v, _ := c.Svc.Get(ctx, id); return v.Machine.State == machines.StateDrained })
	waitFor(t, "agent drained", func() bool { return cfg.Gate.State() == machines.StateDrained })
	if _, err := c.Svc.Resume(ctx, "owner", id, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "agent active", func() bool { return cfg.Gate.State() == machines.StateActive })
	if d := cfg.Gate.Start("j3", jr, "persistent"); !d.Go {
		t.Fatalf("after resume: %v", d.Reasons)
	}
}
