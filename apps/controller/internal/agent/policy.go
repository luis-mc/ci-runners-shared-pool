package agent

import (
	"context"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cpucap"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/load"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/power"
)

// powerTTL is how long a power reading is reused. A pmset call costs a process
// start, and a laptop's power source does not flip faster than this.
const powerTTL = 5 * time.Second

// SetPower sets where the gate reads the power source; nil reads the OS.
func (g *Gate) SetPower(r power.Reader) {
	g.pwMu.Lock()
	g.power, g.pwAt = r, time.Time{}
	g.pwMu.Unlock()
}

// SetClock sets the gate's clock (tests); nil is the real one.
func (g *Gate) SetClock(now func() time.Time) {
	g.mu.Lock()
	g.now = now
	g.mu.Unlock()
	g.pwMu.Lock()
	g.pwAt = time.Time{}
	g.pwMu.Unlock()
}

func (g *Gate) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

// currentPower reads the power source, reusing a reading newer than powerTTL.
// It never holds the gate's lock: a reader may start a process.
func (g *Gate) currentPower() power.State {
	g.pwMu.Lock()
	defer g.pwMu.Unlock()
	now := g.clockUnlocked()
	if !g.pwAt.IsZero() && now.Sub(g.pwAt) < powerTTL {
		return g.pwNow
	}
	r := g.power
	if r == nil {
		r = power.OS()
	}
	g.pwNow, g.pwAt = r.Read(context.Background()), now
	return g.pwNow
}

