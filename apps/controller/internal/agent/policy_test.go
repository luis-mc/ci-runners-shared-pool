package agent_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cpucap"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/power"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
)

var battery = power.State{OnBattery: true, Known: true}

func policyGate(t *testing.T, pw power.State, mutate func(*machines.Settings)) (*agent.Gate, *time.Time) {
	t.Helper()
	g, _, _ := gateWith(t, "700000", mutate) // 30% memory used, under budget
	g.SetPower(power.Static(pw))
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	g.SetClock(func() time.Time { return now })
	return g, &now
}

func accept(g *agent.Gate) (bool, string) { a := g.Acceptance(); return a.Accepting, a.Reason }

// R5.61: Automatic is the default and accepts on mains.
func TestAutomaticAcceptsOnMains(t *testing.T) {
	g, _ := policyGate(t, power.Mains, nil)
	if ok, why := accept(g); !ok || why != "" {
		t.Fatalf("accepting = %v %q", ok, why)
	}
	if d := g.Start("a", job, "persistent"); !d.Go {
		t.Fatalf("held on mains: %v", d.Reasons)
	}
}

// R5.61, R5.63: Automatic pauses on battery when the battery pause is on, and holds new jobs with that reason; a job that runs keeps going.
func TestAutomaticPausesOnBatteryAndNeverCutsARunningJob(t *testing.T) {
	g, _ := policyGate(t, power.Mains, nil)
	if d := g.Start("running", job, "persistent"); !d.Go {
		t.Fatal(d.Reasons)
	}
	g.SetPower(power.Static(battery)) // the charger is pulled
	if ok, why := accept(g); ok || why != machines.PauseReasonOnBattery {
		t.Fatalf("on battery: accepting %v reason %q", ok, why)
	}
	d := g.Start("new", job, "persistent")
	if d.Go || d.Reasons[0] != "on_battery" {
		t.Fatalf("new job on battery: %+v", d)
	}
	if g.SlotsUsed() != 1 {
		t.Fatalf("the running job was touched: %d slots used", g.SlotsUsed())
	}
}

// R5.61: with the battery pause off, Automatic keeps accepting on battery.
func TestBatteryPauseSwitchOff(t *testing.T) {
	g, _ := policyGate(t, battery, func(s *machines.Settings) { s.PauseOnBattery = false })
	if ok, why := accept(g); !ok {
		t.Fatalf("accepting = %v %q, want accepting with the switch off", ok, why)
	}
}

// R5.61: Always ignores the battery.
func TestAlwaysIgnoresBattery(t *testing.T) {
	g, _ := policyGate(t, battery, func(s *machines.Settings) { s.Mode = machines.ModeAlways })
	if ok, why := accept(g); !ok {
		t.Fatalf("Always on battery: %v %q", ok, why)
	}
	if d := g.Start("a", job, "persistent"); !d.Go {
		t.Fatalf("held: %v", d.Reasons)
	}
}

// R5.61: Paused takes no new job and finishes the running ones.
func TestPausedTakesNoNewJobAndFinishesRunning(t *testing.T) {
	g, _ := policyGate(t, power.Mains, nil)
	if d := g.Start("running", job, "persistent"); !d.Go {
		t.Fatal(d.Reasons)
	}
	set, _ := g.Settings()
	set.Mode = machines.ModePaused
	g.SetSettings(set, machines.StateActive)
	if ok, why := accept(g); ok || why != machines.PauseReasonPaused {
		t.Fatalf("paused: %v %q", ok, why)
	}
	if d := g.Start("new", job, "persistent"); d.Go || d.Reasons[0] != "paused" {
		t.Fatalf("new job while paused: %+v", d)
	}
	if got := g.Running(); len(got) != 1 {
		t.Fatalf("the running job was cut: %v", got)
	}
	g.Finish("running")
	// Back to Automatic: the machine takes jobs again.
	set.Mode = machines.ModeAutomatic
	g.SetSettings(set, machines.StateActive)
	if d := g.Start("later", job, "persistent"); !d.Go {
		t.Fatalf("after the pause: %v", d.Reasons)
	}
}

