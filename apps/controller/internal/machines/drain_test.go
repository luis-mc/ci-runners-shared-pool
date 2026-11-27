package machines_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

var oneJob = &agentv1.JobRef{Repository: "o/r", RunId: 42, Job: "build", RunnerName: "runner-1"}

func running(t *testing.T) (*env, string, *fakeAgent) {
	t.Helper()
	e, id, key := setup(t)
	fa, err := e.session(id, key, &agentv1.Hello{Holders: &agentv1.HolderReport{Running: []*agentv1.JobRef{oneJob}}})
	if err != nil {
		t.Fatal(err)
	}
	return e, id, fa
}

// R5.30: drain stops new jobs; running jobs finish; the change names each job it
// waits for. Nothing is ever stopped.
func TestDrainWaitsForRunningJobsAndNamesThem(t *testing.T) {
	e, id, fa := running(t)
	ch, err := e.Svc.Drain(context.Background(), "owner", id, "maintenance")
	if err != nil {
		t.Fatal(err)
	}
	if ch.State != machines.ChangeWaiting || len(ch.Detail.WaitingFor) != 1 || ch.Detail.WaitingFor[0].Repository != "o/r" {
		t.Fatalf("change = %+v", ch)
	}
	d := fa.Next(isDrain).GetDrain()
	if d.GetCommandId() != ch.ID || d.GetChangeKind() != machines.KindDrain || d.GetTimeoutSeconds() != 3600 {
		t.Fatalf("drain command = %+v", d)
	}
	v, _ := e.Svc.Get(context.Background(), id)
	if v.Machine.State != machines.StateDraining || v.PendingChange == nil {
		t.Fatalf("state = %v", v.Machine.State)
	}
	// The agent says what it still waits for.
	fa.Send(drainStatus(ch.ID, false, oneJob))
	waitFor(t, "waiting_for stored", func() bool {
		c, _ := e.Svc.GetChange(context.Background(), id, ch.ID)
		return len(c.Detail.WaitingFor) == 1 && c.State == machines.ChangeWaiting
	})
	// Idle: the machine is drained.
	fa.Send(drainStatus(ch.ID, true))
	waitFor(t, "drained", func() bool {
		v, _ := e.Svc.Get(context.Background(), id)
		return v.Machine.State == machines.StateDrained
	})
	c, _ := e.Svc.GetChange(context.Background(), id, ch.ID)
	if c.State != machines.ChangeDone {
		t.Fatalf("change state = %v", c.State)
	}
}

// R5.34: resume returns a drained machine to active; a change still waiting is
// cancelled first, and that needs a yes.
func TestResumeCancelsWaitingChangeAfterAYes(t *testing.T) {
	e, id, fa := running(t)
	ch, _ := e.Svc.Drain(context.Background(), "owner", id, "")
	fa.Next(isDrain)
	_, err := e.Svc.Resume(context.Background(), "owner", id, false)
	var nc *machines.NeedsConfirmationError
	if !errors.As(err, &nc) || len(nc.Names) < 2 || !strings.Contains(strings.Join(nc.Names, "\n"), "o/r") {
		t.Fatalf("err = %v, want needs confirmation naming the change and the job", err)
	}
	v, err := e.Svc.Resume(context.Background(), "owner", id, true)
	if err != nil || v.Machine.State != machines.StateActive {
		t.Fatalf("resume: %v %v", v.Machine.State, err)
	}
	fa.Next(isResume)
	c, _ := e.Svc.GetChange(context.Background(), id, ch.ID)
	if c.State != machines.ChangeCancel {
		t.Fatalf("change = %v, want cancelled", c.State)
	}
	// And from drained, a plain resume needs no yes.
	ch2, _ := e.Svc.Drain(context.Background(), "owner", id, "")
	fa.Send(drainStatus(ch2.ID, true))
	waitFor(t, "drained", func() bool {
		v, _ := e.Svc.Get(context.Background(), id)
		return v.Machine.State == machines.StateDrained
	})
	if v, err := e.Svc.Resume(context.Background(), "owner", id, false); err != nil || v.Machine.State != machines.StateActive {
		t.Fatalf("resume from drained: %v %v", v.Machine.State, err)
	}
}

