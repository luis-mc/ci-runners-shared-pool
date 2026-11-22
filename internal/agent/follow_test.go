package agent_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/internal/update"
)

// fakeUpdater is the agent's update manager: it records the asked version and
// walks through the states a real one reports.
type fakeUpdater struct {
	mu    sync.Mutex
	asked []string
	st    update.Status
	err   error
	// restarts counts the Restart calls (CRSP235).
	restarts int
}

func (f *fakeUpdater) Restart(context.Context) (update.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarts++
	return f.st, f.err
}

func (f *fakeUpdater) Follow(_ context.Context, v string) (update.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, v)
	return f.st, f.err
}
func (f *fakeUpdater) Status(context.Context) update.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st
}
func (f *fakeUpdater) set(s update.Status) { f.mu.Lock(); f.st = s; f.mu.Unlock() }

func joinedAgent(t *testing.T, up agent.Updater) (*ctl, string) {
	t.Helper()
	c := startController(t)
	ctx := context.Background()
	code, _ := c.Svc.CreateJoinCode(ctx, "owner", 0)
	cfg := fastConfig(t, c, code.Code)
	cfg.Updater = up
	runAgent(t, cfg)
	var id string
	waitFor(t, "agent online", func() bool {
		as, _ := c.Svc.List(ctx)
		if len(as) == 1 && as[0].Machine.Online {
			id = as[0].Machine.ID
		}
		return id != ""
	})
	return c, id
}

func agentUpdate(c *ctl, id string) machines.AgentUpdate {
	as, _ := c.Svc.Agents(context.Background())
	for _, a := range as {
		if a.ID == id {
			return a.Update
		}
	}
	return machines.AgentUpdate{}
}

func TestAgentFollowsTheControllerOverTheLink(t *testing.T) {
	up := &fakeUpdater{st: update.Status{State: update.StateWaitingForJobs, RunningJobs: []update.Job{{Repository: "o/r", RunID: 9, Job: "build"}}}}
	c, id := joinedAgent(t, up)
	if err := c.Svc.AskUpdate(id, "1.5.0"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the agent waits for the job", func() bool {
		u := agentUpdate(c, id)
		return u.State == "waiting_for_jobs" && u.Version == "1.5.0" && len(u.Waiting) == 1 && u.Waiting[0].RunID == 9 && u.Active()
	})
	if len(up.asked) != 1 || up.asked[0] != "1.5.0" {
		t.Fatalf("asked %v", up.asked)
	}
	// The drain limit passed: the manager is idle again with its note; the agent says it postponed.
	up.set(update.Status{State: update.StateIdle, Error: "Running jobs did not end within 1h0m0s. The update tries again at the next check."})
	waitFor(t, "postponed", func() bool { u := agentUpdate(c, id); return u.State == "postponed" && !u.Active() })
	// The controller asks again at its next tick; this time the job is gone and the install starts.
	up.set(update.Status{State: update.StateInstalling})
	_ = c.Svc.AskUpdate(id, "1.5.0")
	waitFor(t, "installing", func() bool { return agentUpdate(c, id).State == "installing" })
}

func TestAgentReportsFailureAndUnsupported(t *testing.T) {
	up := &fakeUpdater{err: update.ErrUnprivileged}
	c, id := joinedAgent(t, up)
	_ = c.Svc.AskUpdate(id, "1.5.0")
	waitFor(t, "unsupported", func() bool { return agentUpdate(c, id).State == "unsupported" })
	if u := agentUpdate(c, id); !strings.Contains(u.Error, "run the install command again") {
		t.Fatalf("the report must say how to fix it: %q", u.Error)
	}

	up2 := &fakeUpdater{err: &update.Failure{Check: "provenance", Reason: "no attestation"}}
	c2, id2 := joinedAgent(t, up2)
	_ = c2.Svc.AskUpdate(id2, "1.5.0")
	waitFor(t, "failed", func() bool {
		u := agentUpdate(c2, id2)
		return u.State == "failed" && u.Error != ""
	})

	// No updater at all: the agent says so instead of staying silent.
	c3, id3 := joinedAgent(t, nil)
	_ = c3.Svc.AskUpdate(id3, "1.5.0")
	waitFor(t, "no updater", func() bool { return agentUpdate(c3, id3).State == "unsupported" })

	// Already current: idle.
	up4 := &fakeUpdater{err: update.ErrNoUpdate}
	c4, id4 := joinedAgent(t, up4)
	_ = c4.Svc.AskUpdate(id4, "1.5.0")
	waitFor(t, "idle", func() bool { return agentUpdate(c4, id4).State == "idle" })
}

