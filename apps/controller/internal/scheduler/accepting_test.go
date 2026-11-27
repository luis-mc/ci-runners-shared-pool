package scheduler_test

import (
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
)

type acceptance struct {
	mu     sync.Mutex
	ok     bool
	reason string
}

func (a *acceptance) set(ok bool, reason string) {
	a.mu.Lock()
	a.ok, a.reason = ok, reason
	a.mu.Unlock()
}
func (a *acceptance) get(string) (bool, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ok, a.reason
}

func envWithAcceptance(t *testing.T) (*env, *acceptance) {
	t.Helper()
	e := newEnv(t)
	acc := &acceptance{ok: true}
	e.eng = scheduler.NewEngine(e.st, scheduler.WithClock(func() time.Time {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.now
	}), scheduler.WithAccepting(acc.get))
	return e, acc
}

// Scheduling R5.68, machines R5.62: a machine that does not accept holds every
// new job with the reason the agent reported, even when a slot is free.
func TestGateHoldsNewJobsWithTheReasonWhileNotAccepting(t *testing.T) {
	for _, c := range []struct {
		reason string
		want   scheduler.WaitReason
		text   string
	}{
		{"paused", scheduler.WaitPaused, "this machine is paused"},
		{"on_battery", scheduler.WaitOnBattery, "this machine is on battery"},
	} {
		t.Run(c.reason, func(t *testing.T) {
			e, acc := envWithAcceptance(t)
			acc.set(false, c.reason)
			d := e.gate(e.req("g1", job("o/r", 1, "build"), 1, quiet))
			waits(t, d, c.want)
			if d.WaitText != c.text {
				t.Fatalf("text %q want %q", d.WaitText, c.text)
			}
			if s := e.status(m1); len(s.Slots) != 0 {
				t.Fatalf("a held job took a slot: %+v", s)
			}
			// The machine accepts again: the same job goes at once.
			acc.set(true, "")
			goes(t, e.gate(e.req("g1", job("o/r", 1, "build"), 1, quiet)))
		})
	}
}

// A light job is held too: the hold is the machine's, not a job class's.
func TestGateHoldsLightJobsWhilePaused(t *testing.T) {
	e, acc := envWithAcceptance(t)
	acc.set(false, "paused")
	j := job("o/r", 2, "smoke")
	waits(t, e.gate(e.req("g2", j, 1, quiet)), scheduler.WaitPaused)
}

// The budget reason is not held here: the gate checks the live load itself, so a
// report that is 15 seconds old cannot hold a job that fits now.
func TestGateDoesNotHoldOnTheBudgetReason(t *testing.T) {
	e, acc := envWithAcceptance(t)
	acc.set(false, "budget")
	goes(t, e.gate(e.req("g3", job("o/r", 3, "build"), 1, quiet)))
}

// A job that already holds its slot is not cut by a pause: a retried request answers go again.
func TestPauseNeverCutsAJobThatHoldsASlot(t *testing.T) {
	e, acc := envWithAcceptance(t)
	goes(t, e.gate(e.req("g4", job("o/r", 4, "build"), 1, quiet)))
	acc.set(false, "paused")
	goes(t, e.gate(e.req("g4", job("o/r", 4, "build"), 1, quiet)))
	waits(t, e.gate(e.req("g5", job("o/r", 5, "build"), 1, quiet)), scheduler.WaitPaused)
}
