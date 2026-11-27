package machines_test

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// OPS29: one computed answer to "does the machine take new jobs", one case per reason.
func TestComputeAvailability(t *testing.T) {
	now := time.Date(2026, 11, 18, 15, 0, 0, 0, time.UTC)
	auto := machines.Settings{Mode: machines.ModeAutomatic, PauseOnBattery: true}
	always := machines.Settings{Mode: machines.ModeAlways, PauseOnBattery: true}
	paused := machines.Settings{Mode: machines.ModePaused}
	tray := machines.Settings{Mode: machines.ModeAlways, PauseUntil: now.Add(time.Hour)}
	trayEnded := machines.Settings{Mode: machines.ModeAlways, PauseUntil: now.Add(-time.Hour)}
	trayHeld := machines.Settings{Mode: machines.ModeAlways, PausedUntilResumed: true}
	battery := machines.Policy{BatteryKnown: true, OnBattery: true, Accepting: true, ReportedAt: now}
	rep := func(reason string) machines.Policy {
		return machines.Policy{Accepting: false, PauseReason: reason, ReportedAt: now}
	}
	cases := []struct {
		name   string
		online bool
		state  string
		set    machines.Settings
		p      machines.Policy
		taking bool
		reason string
	}{
		{"nothing reported takes jobs", true, machines.StateActive, auto, machines.Policy{Accepting: true}, true, ""},
		{"offline", false, machines.StateActive, auto, machines.Policy{}, false, machines.NotTakingOffline},
		{"stopped from the console: draining", true, machines.StateDraining, always, machines.Policy{}, false, machines.NotTakingStopped},
		{"stopped from the console: drained", true, machines.StateDrained, always, machines.Policy{}, false, machines.NotTakingStopped},
		{"mode Paused", true, machines.StateActive, paused, machines.Policy{}, false, machines.NotTakingModePaused},
		{"pause from the tray with a timer", true, machines.StateActive, tray, machines.Policy{}, false, machines.NotTakingTrayPaused},
		{"pause from the tray until resumed", true, machines.StateActive, trayHeld, machines.Policy{}, false, machines.NotTakingTrayPaused},
		{"a tray pause that ended takes jobs", true, machines.StateActive, trayEnded, machines.Policy{}, true, ""},
		{"on battery under Automatic", true, machines.StateActive, auto, battery, false, machines.NotTakingOnBattery},
		{"on battery under Always takes jobs", true, machines.StateActive, always, battery, true, ""},
		{"on battery, battery pause off, takes jobs", true, machines.StateActive, machines.Settings{Mode: machines.ModeAutomatic}, battery, true, ""},
		{"load over the budget", true, machines.StateActive, auto, rep(machines.PauseReasonBudget), false, machines.NotTakingBusy},
		{"base image missing", true, machines.StateActive, auto, rep(machines.PauseReasonBaseImage), false, machines.NotTakingBaseImage},
		{"disk low", true, machines.StateActive, auto, rep(machines.PauseReasonDiskLow), false, machines.NotTakingDiskLow},
		// The agent's own paused and on_battery lag behind a setting the controller just wrote (the bug of OPS29).
		{"a stale agent paused is not read", true, machines.StateActive, always, rep(machines.PauseReasonPaused), true, ""},
		{"a stale agent on_battery under Always is not read", true, machines.StateActive, always, rep(machines.PauseReasonOnBattery), true, ""},
		{"resumed while the agent still says paused", true, machines.StateActive, auto, rep(machines.PauseReasonPaused), true, ""},
	}
	for _, c := range cases {
		got := machines.ComputeAvailability(c.online, c.state, c.set, c.p, now)
		if got.Taking != c.taking || got.Reason != c.reason {
			t.Errorf("%s: got %+v, want taking=%v reason=%q", c.name, got, c.taking, c.reason)
		}
	}
}

// OPS29: the view carries the computed answer, and it follows Stop and Resume at once, whatever the agent reported.
func TestViewTakingFollowsStopAndResume(t *testing.T) {
	e, id, _ := setup(t)
	ctx := context.Background()
	v, _ := e.Svc.Get(ctx, id)
	if v.Machine.Taking.Reason == machines.NotTakingStopped {
		t.Fatalf("a new machine is stopped: %+v", v.Machine.Taking)
	}
	// The test machine has no agent session, so it is offline: offline comes first, Accepting and the legacy reason still say paused.
	wantTaking := machines.NotTakingOffline
	if v.Machine.Online {
		wantTaking = machines.NotTakingModePaused
	}
	if _, _, err := e.Svc.UpdateSettings(ctx, "owner", id, machines.SettingsUpdate{Mode: str(machines.ModePaused)}); err != nil {
		t.Fatal(err)
	}
	v, _ = e.Svc.Get(ctx, id)
	if v.Machine.Taking.Taking || v.Machine.Taking.Reason != wantTaking || v.Machine.Accepting || v.Machine.PauseReason != machines.PauseReasonPaused {
		t.Fatalf("mode Paused: taking %+v accepting %v reason %q", v.Machine.Taking, v.Machine.Accepting, v.Machine.PauseReason)
	}
}
