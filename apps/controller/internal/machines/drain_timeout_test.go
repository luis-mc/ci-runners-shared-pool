package machines_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

func tick(t *testing.T, e *env) {
	t.Helper()
	if err := e.Svc.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// R5.33: one drain timeout per machine (default 60 minutes). When it passes
// with jobs still running, crsp keeps waiting, warns once and stops nothing.
func TestDrainTimeoutWarnsAndKeepsWaiting(t *testing.T) {
	e := newEnv(t)
	r, _ := e.joined("box-1")
	id := r.GetMachineId()
	fa, _ := e.session(id, r.GetMachineCredential(), &agentv1.Hello{Holders: &agentv1.HolderReport{Running: []*agentv1.JobRef{oneJob}}})
	ch, _ := e.Svc.Drain(context.Background(), "owner", id, "")
	fa.Next(isDrain)
	fa.Send(drainStatus(ch.ID, false, oneJob))

	e.Clk.Add(59 * time.Minute)
	tick(t, e)
	if c, _ := e.Svc.GetChange(context.Background(), id, ch.ID); c.State != machines.ChangeWaiting || !c.Detail.OverdueSince.IsZero() {
		t.Fatalf("before the timeout: %+v", c)
	}
	if len(e.Note.overdue) != 0 {
		t.Fatalf("warned too early: %v", e.Note.overdue)
	}

	e.Clk.Add(2 * time.Minute)
	tick(t, e)
	c, _ := e.Svc.GetChange(context.Background(), id, ch.ID)
	if c.State != machines.ChangeWaiting || c.Detail.OverdueSince.IsZero() {
		t.Fatalf("after the timeout: state %v overdue %v, want waiting and overdue", c.State, c.Detail.OverdueSince)
	}
	if len(e.Note.overdue) != 1 || !strings.Contains(e.Note.overdue[0], "1h1m0s 1") {
		t.Fatalf("alert = %v, want one 'still draining' warning with 1 job after 1h1m", e.Note.overdue)
	}
	if v, _ := e.Svc.Get(context.Background(), id); v.Machine.State != machines.StateDraining || len(v.RunningJobs) != 1 {
		t.Fatalf("machine = %v, jobs = %d", v.Machine.State, len(v.RunningJobs))
	}

	// It keeps waiting, and it warns only once.
	e.Clk.Add(5 * time.Hour)
	tick(t, e)
	if len(e.Note.overdue) != 1 {
		t.Fatalf("warned again: %v", e.Note.overdue)
	}
	if c, _ := e.Svc.GetChange(context.Background(), id, ch.ID); c.State != machines.ChangeWaiting {
		t.Fatalf("state = %v", c.State)
	}

	// The job ends late: the change still runs.
	fa.Send(drainStatus(ch.ID, true))
	waitState(t, e, id, ch.ID, machines.ChangeDone)
}

// The drain timeout is a setting of the machine (default 60 minutes).
func TestDrainTimeoutIsPerMachine(t *testing.T) {
	e := newEnv(t)
	r1, _ := e.joined("box-1")
	r2, _ := e.joined("box-2")
	id1, id2 := r1.GetMachineId(), r2.GetMachineId()
	if _, _, err := e.Svc.UpdateSettings(context.Background(), "owner", id1, machines.SettingsUpdate{DrainTimeoutMinutes: ptr(10)}); err != nil {
		t.Fatal(err)
	}
	c1, err := e.Svc.Drain(context.Background(), "owner", id1, "")
	if err != nil {
		t.Fatal(err)
	}
	c2, err := e.Svc.Drain(context.Background(), "owner", id2, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := c1.Deadline.Sub(c1.StartedAt); got != 10*time.Minute {
		t.Fatalf("box-1 deadline after %v, want 10m", got)
	}
	if got := c2.Deadline.Sub(c2.StartedAt); got != 60*time.Minute {
		t.Fatalf("box-2 deadline after %v, want the default 60m", got)
	}
	e.Clk.Add(11 * time.Minute)
	tick(t, e)
	if a, _ := e.Svc.GetChange(context.Background(), id1, c1.ID); a.Detail.OverdueSince.IsZero() {
		t.Fatal("box-1 is overdue after 11 minutes")
	}
	if b, _ := e.Svc.GetChange(context.Background(), id2, c2.ID); !b.Detail.OverdueSince.IsZero() {
		t.Fatal("box-2 is not overdue after 11 minutes")
	}
}

// R5.33: no decision stops a job. The old "stop_jobs" answer is refused, and
// wait longer and cancel still work on a waiting change.
func TestNoDecisionStopsJobs(t *testing.T) {
	e, id, fa := running(t)
	ch, _ := e.Svc.Drain(context.Background(), "owner", id, "")
	fa.Next(isDrain)
	e.Clk.Add(2 * time.Hour)
	tick(t, e)

	_, err := e.Svc.DecideChange(context.Background(), "owner", id, ch.ID, machines.Decision("stop_jobs"), 0, true)
	if !errors.Is(err, machines.ErrInvalid) || !errors.Is(err, machines.ErrNeverStopJobs) {
		t.Fatalf("stop_jobs: %v", err)
	}
	c, err := e.Svc.DecideChange(context.Background(), "owner", id, ch.ID, machines.WaitLonger, 30, false)
	if err != nil || c.State != machines.ChangeWaiting || !c.Detail.OverdueSince.IsZero() || !c.Deadline.After(e.Clk.Now().Add(29*time.Minute)) {
		t.Fatalf("wait longer: %+v %v", c, err)
	}
	fa.Next(isDrain) // the agent hears the new timeout
	if _, err := e.Svc.DecideChange(context.Background(), "owner", id, ch.ID, machines.WaitLonger, 2, false); !errors.Is(err, machines.ErrInvalid) {
		t.Fatalf("a wait under 5 minutes: %v", err)
	}
	c, err = e.Svc.DecideChange(context.Background(), "owner", id, ch.ID, machines.CancelIt, 0, false)
	if err != nil || c.State != machines.ChangeCancel {
		t.Fatalf("cancel: %+v %v", c, err)
	}
	fa.Next(isResume)
	if v, _ := e.Svc.Get(context.Background(), id); v.Machine.State != machines.StateActive {
		t.Fatalf("state = %v", v.Machine.State)
	}
}

func waitState(t *testing.T, e *env, id, chID, want string) {
	t.Helper()
	waitUntil(t, 5*time.Second, "change state "+want, func() bool {
		c, _ := e.Svc.GetChange(context.Background(), id, chID)
		return c.State == want
	})
}
