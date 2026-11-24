package scheduler_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler"
	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler/slots"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

// env is a real SQLite store, an engine and a clock the test moves by hand.
type env struct {
	t   *testing.T
	st  *store.Store
	eng *scheduler.Engine
	mu  sync.Mutex
	now time.Time
}

const m1 = "m1"

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	st, err := store.OpenForTests(ctx, filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e := &env{t: t, st: st, now: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	for _, m := range []string{m1, "m2"} {
		if err := st.SeedMachine(ctx, m, "linux"); err != nil {
			t.Fatal(err)
		}
	}
	e.eng = scheduler.NewEngine(st, scheduler.WithClock(func() time.Time {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.now
	}))
	return e
}

func (e *env) advance(d time.Duration) {
	e.mu.Lock()
	e.now = e.now.Add(d)
	e.mu.Unlock()
}

func (e *env) settings(machine string, mod func(*slots.Settings)) {
	e.t.Helper()
	s := slots.Defaults()
	mod(&s)
	if err := e.st.SetMachineSettingsForTests(context.Background(), machine, s); err != nil {
		e.t.Fatal(err)
	}
}

func job(repo string, run int64, name string) scheduler.JobRef {
	return scheduler.JobRef{Repository: repo, RunID: run, RunAttempt: 1, Job: name, RunnerName: "r1"}
}

var quiet = scheduler.Load{MemoryPct: 40, MemoryKnown: true, CPUPct: 10, CPUKnown: true}

func (e *env) req(gate string, j scheduler.JobRef, class int, load scheduler.Load) scheduler.GateRequest {
	return scheduler.GateRequest{GateID: gate, MachineID: m1, Job: j, EventClass: class, Load: load,
		Holder: scheduler.Holder{Machine: m1, HolderPID: 100, GatePID: 101}}
}

func (e *env) gate(r scheduler.GateRequest) scheduler.Decision {
	e.t.Helper()
	d, err := e.eng.Gate(context.Background(), r)
	if err != nil {
		e.t.Fatalf("gate %s: %v", r.GateID, err)
	}
	e.advance(time.Millisecond) // no two tickets share a nanosecond, so the order is the test's
	return d
}

func (e *env) status(machine string) scheduler.Status {
	e.t.Helper()
	s, err := e.eng.Status(context.Background(), machine)
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func goes(t *testing.T, d scheduler.Decision) scheduler.Grant {
	t.Helper()
	if len(d.Go) == 0 {
		t.Fatalf("want go, got wait %v %q", d.Wait, d.WaitText)
	}
	return d.Go[len(d.Go)-1]
}

func waits(t *testing.T, d scheduler.Decision, reason scheduler.WaitReason) {
	t.Helper()
	if len(d.Go) != 0 {
		t.Fatalf("want wait %s, got go %v", reason, d.Go)
	}
	if len(d.Wait) == 0 || d.Wait[0] != reason {
		t.Fatalf("want wait %s, got %v %q", reason, d.Wait, d.WaitText)
	}
}

func (e *env) fill(n int) []scheduler.Grant {
	e.t.Helper()
	var gs []scheduler.Grant
	for i := 0; i < n; i++ {
		gs = append(gs, goes(e.t, e.gate(e.req(fmt.Sprintf("fill-%d", i), job("o/r", int64(900+i), "build"), 1, quiet))))
	}
	return gs
}

// advanceRenewing moves the clock by d in steps of 60 s, renewing the lease of
// the jobs each step, as the agent's holder report does (R5.70).
func (e *env) advanceRenewing(d time.Duration, jobs ...scheduler.JobRef) {
	e.t.Helper()
	for d > 0 {
		step := 60 * time.Second
		if d < step {
			step = d
		}
		e.advance(step)
		d -= step
		if err := e.eng.RenewJobs(context.Background(), m1, jobs); err != nil {
			e.t.Fatal(err)
		}
	}
}