// R5.26, R5.31: a settings change that restarts runners (allowed backends)
// drains first, names the jobs, needs a yes, and applies only once idle.
func TestBackendChangeDrainsFirstThenApplies(t *testing.T) {
	e, id, fa := running(t)
	up := machines.SettingsUpdate{AllowedBackends: ptr([]string{"persistent", "docker"})}
	_, _, err := e.Svc.UpdateSettings(context.Background(), "owner", id, up)
	var nc *machines.NeedsConfirmationError
	if !errors.As(err, &nc) || len(nc.Names) != 1 {
		t.Fatalf("err = %v, want needs confirmation with the job named", err)
	}
	up.Confirm = true
	_, ch, err := e.Svc.UpdateSettings(context.Background(), "owner", id, up)
	if err != nil || ch == nil || ch.Kind != machines.KindSettings {
		t.Fatalf("ch=%v err=%v", ch, err)
	}
	v, _ := e.Svc.Get(context.Background(), id)
	if len(v.Settings.AllowedBackends) != 1 {
		t.Fatalf("backends applied before the drain finished: %v", v.Settings.AllowedBackends)
	}
	fa.Next(isDrain)
	// A second restarting change is refused while one waits.
	if _, _, err := e.Svc.UpdateSettings(context.Background(), "owner", id, up); !errors.Is(err, machines.ErrConflict) {
		t.Fatalf("second change: %v", err)
	}
	fa.Send(drainStatus(ch.ID, true))
	fa.Next(isResume)
	waitFor(t, "applied", func() bool {
		v, _ := e.Svc.Get(context.Background(), id)
		return len(v.Settings.AllowedBackends) == 2 && v.Machine.State == machines.StateActive
	})
	m := fa.Next(isSettings)
	if len(m.GetSettings().GetAllowedBackends()) != 2 {
		t.Fatalf("pushed = %+v", m.GetSettings())
	}
}

