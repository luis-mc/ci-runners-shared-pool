package assign

import (
	"context"
	"errors"
	"testing"
)

// fakeGate is the listener gate: it records what the controller asked.
type fakeGate struct {
	enforce bool
	err     error
	admits  []GateJob
	done    []int64
	held    map[int64]bool
}

func (g *fakeGate) Admit(_ context.Context, j GateJob) (GateRunner, bool, error) {
	if g.err != nil {
		return GateRunner{}, false, g.err
	}
	g.admits = append(g.admits, j)
	if !g.enforce {
		return GateRunner{}, false, nil // observe mode: written, not taken
	}
	if g.held == nil {
		g.held = map[int64]bool{}
	}
	g.held[j.JobID] = true
	return GateRunner{Name: "box-1"}, true, nil
}
func (g *fakeGate) Holds(_ context.Context, id int64) bool { return g.held[id] }
func (g *fakeGate) Done(_ context.Context, id int64) error {
	g.done = append(g.done, id)
	delete(g.held, id)
	return nil
}

func TestGatedRunnerTakesTheJobAndNoJITRunnerIsMade(t *testing.T) {
	r := newRig(t, m1(8, 16))
	g := &fakeGate{enforce: true}
	r.c.Gate = g
	must(t, r.c.Queued(ctx, job(1, "e2e")))
	if len(r.rn.started) != 0 {
		t.Fatalf("a JIT runner was made: %d", len(r.rn.started))
	}
	l := r.lease(1)
	if l.State != Starting || l.RunnerName != "box-1" || l.RunnerRecord != "" || l.MachineID != "m1" {
		t.Fatalf("lease: %+v", l)
	}
	if len(g.admits) != 1 || g.admits[0].JobLabel != e2eLabel || g.admits[0].MachineID != "m1" {
		t.Errorf("admit: %+v", g.admits)
	}
}

func TestObserveModeLeavesTheJITFlowAsItWas(t *testing.T) {
	r := newRig(t, m1(8, 16))
	g := &fakeGate{}
	r.c.Gate = g
	must(t, r.c.Queued(ctx, job(1, "e2e")))
	if len(r.rn.started) != 1 || len(g.admits) != 1 {
		t.Fatalf("runners %d admits %d", len(r.rn.started), len(g.admits))
	}
}

func TestCompletedTellsTheGateAndNeverFinishesAPersistentRunner(t *testing.T) {
	r := newRig(t, m1(8, 16))
	g := &fakeGate{enforce: true}
	r.c.Gate = g
	must(t, r.c.Queued(ctx, job(1, "e2e")))
	j := job(1, "e2e")
	j.RunnerName, j.Status = "box-1", "in_progress"
	must(t, r.c.InProgress(ctx, j))
	if r.lease(1).State != Running {
		t.Fatalf("lease: %+v", r.lease(1))
	}
	must(t, r.c.Completed(ctx, j))
	if len(g.done) != 1 || g.done[0] != 1 || len(r.rn.finished) != 0 {
		t.Errorf("done %v finished %v", g.done, r.rn.finished)
	}
	if r.lease(1).State != Done {
		t.Errorf("lease: %+v", r.lease(1))
	}
}

func TestGateFailureKeepsTheJobWaitingWithABackoff(t *testing.T) {
	r := newRig(t, m1(8, 16))
	r.c.Gate = &fakeGate{enforce: true, err: errors.New("labels API answered 500")}
	must(t, r.c.Queued(ctx, job(1, "e2e")))
	l := r.lease(1)
	if l.State != Waiting || l.ReasonCode != CodeBackoff || l.Fails != 1 || len(r.rn.started) != 0 {
		t.Fatalf("lease: %+v runners %d", l, len(r.rn.started))
	}
}

func TestUnusedGatedRunnerIsStoppedNotDeleted(t *testing.T) {
	r := newRig(t, m1(8, 16))
	g := &fakeGate{enforce: true}
	r.c.Gate = g
	must(t, r.c.Queued(ctx, job(1, "e2e")))
	r.clk.advance(UnusedGrace + 10*60*1e9)
	must(t, r.c.Sweep(ctx))
	if len(g.done) != 1 || len(r.rn.removed) != 0 {
		t.Fatalf("done %v removed %v", g.done, r.rn.removed)
	}
}
