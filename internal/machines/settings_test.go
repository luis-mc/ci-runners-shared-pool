package machines_test

import (
	"context"
	"errors"
	"testing"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler/slots"
)

func setup(t *testing.T) (*env, string, string) {
	t.Helper()
	e := newEnv(t)
	r, _ := e.joined("box-1")
	return e, r.GetMachineId(), r.GetMachineCredential()
}

// R5.22: defaults are 85% memory, 60% CPU, persistent runner only, 20 GiB cache
// cap, 60 minute drain timeout, state active; slots are sized from the budget.
func TestDefaultSettings(t *testing.T) {
	e, id, _ := setup(t)
	v, _ := e.Svc.Get(context.Background(), id)
	s := v.Settings
	if s.BudgetMemoryPct != 85 || s.BudgetCPUPct != 60 || s.CacheCapBytes != 20<<30 || s.DrainTimeoutMinutes != 60 ||
		len(s.AllowedBackends) != 1 || s.AllowedBackends[0] != "persistent" || v.Machine.State != machines.StateActive {
		t.Fatalf("settings = %+v", s)
	}
	// 8 threads at 60% = 4 by CPU; 32 GiB at 85% / 1.5 = 18 by memory: slots = 4 (DL79).
	if s.JobSlots != 4 {
		t.Fatalf("slots sized from the budget = %d, want 4", s.JobSlots)
	}
}

// R5.12: detected does not mean allowed. Backends show allowed only when the
// user allowed them.
func TestDetectedIsNotAllowed(t *testing.T) {
	e, id, _ := setup(t)
	v, _ := e.Svc.Get(context.Background(), id)
	allowed := map[string]bool{}
	for _, b := range v.Backends {
		allowed[b.Name] = b.Allowed
	}
	if !allowed["persistent"] || allowed["docker"] {
		t.Fatalf("allowed = %v: docker is usable but not allowed", allowed)
	}
}

// R5.22: ranges are enforced.
func TestSettingsRanges(t *testing.T) {
	e, id, _ := setup(t)
	bad := []machines.SettingsUpdate{
		{BudgetMemoryPct: ptr(9)}, {BudgetMemoryPct: ptr(96)}, {BudgetCPUPct: ptr(5)}, {BudgetCPUPct: ptr(99)},
		{JobSlots: ptr(-1)}, {JobSlots: ptr(65)}, {DrainTimeoutMinutes: ptr(4)}, {DrainTimeoutMinutes: ptr(1441)},
		{CacheCapBytes: ptr(int64(1 << 20))}, {CacheCapBytes: ptr(int64(300) << 30)}, // above the 200 GiB free
		{AllowedBackends: ptr([]string{"kvm"})},         // detected but not usable
		{AllowedBackends: ptr([]string{"firecracker"})}, // not reported at all
		{GateOnTimeout: ptr("maybe")},
	}
	for i, u := range bad {
		if _, _, err := e.Svc.UpdateSettings(context.Background(), "owner", id, u); !errors.Is(err, machines.ErrInvalid) {
			t.Errorf("case %d: err = %v, want ErrInvalid", i, err)
		}
	}
	ok := machines.SettingsUpdate{BudgetMemoryPct: ptr(10), BudgetCPUPct: ptr(95), JobSlots: ptr(0), DrainTimeoutMinutes: ptr(1440)}
	if _, _, err := e.Svc.UpdateSettings(context.Background(), "owner", id, ok); err != nil {
		t.Fatalf("limits are allowed: %v", err)
	}
}

