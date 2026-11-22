package machines_test

import (
	"context"
	"errors"
	"testing"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
)

// runners R5.83, OPS86: the default sets no console value, so a new machine
// keeps the first value of its agent (10 GiB).
func TestDiskFloorIsUnsetByDefault(t *testing.T) {
	e, id, _ := setup(t)
	v, _ := e.Svc.Get(context.Background(), id)
	if v.Settings.DiskFloorGiB != 0 || !machines.IsDefaultSettings(v.Settings) {
		t.Fatalf("default settings = %+v", v.Settings)
	}
}

// OPS86: 1 to 2000 GiB is accepted and read back; anything else is refused and
// moves nothing.
func TestDiskFloorRange(t *testing.T) {
	e, id, _ := setup(t)
	ctx := context.Background()
	for _, ok := range []int{1, 150, 2000} {
		if v, _, err := e.Svc.UpdateSettings(ctx, "owner", id, machines.SettingsUpdate{DiskFloorGiB: num(ok)}); err != nil || v.Settings.DiskFloorGiB != ok {
			t.Fatalf("floor %d: %v", ok, err)
		}
	}
	for _, bad := range []int{0, -1, 2001, 100000} {
		if _, _, err := e.Svc.UpdateSettings(ctx, "owner", id, machines.SettingsUpdate{DiskFloorGiB: num(bad)}); !errors.Is(err, machines.ErrInvalid) {
			t.Fatalf("floor %d should be refused: %v", bad, err)
		}
	}
	if v, _ := e.Svc.Get(ctx, id); v.Settings.DiskFloorGiB != 2000 {
		t.Fatalf("a refused change moved the floor to %d", v.Settings.DiskFloorGiB)
	}
}

// OPS86: the new value is pushed to the online agent at once, with no drain.
func TestDiskFloorReachesTheAgent(t *testing.T) {
	e, id, key := setup(t)
	a, err := e.session(id, key, &agentv1.Hello{MachineId: id, AgentVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	v, ch, err := e.Svc.UpdateSettings(context.Background(), "owner", id, machines.SettingsUpdate{DiskFloorGiB: num(150)})
	if err != nil || ch != nil || v.PendingChange != nil {
		t.Fatalf("a floor change waited for a drain: %v %+v", err, ch)
	}
	got := a.Next(func(m *agentv1.SessionResponse) bool { return m.GetSettings().GetDiskFloorGib() == 150 }).GetSettings()
	if got.GetDiskFloorGib() != 150 {
		t.Fatalf("on the wire: %+v", got)
	}
}

// OPS86: a floor turned off in config is shown read-only: the agent says so in
// its report and the console value is refused.
func TestDiskFloorOffInConfigRefusesTheConsole(t *testing.T) {
	e, id, _ := setup(t)
	ctx := context.Background()
	e.Svc.HandleAccepting(ctx, id, &agentv1.AcceptingReport{Accepting: true, DiskFloorOff: true})
	if v, _ := e.Svc.Get(ctx, id); !v.Policy.DiskFloorOff {
		t.Fatalf("the report did not reach the view: %+v", v.Policy)
	}
	if _, _, err := e.Svc.UpdateSettings(ctx, "owner", id, machines.SettingsUpdate{DiskFloorGiB: num(150)}); !errors.Is(err, machines.ErrInvalid) {
		t.Fatalf("a console value while the floor is off: %v", err)
	}
	e.Svc.HandleAccepting(ctx, id, &agentv1.AcceptingReport{Accepting: true, DiskFloorGib: 10})
	v, _ := e.Svc.Get(ctx, id)
	if v.Policy.DiskFloorOff || v.Policy.DiskFloorGiB != 10 {
		t.Fatalf("the floor in force: %+v", v.Policy)
	}
	if _, _, err := e.Svc.UpdateSettings(ctx, "owner", id, machines.SettingsUpdate{DiskFloorGiB: num(150)}); err != nil {
		t.Fatalf("the floor is on again: %v", err)
	}
}
