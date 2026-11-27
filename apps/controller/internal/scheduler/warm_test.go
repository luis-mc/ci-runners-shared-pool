package scheduler_test

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

var warmNow = time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)

func warmSet(slotsN, warm int) slots.Settings {
	s := slots.Defaults()
	s.JobSlots, s.WarmRunners = slotsN, warm
	return s
}

func heldCI(names ...string) []slots.Slot {
	var out []slots.Slot
	for i, n := range names {
		out = append(out, slots.Slot{Kind: slots.KindCI, Number: i + 1, Job: slots.Job{Repository: "o/r", Job: n}})
	}
	return out
}

func warmAges(mins ...int) []scheduler.WarmRunner {
	var out []scheduler.WarmRunner
	for i, m := range mins {
		out = append(out, scheduler.WarmRunner{Name: fmt.Sprintf("w%d", i+1), StartedAt: warmNow.Add(-time.Duration(m) * time.Minute)})
	}
	return out
}

// R5.66: the count is kept, and a warm runner takes budget like a job.
func TestWarmCountsAgainstSlotsAndBudget(t *testing.T) {
	// Default 0: nothing changes until it is set.
	p := scheduler.PlanWarm(scheduler.WarmInput{Settings: warmSet(4, 0), Load: quiet, Now: warmNow})
	if p.Start != 0 || len(p.Release) != 0 {
		t.Fatalf("off: %+v", p)
	}
	// Wants 2, none yet, room for all: start 2.
	p = scheduler.PlanWarm(scheduler.WarmInput{Settings: warmSet(4, 2), Load: quiet, Now: warmNow})
	if p.Start != 2 {
		t.Fatalf("want 2 started: %+v", p)
	}
	// One exists: start one more, count kept at 2.
	p = scheduler.PlanWarm(scheduler.WarmInput{Settings: warmSet(4, 2), Warm: warmAges(1), Load: quiet, Now: warmNow})
	if p.Start != 1 {
		t.Fatalf("%+v", p)
	}
	// A job holds 2 of 3 units (e2e): only one warm runner fits beside it.
	p = scheduler.PlanWarm(scheduler.WarmInput{Settings: warmSet(3, 2), Held: heldCI("e2e"), Load: quiet, Now: warmNow})
	if p.Start != 1 {
		t.Fatalf("warm must count against the budget: %+v", p)
	}
	// Too many warm runners for the setting: the oldest go.
	p = scheduler.PlanWarm(scheduler.WarmInput{Settings: warmSet(4, 1), Warm: warmAges(5, 9), Load: quiet, Now: warmNow})
	if !reflect.DeepEqual(p.Release, []string{"w2"}) || p.Start != 0 {
		t.Fatalf("%+v", p)
	}
}

// R5.66: never start over the machine's limits, and the hold-until-measured rule stays.
func TestWarmNeverOverLimits(t *testing.T) {
	over := scheduler.Load{MemoryPct: 90, MemoryKnown: true, CPUPct: 10, CPUKnown: true}
	for name, load := range map[string]scheduler.Load{"over budget": over, "not measured": {}} {
		p := scheduler.PlanWarm(scheduler.WarmInput{Settings: warmSet(4, 2), Load: load, Now: warmNow})
		if p.Start != 0 || p.Why == "" {
			t.Fatalf("%s: %+v", name, p)
		}
	}
	// No room: the budget is spent by real jobs.
	p := scheduler.PlanWarm(scheduler.WarmInput{Settings: warmSet(2, 2), Held: heldCI("build", "build"), Load: quiet, Now: warmNow})
	if p.Start != 0 {
		t.Fatalf("%+v", p)
	}
	// A hold releases every warm runner and starts none.
	for _, hold := range []string{scheduler.WarmHoldPaused, scheduler.WarmHoldBattery, scheduler.WarmHoldDiskFloor} {
		p := scheduler.PlanWarm(scheduler.WarmInput{Settings: warmSet(4, 2), Warm: warmAges(1, 2), Load: quiet, Hold: hold, Now: warmNow})
		if p.Start != 0 || len(p.Release) != 2 || !strings.HasPrefix(p.Why, "no warm runner") {
			t.Fatalf("%s: %+v", hold, p)
		}
	}
	// Real jobs took the slots after the warm runners started: the oldest go.
	p = scheduler.PlanWarm(scheduler.WarmInput{Settings: warmSet(3, 2), Held: heldCI("e2e", "build"), Warm: warmAges(7, 3), Load: quiet, Now: warmNow})
	if !reflect.DeepEqual(p.Release, []string{"w1", "w2"}) {
		t.Fatalf("%+v", p)
	}
}

