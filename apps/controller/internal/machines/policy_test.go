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

func str(s string) *string { return &s }
func num(n int) *int       { return &n }

// R5.61: the defaults are Automatic, the battery pause on, and no CPU cap.
func TestPolicyDefaults(t *testing.T) {
	e, id, _ := setup(t)
	v, _ := e.Svc.Get(context.Background(), id)
	s := v.Settings
	if s.EffectiveMode() != machines.ModeAutomatic || !s.PauseOnBattery || s.EffectiveCPUCap() != 100 || s.UserPaused(e.Clk.Now()) {
		t.Fatalf("policy defaults = %+v", s)
	}
	// A machine that reported nothing accepts, so an older agent keeps working.
	if !v.Machine.Accepting || v.Machine.PauseReason != "" || !v.Policy.Accepting {
		t.Fatalf("status before a report: %+v", v.Machine)
	}
}

// R5.61: mode transitions. Every mode is accepted, a bad word is refused, and each change is audited and pushed.
func TestModeTransitions(t *testing.T) {
	e, id, _ := setup(t)
	ctx := context.Background()
	for _, from := range []string{machines.ModeAutomatic, machines.ModeAlways, machines.ModePaused} {
		for _, to := range []string{machines.ModeAutomatic, machines.ModeAlways, machines.ModePaused} {
			if _, _, err := e.Svc.UpdateSettings(ctx, "owner", id, machines.SettingsUpdate{Mode: str(from)}); err != nil {
				t.Fatalf("%s: %v", from, err)
			}
			v, _, err := e.Svc.UpdateSettings(ctx, "owner", id, machines.SettingsUpdate{Mode: str(to)})
			if err != nil || v.Settings.Mode != to {
				t.Fatalf("%s -> %s: %v %q", from, to, err, v.Settings.Mode)
			}
		}
	}
	if _, _, err := e.Svc.UpdateSettings(ctx, "owner", id, machines.SettingsUpdate{Mode: str("sometimes")}); !errors.Is(err, machines.ErrInvalid) {
		t.Fatalf("a bad mode: %v", err)
	}
	v, _ := e.Svc.Get(ctx, id)
	if v.Settings.Mode != machines.ModePaused {
		t.Fatalf("a refused change moved the mode to %q", v.Settings.Mode)
	}
	audited := false
	for _, a := range e.Repo.AuditLog() {
		if a.Action == "machine.settings" && a.Subject == id && strings.Contains(a.Detail, "mode") {
			audited = true
		}
	}
	if !audited {
		t.Fatal("a mode change is not in the audit log")
	}
}

// R5.64: the CPU cap is 25, 50, 75 or 100.
func TestCPUCapValues(t *testing.T) {
	e, id, _ := setup(t)
	ctx := context.Background()
	for _, ok := range []int{25, 50, 75, 100} {
		if v, _, err := e.Svc.UpdateSettings(ctx, "owner", id, machines.SettingsUpdate{CPUCapPct: num(ok)}); err != nil || v.Settings.CPUCapPct != ok {
			t.Fatalf("cap %d: %v", ok, err)
		}
	}
	for _, bad := range []int{0, 10, 60, 101, -5} {
		if _, _, err := e.Svc.UpdateSettings(ctx, "owner", id, machines.SettingsUpdate{CPUCapPct: num(bad)}); !errors.Is(err, machines.ErrInvalid) {
			t.Fatalf("cap %d should be refused: %v", bad, err)
		}
	}
}

// R5.61: the pause from the tray is one hour from the controller clock, until Resume, or none.
func TestPauseFromTheTray(t *testing.T) {
	e, id, _ := setup(t)
	ctx := context.Background()
	v, _, err := e.Svc.UpdateSettings(ctx, "tray:1000", id, machines.SettingsUpdate{Pause: str(machines.PauseOneHour)})
	if err != nil {
		t.Fatal(err)
	}
	if want := e.Clk.Now().Add(time.Hour); !v.Settings.PauseUntil.Equal(want) || v.Settings.PausedUntilResumed {
		t.Fatalf("one hour: until %v, want %v", v.Settings.PauseUntil, want)
	}
	if !v.Settings.UserPaused(e.Clk.Now()) || v.Settings.UserPaused(e.Clk.Now().Add(61*time.Minute)) {
		t.Fatal("the timer must hold for the hour and end after it")
	}
	v, _, _ = e.Svc.UpdateSettings(ctx, "tray:1000", id, machines.SettingsUpdate{Pause: str(machines.PauseUntilResumedValue)})
	if !v.Settings.PausedUntilResumed || !v.Settings.PauseUntil.IsZero() || !v.Settings.UserPaused(e.Clk.Now().Add(1000*time.Hour)) {
		t.Fatalf("until resumed: %+v", v.Settings)
	}
	v, _, _ = e.Svc.UpdateSettings(ctx, "tray:1000", id, machines.SettingsUpdate{Pause: str(machines.PauseNone)})
	if v.Settings.PausedUntilResumed || !v.Settings.PauseUntil.IsZero() || v.Settings.UserPaused(e.Clk.Now()) {
		t.Fatalf("resume: %+v", v.Settings)
	}
	if _, _, err := e.Svc.UpdateSettings(ctx, "tray:1000", id, machines.SettingsUpdate{Pause: str("tomorrow")}); !errors.Is(err, machines.ErrInvalid) {
		t.Fatalf("a bad pause: %v", err)
	}
}