// The agent's drainer holds new jobs out, waits for the running job (never cuts
// it), and starts the install only when idle.
func TestUpdateDrainerWaitsForTheRunningJobAndNeverCutsIt(t *testing.T) {
	g, _, _ := gateWith(t, "700000", nil)
	if d := g.Start("j1", job, "persistent"); !d.Go {
		t.Fatal(d.Reasons)
	}
	idled := make(chan update.Kind, 1)
	d := &agent.UpdateDrainer{Gate: g, Poll: 5 * time.Millisecond, OnIdle: func(_ context.Context, k update.Kind) error { idled <- k; return nil }}
	if err := d.BeginDrain(context.Background(), update.KindUpdate, "follow-controller"); err != nil {
		t.Fatal(err)
	}
	// The first poll held the gate and found the job: no install yet.
	waitFor(t, "the drain holds the gate", func() bool { return g.State() != machines.StateActive })
	select {
	case <-idled:
		t.Fatal("installed while a job ran")
	default:
	}
	if g.Start("j2", job, "persistent").Go {
		t.Fatal("a job started during the drain")
	}
	g.Finish("j1")
	select {
	case k := <-idled:
		if k != update.KindUpdate {
			t.Fatal(k)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the install must start once idle")
	}

	// Cancel (the 60-minute limit) lets jobs start again.
	g2, _, _ := gateWith(t, "700000", nil)
	d2 := &agent.UpdateDrainer{Gate: g2, Poll: 5 * time.Millisecond, OnIdle: func(context.Context, update.Kind) error { return update.ErrNotIdle }}
	g2.Start("j3", job, "persistent")
	_ = d2.BeginDrain(context.Background(), update.KindUpdate, "x")
	waitFor(t, "the gate to hold during the drain", func() bool { return g2.State() != machines.StateActive })
	_ = d2.CancelDrain(context.Background(), "x")
	if g2.State() != machines.StateActive {
		t.Fatalf("state %s", g2.State())
	}
}

// Machines R5.33: the update drain waits as long as the machine's drain timeout
// setting says; 60 minutes (the default) until the agent has settings.
func TestUpdateDrainerTimeoutIsTheMachineSetting(t *testing.T) {
	g, _, _ := gateWith(t, "700000", func(s *machines.Settings) { s.DrainTimeoutMinutes = 135 })
	d := &agent.UpdateDrainer{Gate: g}
	if got := d.DrainTimeoutMinutes(context.Background()); got != 135 {
		t.Errorf("drain timeout %d, want the machine setting 135", got)
	}
	g.SetSettings(machines.Settings{}, machines.StateActive) // an unset value
	if got := d.DrainTimeoutMinutes(context.Background()); got != 60 {
		t.Errorf("unset: %d, want 60", got)
	}
}

// CRSP235: the console's Restart the service reaches the agent only after the
// drain found the machine idle; the agent then asks its manager to restart.
func TestAgentRestartsItsServiceWhenTheControllerAsks(t *testing.T) {
	up := &fakeUpdater{}
	c, id := joinedAgent(t, up)
	if _, err := c.Svc.RestartService(context.Background(), "owner", id, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the agent was asked to restart", func() bool {
		up.mu.Lock()
		defer up.mu.Unlock()
		return up.restarts == 1
	})
}

// A restart the agent cannot do is said, not left silent.
func TestAgentSaysItCannotRestart(t *testing.T) {
	up := &fakeUpdater{err: update.ErrUnprivileged}
	c, id := joinedAgent(t, up)
	if _, err := c.Svc.RestartService(context.Background(), "owner", id, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "unsupported", func() bool { return agentUpdate(c, id).State == "unsupported" })
	waitFor(t, "the change needs the owner", func() bool {
		v, _ := c.Svc.Get(context.Background(), id)
		return v.PendingChange != nil && v.PendingChange.State == machines.ChangeNeedsYou
	})
}
