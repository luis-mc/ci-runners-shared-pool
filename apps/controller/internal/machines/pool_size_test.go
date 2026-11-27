package machines_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// sized reports a load of used% (32 GiB machine, 8 threads: CPU budget 4,
// memory budget 27.2 GiB) and waits until the controller has it.
func sized(t *testing.T, usedPct float64) (*env, string) {
	t.Helper()
	e, id, key := setup(t)
	fa, _ := e.session(id, key, nil)
	fa.Send(loadReport(usedPct))
	waitFor(t, "load", func() bool {
		v, _ := e.Svc.Get(context.Background(), id)
		return v.Load != nil && v.Load.MemoryPct == usedPct
	})
	return e, id
}

func upd(e *env, id string, u machines.SettingsUpdate) error {
	_, _, err := e.Svc.UpdateSettings(context.Background(), "owner", id, u)
	return err
}

// R5.58: a size that fits is saved and reaches the view.
func TestPoolSizeThatFitsIsSaved(t *testing.T) {
	e, id := sized(t, 25) // 8 GiB used outside: room 19.2 GiB
	if err := upd(e, id, machines.SettingsUpdate{PoolMemoryGiB: ptr(12.0), PoolCPUs: ptr(4)}); err != nil {
		t.Fatal(err)
	}
	v, _ := e.Svc.Get(context.Background(), id)
	if v.Settings.PoolMemoryGiB != 12 || v.Settings.PoolCPUs != 4 {
		t.Fatalf("settings = %+v", v.Settings)
	}
}

// R5.58: a size over the budget is refused with the figures and a nearest size.
func TestPoolSizeOverTheBudgetIsRefusedWithNearestSize(t *testing.T) {
	e, id := sized(t, 25)
	err := upd(e, id, machines.SettingsUpdate{PoolMemoryGiB: ptr(24.0), PoolCPUs: ptr(4)})
	if !errors.Is(err, machines.ErrInvalid) || !strings.Contains(err.Error(), "memory budget") || !strings.Contains(err.Error(), "19 GiB fits 4 CPUs") {
		t.Fatalf("err = %v", err)
	}
	err = upd(e, id, machines.SettingsUpdate{PoolMemoryGiB: ptr(20.0), PoolCPUs: ptr(6)})
	if !errors.Is(err, machines.ErrInvalid) || !strings.Contains(err.Error(), "CPU budget") {
		t.Fatalf("cpu err = %v", err)
	}
	if v, _ := e.Svc.Get(context.Background(), id); v.Settings.PoolMemoryGiB != 0 {
		t.Fatal("a refused size must not be saved")
	}
}

// R5.58: a shrink is never accidental.
func TestPoolSizeShrinkNeedsItsOwnConfirm(t *testing.T) {
	e, id := sized(t, 25)
	if err := upd(e, id, machines.SettingsUpdate{PoolMemoryGiB: ptr(16.0), PoolCPUs: ptr(4)}); err != nil {
		t.Fatal(err)
	}
	small := machines.SettingsUpdate{PoolMemoryGiB: ptr(8.0), PoolCPUs: ptr(4)}
	var nc *machines.NeedsConfirmationError
	if err := upd(e, id, small); !errors.As(err, &nc) || !strings.Contains(nc.Message, "confirm_shrink") {
		t.Fatalf("err = %v", err)
	}
	// The general confirm (jobs a restart waits for) is not a yes to a shrink.
	small.Confirm = true
	if err := upd(e, id, small); !errors.As(err, &nc) {
		t.Fatalf("Confirm must not confirm a shrink: %v", err)
	}
	small.ConfirmShrink = true
	if err := upd(e, id, small); err != nil {
		t.Fatal(err)
	}
	if v, _ := e.Svc.Get(context.Background(), id); v.Settings.PoolMemoryGiB != 8 {
		t.Fatalf("settings = %+v", v.Settings)
	}
}

// R5.58: another change never shrinks the pool as a side effect.
func TestAnotherChangeDoesNotShrinkThePool(t *testing.T) {
	e, id := sized(t, 25)
	if err := upd(e, id, machines.SettingsUpdate{PoolMemoryGiB: ptr(16.0), PoolCPUs: ptr(4)}); err != nil {
		t.Fatal(err)
	}
	if err := upd(e, id, machines.SettingsUpdate{JobSlots: ptr(3)}); err != nil {
		t.Fatal(err)
	}
	if v, _ := e.Svc.Get(context.Background(), id); v.Settings.PoolMemoryGiB != 16 || v.Settings.PoolCPUs != 4 {
		t.Fatalf("pool changed: %+v", v.Settings)
	}
	// A lower budget that no longer holds the pool is refused, not applied.
	if err := upd(e, id, machines.SettingsUpdate{BudgetMemoryPct: ptr(50)}); !errors.Is(err, machines.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestPoolSizeNeedsBothValuesAndAKnownLoad(t *testing.T) {
	e, id := sized(t, 25)
	if err := upd(e, id, machines.SettingsUpdate{PoolCPUs: ptr(4)}); !errors.Is(err, machines.ErrInvalid) {
		t.Fatalf("one value: %v", err)
	}
	e2, id2, _ := setup(t)
	if err := upd(e2, id2, machines.SettingsUpdate{PoolMemoryGiB: ptr(12.0), PoolCPUs: ptr(4)}); !errors.Is(err, machines.ErrInvalid) {
		t.Fatalf("no load yet: %v", err)
	}
}

// R5.54: .wslconfig values reach the facts, and the pool cannot go above them.
func TestWSLConfigFactsAndDockerCeiling(t *testing.T) {
	f := machines.FactsFromProto(&agentv1.Facts{WslConfigPresent: true, WslMemoryBytes: 8 << 30, WslProcessors: 4, WslNestedVirtualization: true, MemoryBytes: 32 << 30, Threads: 8})
	if !f.WSL.ConfigPresent || f.WSL.MemoryBytes != 8<<30 || f.WSL.Processors != 4 || !f.WSL.NestedVirtualization {
		t.Fatalf("facts = %+v", f.WSL)
	}
	if got := machines.ProtoSettings(machines.Settings{PoolMemoryGiB: 12, PoolCPUs: 4}, "active"); got.GetPoolMemoryGib() != 12 || got.GetPoolCpus() != 4 {
		t.Fatalf("wire = %+v", got)
	}
}