// R5.24: the policy reaches an online agent with the next settings message.
func TestPolicyReachesTheAgent(t *testing.T) {
	e, id, key := setup(t)
	a, err := e.session(id, key, &agentv1.Hello{MachineId: id, AgentVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, _, err := e.Svc.UpdateSettings(ctx, "owner", id, machines.SettingsUpdate{Mode: str(machines.ModeAlways), CPUCapPct: num(50), PauseOnBattery: boolp(false)}); err != nil {
		t.Fatal(err)
	}
	got := a.Next(func(m *agentv1.SessionResponse) bool { return m.GetSettings().GetMode() == "always" }).GetSettings()
	if got.GetMode() != "always" || got.GetCpuCapPct() != 50 || got.GetPauseOnBattery() {
		t.Fatalf("settings on the wire: %+v", got)
	}
	if _, _, err := e.Svc.UpdateSettings(ctx, "tray:1000", id, machines.SettingsUpdate{Pause: str(machines.PauseUntilResumedValue)}); err != nil {
		t.Fatal(err)
	}
	got = a.Next(func(m *agentv1.SessionResponse) bool { return m.GetSettings().GetPauseUntilResumed() }).GetSettings()
	if !got.GetPauseUntilResumed() {
		t.Fatalf("pause on the wire: %+v", got)
	}
}

func boolp(b bool) *bool { return &b }

// R5.62: the agent's report sets Accepting and PauseReason on the machine; offline machines keep the last report and CRSP74 reads online && Accepting.
func TestAcceptingReportSetsMachineStatus(t *testing.T) {
	e, id, key := setup(t)
	a, err := e.session(id, key, &agentv1.Hello{MachineId: id, AgentVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	a.Send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_AcceptingReport{AcceptingReport: &agentv1.AcceptingReport{
		Accepting: false, PauseReason: "on_battery", OnBattery: true, BatteryKnown: true, CpuCapPct: 50, CpuCapState: "not_supported", CpuCapDetail: "CPU cap is not supported on macOS yet",
	}}})
	waitUntil(t, 10*time.Second, "the report arrives", func() bool { return !e.Svc.Policy(id).Accepting })
	v, _ := e.Svc.Get(ctx, id)
	if v.Machine.Accepting || v.Machine.PauseReason != "on_battery" || !v.Policy.OnBattery || v.Policy.CPUCapState != "not_supported" {
		t.Fatalf("view: %+v %+v", v.Machine, v.Policy)
	}
	online, accepting, reason, err := e.Svc.Accepting(ctx, id)
	if err != nil || !online || accepting || reason != "on_battery" {
		t.Fatalf("Accepting = %v %v %q %v", online, accepting, reason, err)
	}
	// An accepting report clears the reason, even when the agent sent a stale one.
	a.Send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_AcceptingReport{AcceptingReport: &agentv1.AcceptingReport{Accepting: true, PauseReason: "paused"}}})
	waitUntil(t, 10*time.Second, "accepting again", func() bool { return e.Svc.Policy(id).Accepting })
	if p := e.Svc.Policy(id); p.PauseReason != "" {
		t.Fatalf("an accepting machine kept the reason %q", p.PauseReason)
	}
	if _, _, _, err := e.Svc.Accepting(ctx, "nope"); !errors.Is(err, machines.ErrNotFound) {
		t.Fatalf("unknown machine: %v", err)
	}
}

// R5.64: a report about another percent than the chosen one shows as applying,
// without the old error; the state returns when the agent reports the chosen percent.
func TestCPUCapStateFollowsTheChosenPct(t *testing.T) {
	e, id, _ := setup(t)
	ctx := context.Background()
	report := func(pct int32, state, detail string) {
		e.Svc.HandleAccepting(ctx, id, &agentv1.AcceptingReport{Accepting: true, CpuCapPct: pct, CpuCapState: state, CpuCapDetail: detail})
	}
	set := func(pct int) {
		if _, _, err := e.Svc.UpdateSettings(ctx, "owner", id, machines.SettingsUpdate{CPUCapPct: num(pct)}); err != nil {
			t.Fatal(err)
		}
	}
	state := func() (string, string) {
		v, _ := e.Svc.Get(ctx, id)
		return v.Policy.CPUCapState, v.Policy.CPUCapDetail
	}
	set(100)
	report(100, machines.CapFailed, "access denied")
	if s, d := state(); s != machines.CapFailed || d != "access denied" {
		t.Fatalf("matching pct: %q %q", s, d)
	}
	set(50)
	if s, d := state(); s != machines.CapApplying || d != "" {
		t.Fatalf("after the change, old report: %q %q", s, d)
	}
	report(100, machines.CapFailed, "access denied") // a late report of the old value
	if s, _ := state(); s != machines.CapApplying {
		t.Fatalf("a report for another pct: %q", s)
	}
	report(50, machines.CapApplied, "")
	if s, d := state(); s != machines.CapApplied || d != "" {
		t.Fatalf("report for the chosen pct: %q %q", s, d)
	}
}