// R5.61: a pause from the tray with a timer ends by itself; one until Resume does not.
func TestTimedPauseEndsAndUntilResumedDoesNot(t *testing.T) {
	g, now := policyGate(t, power.Mains, func(s *machines.Settings) { s.PauseUntil = time.Date(2026, 11, 15, 13, 0, 0, 0, time.UTC) })
	a := g.Acceptance()
	if a.Accepting || a.Reason != "paused" || a.PauseUntil.IsZero() {
		t.Fatalf("inside the hour: %+v", a)
	}
	*now = now.Add(61 * time.Minute)
	if ok, why := accept(g); !ok {
		t.Fatalf("after the hour: %v %q", ok, why)
	}
	set, _ := g.Settings()
	set.PauseUntil, set.PausedUntilResumed = time.Time{}, true
	g.SetSettings(set, machines.StateActive)
	*now = now.Add(48 * time.Hour)
	if ok, why := accept(g); ok || why != "paused" {
		t.Fatalf("until resumed after two days: %v %q", ok, why)
	}
}

// R5.61 (decision CRSP75-1): a pause from the tray holds in Always too.
func TestTrayPauseHoldsInAlways(t *testing.T) {
	g, _ := policyGate(t, power.Mains, func(s *machines.Settings) {
		s.Mode, s.PausedUntilResumed = machines.ModeAlways, true
	})
	if ok, why := accept(g); ok || why != "paused" {
		t.Fatalf("%v %q", ok, why)
	}
}

// R5.62: a machine over its budget reports the budget reason, which is not a pause.
func TestOverBudgetReportsBudget(t *testing.T) {
	g, _, _ := gateWith(t, "100000", nil) // 90% memory used
	g.SetPower(power.Static{})
	if ok, why := accept(g); ok || why != machines.PauseReasonBudget {
		t.Fatalf("%v %q", ok, why)
	}
}

// R5.62: a drained machine takes no new job; it says paused (the reuse of the drain rules).
func TestDrainedMachineReportsPaused(t *testing.T) {
	g, _ := policyGate(t, power.Mains, nil)
	g.Hold()
	if ok, why := accept(g); ok || why != "paused" {
		t.Fatalf("%v %q", ok, why)
	}
}

type recordedCap struct {
	mu   sync.Mutex
	seen []int
	res  cpucap.Result
}

func (r *recordedCap) Apply(_ context.Context, pct int) cpucap.Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, pct)
	return r.res
}
func (r *recordedCap) last() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.seen) == 0 {
		return -1
	}
	return r.seen[len(r.seen)-1]
}