// clockUnlocked reads the clock without taking g.mu (pwMu is held).
func (g *Gate) clockUnlocked() time.Time {
	// now is set before the gate is shared; a later SetClock only happens in tests.
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

// Acceptance is whether the machine takes new jobs now and, when it does not,
// why (R5.62). Reason is one of machines.PauseReason*; it is empty while
// accepting.
type Acceptance struct {
	Accepting bool
	Reason    string
	Power     power.State
	// PauseUntil is the end of a timed pause from the tray, when one runs.
	PauseUntil time.Time
}

// Acceptance returns the current answer. A full slot table is not a reason:
// the machine still accepts, the job waits for a slot.
func (g *Gate) Acceptance() Acceptance {
	ps := g.currentPower()
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.acceptanceLocked(ps, g.clock())
}

func (g *Gate) acceptanceLocked(ps power.State, now time.Time) Acceptance {
	a := Acceptance{Power: ps}
	if !g.have {
		a.Reason = machines.PauseReasonPaused
		return a
	}
	if !g.settings.PausedUntilResumed && g.settings.PauseUntil.After(now) {
		a.PauseUntil = g.settings.PauseUntil
	}
	switch {
	case g.state != machines.StateActive, g.settings.UserPaused(now):
		a.Reason = machines.PauseReasonPaused
	case g.settings.EffectiveMode() == machines.ModeAutomatic && g.settings.PauseOnBattery && ps.OnBattery:
		a.Reason = machines.PauseReasonOnBattery
	case len(g.baseGaps) > 0:
		a.Reason = machines.PauseReasonBaseImage
	case g.floor != nil && g.diskLowLocked():
		a.Reason = machines.PauseReasonDiskLow
	default:
		rep, ok := g.sampler.Latest()
		if good, _ := load.Decide(rep, ok, g.settings.BudgetMemoryPct, g.settings.BudgetCPUPct); !good {
			a.Reason = machines.PauseReasonBudget
		} else {
			a.Accepting = true
		}
	}
	return a
}

// diskLowLocked reads the disk floor; the caller holds g.mu.
func (g *Gate) diskLowLocked() bool {
	held, _ := g.floor.Check()
	return held
}

// capState is what the agent last did about the CPU cap.
type capState struct {
	pct     int
	at      time.Time
	res     cpucap.Result
	everSet bool
}

// capEvery is how often the cap is set again, so a runner service that started
// since is capped within a minute.
const capEvery = time.Minute

// syncCap sets the CPU cap when the setting changed or the last set is old. At
// 100 with no cap ever set there is nothing to lift, so the OS is not touched.
func (l *link) syncCap(ctx context.Context, set machines.Settings) cpucap.Result {
	pct := set.EffectiveCPUCap()
	l.capMu.Lock()
	defer l.capMu.Unlock()
	now := time.Now()
	if l.cap.pct == pct && now.Sub(l.cap.at) < capEvery {
		return l.cap.res
	}
	var res cpucap.Result
	switch {
	case pct >= 100 && !l.cap.everSet:
		res = cpucap.Result{State: cpucap.Applied}
		if l.cfg.sys().GOOS() == "darwin" {
			res = cpucap.Result{State: cpucap.NotSupported, Detail: "CPU cap is not supported on macOS yet"}
		}
	default:
		ap := l.cfg.CPUCap
		if ap == nil {
			ap = cpucap.New()
		}
		res = ap.Apply(ctx, pct)
		if res.State == cpucap.Applied {
			l.cap.everSet = true
		}
	}
	l.cap.pct, l.cap.at, l.cap.res = pct, now, res
	if res.State == cpucap.Failed {
		l.cfg.Logger.Warn("the CPU cap could not be set", "pct", pct, "detail", res.Detail)
	}
	return res
}

// acceptingReport builds the message for the controller.
func (l *link) acceptingReport(ctx context.Context) *agentv1.AcceptingReport {
	set, _ := l.cfg.Gate.Settings()
	l.checkBaseImages(ctx)
	acc := l.cfg.Gate.Acceptance()
	res := l.syncCap(ctx, set)
	l.capMu.Lock()
	applied := l.cap.pct // the percent the state is about, set after the apply
	l.capMu.Unlock()
	floorGiB, floorOff := l.cfg.Gate.DiskFloor()
	r := &agentv1.AcceptingReport{
		DiskFloorGib: int32(floorGiB), DiskFloorOff: floorOff, //nolint:gosec // at most 2000
		Accepting: acc.Accepting, PauseReason: acc.Reason, OnBattery: acc.Power.OnBattery, BatteryKnown: acc.Power.Known,
		CpuCapPct: int32(applied), CpuCapState: res.State, CpuCapDetail: res.Detail, //nolint:gosec // 25 to 100
	}
	if !acc.PauseUntil.IsZero() {
		r.PauseUntilUnixMs = acc.PauseUntil.UnixMilli()
	}
	return r
}

// kickPolicy asks the policy loop to report now (settings changed).
func (l *link) kickPolicy() {
	select {
	case l.kick <- struct{}{}:
	default:
	}
}

// policyLoop reports the acceptance when the session opens, each time it
// changes, and every LoadEvery otherwise, so the controller always holds a
// report newer than the link's silence limit (R5.62).
func (l *link) policyLoop(ctx context.Context) {
	t := time.NewTicker(l.cfg.PolicyEvery)
	defer t.Stop()
	var last *agentv1.AcceptingReport
	var sentAt time.Time
	for {
		r := l.acceptingReport(ctx)
		if last == nil || !sameReport(last, r) || time.Since(sentAt) >= l.cfg.LoadEvery {
			if err := l.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_AcceptingReport{AcceptingReport: r}}); err != nil {
				return
			}
			last, sentAt = r, time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-l.kick:
		}
	}
}

func sameReport(a, b *agentv1.AcceptingReport) bool {
	return a.GetAccepting() == b.GetAccepting() && a.GetPauseReason() == b.GetPauseReason() &&
		a.GetOnBattery() == b.GetOnBattery() && a.GetBatteryKnown() == b.GetBatteryKnown() &&
		a.GetCpuCapPct() == b.GetCpuCapPct() && a.GetCpuCapState() == b.GetCpuCapState() &&
		a.GetCpuCapDetail() == b.GetCpuCapDetail() && a.GetPauseUntilUnixMs() == b.GetPauseUntilUnixMs() &&
		a.GetDiskFloorGib() == b.GetDiskFloorGib() && a.GetDiskFloorOff() == b.GetDiskFloorOff()
}