// R5.31: agent updates, image rebuilds and service restarts drain first and call
// OnIdle only once the machine reports idle.
func TestOtherRestartsDrainFirst(t *testing.T) {
	var mu sync.Mutex
	var got []machines.Change
	seen := func() []machines.Change { mu.Lock(); defer mu.Unlock(); return append([]machines.Change(nil), got...) }
	e := newEnv(t, func(o *machines.Options) {
		o.OnIdle = func(_ context.Context, c machines.Change) error {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, c)
			return nil
		}
	})
	r, _ := e.joined("box-1")
	fa, err := e.session(r.GetMachineId(), r.GetMachineCredential(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := e.Svc.StartChange(context.Background(), "system", r.GetMachineId(), machines.KindAgentUpdate, "new version")
	if err != nil {
		t.Fatal(err)
	}
	if len(seen()) != 0 {
		t.Fatal("OnIdle ran before the drain finished")
	}
	fa.Next(isDrain)
	fa.Send(drainStatus(ch.ID, true))
	waitFor(t, "on idle", func() bool { return len(seen()) == 1 })
	if k := seen()[0].Kind; k != machines.KindAgentUpdate {
		t.Fatalf("got %v", k)
	}
	if _, err := e.Svc.StartChange(context.Background(), "system", r.GetMachineId(), "bogus", ""); !errors.Is(err, machines.ErrInvalid) {
		t.Fatalf("bogus kind: %v", err)
	}
}

// R5.32: the controller acts only on an idle report for this change's command
// id. A report for an old command, or one that is not idle, never counts.
func TestOnlyAnIdleReportForThisChangeCounts(t *testing.T) {
	e, id, fa := running(t)
	ch, _ := e.Svc.Drain(context.Background(), "owner", id, "")
	fa.Next(isDrain)
	fa.Send(drainStatus("ch_old", true))
	fa.Send(drainStatus(ch.ID, false, oneJob))
	fa.Send(heartbeat())
	fa.Next(func(m *agentv1.SessionResponse) bool { return m.GetHeartbeat() != nil })
	v, _ := e.Svc.Get(context.Background(), id)
	if v.Machine.State != machines.StateDraining {
		t.Fatalf("state = %v: an old or non-idle report drained the machine", v.Machine.State)
	}
	// A job that started since the drain began makes the agent say "not idle"
	// again; the controller keeps waiting. A fresh idle report finishes it.
	fa.Send(drainStatus(ch.ID, true))
	waitFor(t, "drained", func() bool {
		v, _ := e.Svc.Get(context.Background(), id)
		return v.Machine.State == machines.StateDrained
	})
}

func TestDrainOfflineMachineStillWaits(t *testing.T) {
	e, id, _ := setup(t)
	ch, err := e.Svc.Drain(context.Background(), "owner", id, "")
	if err != nil || ch.State != machines.ChangeWaiting {
		t.Fatalf("%v %v", ch, err)
	}
	if _, err := e.Svc.Drain(context.Background(), "owner", id, ""); !errors.Is(err, machines.ErrConflict) {
		t.Fatalf("second drain: %v", err)
	}
}

// CRSP235 (machines R5.81): Restart the service of an agent machine names the
// running jobs, never cuts one, asks the agent only when idle, and ends when the
// agent connects again. The change is audited with its actor.
func TestRestartServiceWaitsForJobsAsksTheAgentAndEndsOnReconnect(t *testing.T) {
	e, id, key := setup(t)
	fa, err := e.session(id, key, &agentv1.Hello{Holders: &agentv1.HolderReport{Running: []*agentv1.JobRef{oneJob}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err = e.Svc.RestartService(ctx, "owner", id, false)
	var nc *machines.NeedsConfirmationError
	if !errors.As(err, &nc) || !strings.Contains(strings.Join(nc.Names, "\n"), "o/r") {
		t.Fatalf("err = %v, want a question that names the running job", err)
	}
	ch, err := e.Svc.RestartService(ctx, "owner", id, true)
	if err != nil || ch.Kind != machines.KindServiceRestart {
		t.Fatalf("change = %+v, %v", ch, err)
	}
	fa.Next(isDrain)
	// A job still runs: the agent is not asked.
	fa.Send(drainStatus(ch.ID, false, oneJob))
	// Idle: the agent is asked to restart; the change stays running and the machine stays held.
	fa.Send(drainStatus(ch.ID, true))
	if r := fa.Next(func(m *agentv1.SessionResponse) bool { return m.GetRestartAgent() != nil }).GetRestartAgent(); r.GetCommandId() != ch.ID {
		t.Fatalf("restart command = %+v", r)
	}
	waitFor(t, "running, not done", func() bool {
		c, _ := e.Svc.GetChange(ctx, id, ch.ID)
		return c.State == machines.ChangeRunning
	})
	if v, _ := e.Svc.Get(ctx, id); v.Machine.State == machines.StateActive || v.PendingChange == nil {
		t.Fatalf("the machine must keep no new jobs while the service restarts: %v", v.Machine.State)
	}
	// The agent connects again: the change is done and the machine takes jobs.
	if _, err := e.session(id, key, &agentv1.Hello{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "done and active", func() bool {
		c, _ := e.Svc.GetChange(ctx, id, ch.ID)
		v, _ := e.Svc.Get(ctx, id)
		return c.State == machines.ChangeDone && v.Machine.State == machines.StateActive && v.PendingChange == nil
	})
}

func TestRestartServiceRefusesTheControllerAndAnOfflineMachine(t *testing.T) {
	e, id, fa := running(t)
	ctx := context.Background()
	if _, err := e.Svc.RestartService(ctx, "owner", "m_unknown", true); !errors.Is(err, machines.ErrNotFound) {
		t.Fatalf("unknown machine: %v", err)
	}
	m, _ := e.Repo.GetMachine(ctx, id)
	m.IsController = true
	_ = e.Repo.UpdateMachine(ctx, m)
	if _, err := e.Svc.RestartService(ctx, "owner", id, true); !errors.Is(err, machines.ErrConflict) {
		t.Fatalf("controller: %v", err)
	}

	m.IsController = false
	_ = e.Repo.UpdateMachine(ctx, m)
	fa.Close() // the agent leaves: nothing to ask
	waitFor(t, "offline", func() bool { return !e.Svc.Connected(id) })
	if _, err := e.Svc.RestartService(ctx, "owner", id, true); !errors.Is(err, machines.ErrOffline) {
		t.Fatalf("offline: %v", err)
	}
}