// R5.62, R5.64 end to end over the real link: the agent reports accepting, the
// controller gate follows a console change to Paused, the battery turns the
// machine off and back on, and the CPU cap reaches the applier.
func TestAcceptingReportAndCPUCapOverTheLink(t *testing.T) {
	c := startController(t)
	ctx := context.Background()
	code, _ := c.Svc.CreateJoinCode(ctx, "owner", 0)
	cfg := fastConfig(t, c, code.Code)
	cfg.Gate = agent.NewGate(cfg.Sampler)
	var mu sync.Mutex
	pw := power.Mains
	cfg.Gate.SetPower(power.ReaderFunc(func(context.Context) power.State { mu.Lock(); defer mu.Unlock(); return pw }))
	cfg.PolicyEvery = 20 * time.Millisecond
	rc := &recordedCap{res: cpucap.Result{State: cpucap.Applied, Targets: 1}}
	cfg.CPUCap = rc
	runAgent(t, cfg)
	var id string
	waitFor(t, "online and accepting", func() bool {
		vs, _ := c.Svc.List(ctx)
		if len(vs) == 1 && vs[0].Machine.Online && !vs[0].Policy.ReportedAt.IsZero() {
			id = vs[0].Machine.ID
		}
		_, sampled := cfg.Sampler.Latest()
		return id != "" && sampled && c.Svc.Policy(id).Accepting
	})
	online, accepting, reason, err := c.Svc.Accepting(ctx, id)
	if err != nil || !online || !accepting || reason != "" {
		t.Fatalf("status: %v %v %q %v", online, accepting, reason, err)
	}
	v, _ := c.Svc.Get(ctx, id)
	if !v.Machine.Accepting || v.Machine.PauseReason != "" {
		t.Fatalf("the machine record says %v %q", v.Machine.Accepting, v.Machine.PauseReason)
	}

	// The console sets Paused: the agent follows within moments, and says why.
	paused := machines.ModePaused
	if _, _, err := c.Svc.UpdateSettings(ctx, "owner", id, machines.SettingsUpdate{Mode: &paused}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "paused reported", func() bool { p := c.Svc.Policy(id); return !p.Accepting && p.PauseReason == "paused" })
	if d := cfg.Gate.Start("x", job, "persistent"); d.Go {
		t.Fatal("a job started while paused")
	}
	auto := machines.ModeAutomatic
	if _, _, err := c.Svc.UpdateSettings(ctx, "owner", id, machines.SettingsUpdate{Mode: &auto}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "accepting again", func() bool { return c.Svc.Policy(id).Accepting })

	// Unplug: on battery, with the reason and the battery fact.
	mu.Lock()
	pw = battery
	mu.Unlock()
	waitFor(t, "on battery reported", func() bool {
		p := c.Svc.Policy(id)
		return !p.Accepting && p.PauseReason == machines.PauseReasonOnBattery && p.OnBattery && p.BatteryKnown
	})
	_, accepting, reason, _ = c.Svc.Accepting(ctx, id)
	if accepting || reason != "on_battery" {
		t.Fatalf("status on battery: %v %q", accepting, reason)
	}
	// The switch off puts the machine back to work on battery.
	off := false
	if _, _, err := c.Svc.UpdateSettings(ctx, "owner", id, machines.SettingsUpdate{PauseOnBattery: &off}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "accepting on battery with the switch off", func() bool { return c.Svc.Policy(id).Accepting })

	// The CPU cap reaches the applier, and the report says it was applied.
	half := 50
	if _, _, err := c.Svc.UpdateSettings(ctx, "owner", id, machines.SettingsUpdate{CPUCapPct: &half}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "cap applied", func() bool {
		p := c.Svc.Policy(id)
		return rc.last() == 50 && p.CPUCapPct == 50 && p.CPUCapState == cpucap.Applied
	})
	// The report says which percent the result is about: it never carries a new
	// percent with the result of the old one.
	for _, pct := range []int{25, 100} {
		pct := pct
		if _, _, err := c.Svc.UpdateSettings(ctx, "owner", id, machines.SettingsUpdate{CPUCapPct: &pct}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "cap applied again", func() bool {
			p := c.Svc.Policy(id)
			return rc.last() == pct && p.CPUCapPct == pct && p.CPUCapState == cpucap.Applied
		})
	}
}

// CRSP83 (runners R5.83): under the free-disk floor the machine pauses with
// the reason disk_low and starts no job; a running job keeps going; it
// resumes 1 GiB above the floor.
func TestDiskFloorPausesNewJobs(t *testing.T) {
	g, _ := policyGate(t, power.Mains, nil)
	const gb = int64(1) << 30
	free := 50 * gb
	g.SetDiskFloor(&runnercache.Floor{Bytes: 10 * gb, Path: "x", Read: func(string) (runnercache.Disk, error) {
		return runnercache.Disk{Free: free, Total: 500 * gb}, nil
	}})
	if d := g.Start("running", job, "persistent"); !d.Go {
		t.Fatalf("held with room: %v", d.Reasons)
	}
	free = 4 * gb
	if ok, why := accept(g); ok || why != machines.PauseReasonDiskLow {
		t.Fatalf("under the floor: accepting %v reason %q", ok, why)
	}
	d := g.Start("new", job, "persistent")
	if d.Go || d.Reasons[0] != "disk_low" {
		t.Fatalf("a job started under the floor: %+v", d)
	}
	if g.SlotsUsed() != 1 {
		t.Fatalf("the running job was touched: %d", g.SlotsUsed())
	}
	free = 12 * gb
	if ok, _ := accept(g); !ok {
		t.Fatal("did not resume with 12 GiB free")
	}
	g.SetDiskFloor(nil)
	free = 1
	if ok, _ := accept(g); !ok {
		t.Fatal("a removed floor still holds")
	}
}
