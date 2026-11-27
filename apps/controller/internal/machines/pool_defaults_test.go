package machines_test

import (
	"context"
	"errors"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// A machine that joins starts with the pool's Budgets defaults (R5.22); sizing
// from the machine uses the pool budget, and a fixed slot count is kept.
func TestJoinUsesPoolDefaults(t *testing.T) {
	pool := machines.PoolDefaults{BudgetMemoryPct: 50, BudgetCPUPct: 25, SizeSlotsFromMachine: false, JobSlots: 5, DrainTimeoutMinutes: 90}
	e := newEnv(t, func(o *machines.Options) {
		o.PoolDefaults = func(context.Context) (machines.PoolDefaults, error) { return pool, nil }
	})
	r, _ := e.joined("box-1")
	s := r.GetSettings()
	if s.GetBudgetMemoryPct() != 50 || s.GetBudgetCpuPct() != 25 || s.GetJobSlots() != 5 || s.GetDrainTimeoutMinutes() != 90 {
		t.Fatalf("settings = %+v", s)
	}
}

func TestJoinSizesSlotsFromMachineWithinPoolBudget(t *testing.T) {
	pool := machines.PoolDefaults{BudgetMemoryPct: 85, BudgetCPUPct: 25, SizeSlotsFromMachine: true, JobSlots: 9, DrainTimeoutMinutes: 60}
	e := newEnv(t, func(o *machines.Options) {
		o.PoolDefaults = func(context.Context) (machines.PoolDefaults, error) { return pool, nil }
	})
	r, _ := e.joined("box-1")
	// 8 threads at 25% is 2 slots; 32 GiB at 85% is more.
	if got := r.GetSettings().GetJobSlots(); got != 2 {
		t.Fatalf("slots = %d, want 2", got)
	}
}

func TestJoinFallsBackWhenPoolDefaultsFail(t *testing.T) {
	e := newEnv(t, func(o *machines.Options) {
		o.PoolDefaults = func(context.Context) (machines.PoolDefaults, error) {
			return machines.PoolDefaults{}, errors.New("db down")
		}
	})
	r, _ := e.joined("box-1")
	if r.GetSettings().GetBudgetMemoryPct() != 85 || r.GetSettings().GetDrainTimeoutMinutes() != 60 {
		t.Fatalf("settings = %+v", r.GetSettings())
	}
}
