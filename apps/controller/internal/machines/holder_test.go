package machines_test

import (
	"context"
	"sync"
	"testing"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// CRSP38: a stuck job blocks every drain, so the drain says which job holds it
// and for how long, flags a job far past its usual duration, and offers named
// choices; it never waits silently and never cuts a job (R5.55, R5.56).

func jobAt(started time.Time, name, runner string) *agentv1.JobRef {
	return &agentv1.JobRef{Repository: "o/r", RunId: 7, Job: name, RunnerName: runner, StartedUnixMs: started.UnixMilli()}
}

func TestDrainNamesTheHolderAndHowLongItRuns(t *testing.T) {
	e := newEnv(t)
	r, _ := e.joined("box-1")
	id := r.GetMachineId()
	started := e.Clk.Now().Add(-20 * time.Minute)
	long := jobAt(started, "build", "runner-1")
	short := jobAt(e.Clk.Now().Add(-2*time.Minute), "lint", "runner-2")
	fa, _ := e.session(id, r.GetMachineCredential(), &agentv1.Hello{Holders: &agentv1.HolderReport{Running: []*agentv1.JobRef{short, long}}})
	ch, _ := e.Svc.Drain(context.Background(), "owner", id, "")
	fa.Next(isDrain)
	fa.Send(drainStatus(ch.ID, false, short, long))
	waitFor(t, "holder", func() bool {
		c, _ := e.Svc.GetChange(context.Background(), id, ch.ID)
		return c.Detail.Holder != nil
	})
	c, _ := e.Svc.GetChange(context.Background(), id, ch.ID)
	h := c.Detail.Holder
	if h.Job.Job != "build" || !h.Job.StartedAt.Equal(started.Truncate(time.Millisecond)) || h.OverUsual || h.Usual != 0 {
		t.Fatalf("holder = %+v, want the job that started first, no history", h)
	}
	if got := machines.Choices(c); len(got) != 0 {
		t.Fatalf("choices before the timeout: %v", got)
	}
}

func TestHolderFarPastItsUsualDurationIsFlaggedOnceWithChoices(t *testing.T) {
	var stuck []string
	var mu sync.Mutex
	e := newEnv(t)
	e.Svc.SetTypical(func(_ context.Context, repo, job string) (time.Duration, bool) {
		if job == "build" {
			return 10 * time.Minute, true
		}
		return 0, false
	})
	e.Note.stuck = func(s string) { mu.Lock(); stuck = append(stuck, s); mu.Unlock() }
	r, _ := e.joined("box-1")
	id := r.GetMachineId()
	j := jobAt(e.Clk.Now(), "build", "runner-1")
	fa, _ := e.session(id, r.GetMachineCredential(), &agentv1.Hello{Holders: &agentv1.HolderReport{Running: []*agentv1.JobRef{j}}})
	ch, _ := e.Svc.Drain(context.Background(), "owner", id, "")
	fa.Next(isDrain)
	fa.Send(drainStatus(ch.ID, false, j))
	// The agent's report is handled on the session goroutine; the clock must
	// not move before it has landed, or the tick has no holder to flag.
	waitFor(t, "holder", func() bool {
		c, _ := e.Svc.GetChange(context.Background(), id, ch.ID)
		return c.Detail.Holder != nil
	})

	e.Clk.Add(15 * time.Minute) // 1.5 times the usual: not far past yet
	tick(t, e)
	c, _ := e.Svc.GetChange(context.Background(), id, ch.ID)
	if c.Detail.Holder == nil || c.Detail.Holder.OverUsual || len(stuck) != 0 || len(machines.Choices(c)) != 0 {
		t.Fatalf("flagged too early: %+v %v", c.Detail.Holder, stuck)
	}
	e.Clk.Add(10 * time.Minute) // 25 minutes: 2.5 times the usual
	tick(t, e)
	tick(t, e)
	c, _ = e.Svc.GetChange(context.Background(), id, ch.ID)
	if !c.Detail.Holder.OverUsual || c.Detail.Holder.Usual != 10*time.Minute {
		t.Fatalf("holder = %+v", c.Detail.Holder)
	}
	waitFor(t, "stuck alert", func() bool { mu.Lock(); defer mu.Unlock(); return len(stuck) >= 1 })
	tick(t, e) // a later tick must not raise it again
	mu.Lock()
	n := len(stuck)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("stuck alerts = %d, want one", n)
	}
	got := machines.Choices(c)
	if len(got) != 2 || got[0] != "wait_longer" || got[1] != "cancel" {
		t.Fatalf("choices = %v", got)
	}
	for _, ch := range got {
		if ch == "stop_jobs" {
			t.Fatal("a choice that stops a job")
		}
	}
	if c.State != machines.ChangeWaiting {
		t.Fatalf("state = %v: the drain keeps waiting", c.State)
	}
}

// R5.57: a drain for runner set X waits only for jobs on X.
func TestScopedDrainCountsOnlyItsOwnRunners(t *testing.T) {
	e := newEnv(t)
	r, _ := e.joined("box-1")
	id := r.GetMachineId()
	mine := jobAt(e.Clk.Now(), "build", "runner-1")
	other := jobAt(e.Clk.Now(), "test", "runner-2") // same repository, outside the set
	fa, _ := e.session(id, r.GetMachineCredential(), &agentv1.Hello{Holders: &agentv1.HolderReport{Running: []*agentv1.JobRef{mine, other}}})
	e.Svc.SetRunning(id, []machines.JobRef{{Repository: "o/r", RunID: 7, Job: "build", RunnerName: "runner-1"}, {Repository: "o/r", RunID: 7, Job: "test", RunnerName: "runner-2"}}, nil)
	ch, err := e.Svc.StartChangeFor(context.Background(), "owner", id, machines.KindRunnerUpdate, "new runner version", []string{"Runner-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.Detail.WaitingFor) != 1 || ch.Detail.WaitingFor[0].RunnerName != "runner-1" {
		t.Fatalf("waiting for %+v, want only runner-1", ch.Detail.WaitingFor)
	}
	d := fa.Next(isDrain).GetDrain()
	if len(d.GetRunnerNames()) != 1 || d.GetRunnerNames()[0] != "Runner-1" {
		t.Fatalf("the agent was not told the runner set: %v", d.GetRunnerNames())
	}
	if v, _ := e.Svc.Get(context.Background(), id); v.Machine.State != machines.StateActive {
		t.Fatalf("machine = %v: a scoped drain leaves the other runners working", v.Machine.State)
	}
	// An agent that reports the whole machine still gets only X counted; with
	// X idle the change runs even though runner-2 is busy.
	fa.Send(drainStatus(ch.ID, false, other))
	waitState(t, e, id, ch.ID, machines.ChangeDone)
}

func TestJobsOnKeepsTheWholeMachineWithoutNames(t *testing.T) {
	jobs := []machines.JobRef{{RunnerName: "a"}, {RunnerName: "b"}}
	if got := machines.JobsOn(jobs, nil); len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	if got := machines.JobsOn(jobs, []string{"B"}); len(got) != 1 || got[0].RunnerName != "b" {
		t.Fatalf("got %v", got)
	}
}