// R5.25: a change that does not restart runners takes effect for the next job.
// It reaches the agent at once (R5.24) and running jobs are untouched even when
// the new budget is exceeded.
func TestBudgetChangeNeedsNoDrainAndReachesAgent(t *testing.T) {
	e, id, key := setup(t)
	fa, err := e.session(id, key, &agentv1.Hello{Holders: &agentv1.HolderReport{Running: []*agentv1.JobRef{{Repository: "o/r", RunId: 1, Job: "build"}}}})
	if err != nil {
		t.Fatal(err)
	}
	v, ch, err := e.Svc.UpdateSettings(context.Background(), "owner", id, machines.SettingsUpdate{BudgetMemoryPct: ptr(20), JobSlots: ptr(1)})
	if err != nil || ch != nil {
		t.Fatalf("v=%v ch=%v err=%v: a budget change must not drain", v, ch, err)
	}
	m := fa.Next(isSettings)
	if m.GetSettings().GetBudgetMemoryPct() != 20 || m.GetSettings().GetJobSlots() != 1 || m.GetSettings().GetVersion() != 2 {
		t.Fatalf("pushed settings = %+v", m.GetSettings())
	}
	if v.Machine.State != machines.StateActive || len(v.RunningJobs) != 1 {
		t.Fatalf("state=%v running=%d: running jobs must keep going", v.Machine.State, len(v.RunningJobs))
	}
}

// R5.24: an offline agent gets the settings in HelloAck on reconnect.
func TestOfflineAgentGetsSettingsOnReconnect(t *testing.T) {
	e, id, key := setup(t)
	if _, _, err := e.Svc.UpdateSettings(context.Background(), "owner", id, machines.SettingsUpdate{JobSlots: ptr(7)}); err != nil {
		t.Fatal(err)
	}
	fa, err := e.session(id, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fa.Ack.GetSettings().GetJobSlots() != 7 {
		t.Fatalf("ack settings = %+v", fa.Ack.GetSettings())
	}
}

func TestRename(t *testing.T) {
	e, id, _ := setup(t)
	v, _, err := e.Svc.UpdateSettings(context.Background(), "owner", id, machines.SettingsUpdate{Name: ptr("build-box")})
	if err != nil || v.Machine.Name != "build-box" {
		t.Fatalf("%v %v", v.Machine.Name, err)
	}
	if _, _, err := e.Svc.UpdateSettings(context.Background(), "owner", id, machines.SettingsUpdate{Name: ptr("")}); !errors.Is(err, machines.ErrInvalid) {
		t.Fatalf("empty name: %v", err)
	}
}

// CRSP235 (scheduling R5.75): Gate maximum wait is 1 or 2 minutes, saved from the
// console, sent to the agent, and never above the hold cap. The default is the cap.
func TestGateMaxWaitIsSavedWithinTheHoldCap(t *testing.T) {
	if machines.MaxGateWaitMinutes != slots.MaxHoldMin {
		t.Fatalf("the saved maximum %d must equal the hold cap %d", machines.MaxGateWaitMinutes, slots.MaxHoldMin)
	}
	e, id, key := setup(t)
	fa, err := e.session(id, key, &agentv1.Hello{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if v, _ := e.Svc.Get(ctx, id); v.Settings.GateMaxWaitMinutes != slots.MaxHoldMin {
		t.Fatalf("default = %d, want the hold cap", v.Settings.GateMaxWaitMinutes)
	}
	for _, bad := range []int{0, -1, slots.MaxHoldMin + 1, 30} {
		if _, _, err := e.Svc.UpdateSettings(ctx, "owner", id, machines.SettingsUpdate{GateMaxWaitMinutes: &bad}); !errors.Is(err, machines.ErrInvalid) {
			t.Fatalf("%d: err = %v, want invalid", bad, err)
		}
	}
	one := 1
	v, _, err := e.Svc.UpdateSettings(ctx, "owner", id, machines.SettingsUpdate{GateMaxWaitMinutes: &one})
	if err != nil || v.Settings.GateMaxWaitMinutes != 1 {
		t.Fatalf("saved = %+v, %v", v.Settings, err)
	}
	got := fa.Next(func(m *agentv1.SessionResponse) bool {
		return m.GetSettings() != nil && m.GetSettings().GetGateMaxWaitMinutes() == 1
	})
	if got == nil {
		t.Fatal("the agent did not get the new wait")
	}
}