// R5.66: older than 15 minutes means replaced.
func TestWarmReplacedWhenStale(t *testing.T) {
	p := scheduler.PlanWarm(scheduler.WarmInput{Settings: warmSet(4, 2), Warm: warmAges(16, 2), Load: quiet, Now: warmNow})
	if !reflect.DeepEqual(p.Release, []string{"w1"}) || !reflect.DeepEqual(p.Replace, []string{"w1"}) || p.Start != 1 {
		t.Fatalf("%+v", p)
	}
	p = scheduler.PlanWarm(scheduler.WarmInput{Settings: warmSet(4, 1), Warm: warmAges(14), Load: quiet, Now: warmNow})
	if len(p.Release) != 0 || p.Start != 0 {
		t.Fatalf("a 14 minute runner stays: %+v", p)
	}
}

type warmLog struct {
	mu       sync.Mutex
	warm     []scheduler.WarmRunner
	released []string
}

func (w *warmLog) list(string) []scheduler.WarmRunner {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]scheduler.WarmRunner(nil), w.warm...)
}

func (w *warmLog) release(_ string, names []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.released = append(w.released, names...)
}

// R5.66: a real job is never held by a warm runner; the oldest warm runner is released.
func TestWarmReleasedForARealJob(t *testing.T) {
	e := newEnv(t)
	wl := &warmLog{warm: warmAges(10, 4)} // w1 older than w2
	e.eng = scheduler.NewEngine(e.st, scheduler.WithClock(func() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now }),
		scheduler.WithWarm(wl.list, wl.release))
	e.settings(m1, func(s *slots.Settings) { s.JobSlots = 3; s.WarmRunners = 2 })

	// Budget 3: build (1) + two warm (2) fills it; the job fits only if one warm yields.
	goes(t, e.gate(e.req("g1", job("o/r", 1, "build"), 1, quiet)))
	if len(wl.released) != 0 {
		t.Fatalf("1 + 1 + 2 warm fits 3: nothing to release, got %v", wl.released)
	}
	// e2e weighs 2: held 1 + 2 = 3, with two warm that is 5 of 3, so both go.
	goes(t, e.gate(e.req("g2", scheduler.JobRef{Repository: "o/r", RunID: 2, RunAttempt: 1, Job: "e2e", RunnerName: "r2"}, 1, quiet)))
	if !reflect.DeepEqual(wl.released, []string{"w1", "w2"}) {
		t.Fatalf("released %v", wl.released)
	}
}

// The warm runner a job runs on is that job's own: it is not drained for it.
func TestWarmRunnerOfTheJobIsNotDrained(t *testing.T) {
	e := newEnv(t)
	wl := &warmLog{warm: warmAges(10)}
	e.eng = scheduler.NewEngine(e.st, scheduler.WithClock(func() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now }),
		scheduler.WithWarm(wl.list, wl.release))
	e.settings(m1, func(s *slots.Settings) { s.JobSlots = 2; s.WarmRunners = 1 })
	goes(t, e.gate(e.req("g1", job("o/r", 1, "build"), 1, quiet)))
	// build holds 1 of 2; the warm w1 holds the other. A job on runner w1 takes it.
	j := scheduler.JobRef{Repository: "o/r", RunID: 2, RunAttempt: 1, Job: "unit", RunnerName: "w1"}
	goes(t, e.gate(e.req("g2", j, 1, quiet)))
	if len(wl.released) != 0 {
		t.Fatalf("released %v", wl.released)
	}
}

func TestWarmYieldNamesOnlyWhatIsNeeded(t *testing.T) {
	got := scheduler.WarmToYield(warmSet(3, 2), heldCI("build"), warmAges(9, 3), slots.Weight{CPU: 1, Memory: 1})
	if !reflect.DeepEqual(got, []string{"w1"}) { // held 1 + job 1 + two warm = 4 of 3: one yields, the oldest
		t.Fatalf("got %v", got)
	}
}

func TestWarmRunnersSettingNormalises(t *testing.T) {
	if got := slots.Normalise(nil).WarmRunners; got != 0 {
		t.Fatalf("default %d", got)
	}
	if got := slots.Normalise(map[string]any{"warm_runners": 2}).WarmRunners; got != 2 {
		t.Fatalf("%d", got)
	}
	if got := slots.Normalise(map[string]any{"warm_runners": -1}).WarmRunners; got != 0 {
		t.Fatalf("a bad value keeps the default: %d", got)
	}
	if err := slots.CheckKeys(map[string]any{"warm_runners": 1}); err != nil {
		t.Fatal(err)
	}
}
