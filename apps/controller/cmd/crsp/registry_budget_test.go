package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/core"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
)

type fakeBudgets struct {
	limit, used map[registry.Kind]int64
	refused     []registry.Kind
}

func (f *fakeBudgets) Budgets(context.Context) ([]registry.Budget, error) {
	var out []registry.Budget
	for _, k := range []registry.Kind{registry.KindStandard, registry.KindCustom} {
		out = append(out, registry.Budget{Kind: k, LimitBytes: f.limit[k], UsedBytes: f.used[k]})
	}
	return out, nil
}

func (f *fakeBudgets) PushRefused(_ context.Context, k registry.Kind) error {
	f.refused = append(f.refused, k)
	return nil
}

type fakeLedger map[string]bool

func (l fakeLedger) LayerKnown(_ context.Context, d string) (bool, error) { return l[d], nil }

func testGate(src *fakeBudgets, known fakeLedger) (*budgetGate, *core.DiskGate) {
	g := newBudgetGate(known, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if src != nil {
		g.set(src)
	}
	dg := &core.DiskGate{
		Folder:     "x",
		Disk:       func(string) (runnercache.Disk, error) { return runnercache.Disk{Total: 100, Free: 90}, nil },
		OverBudget: g.OverBudget, Refused: g.Refused,
	}
	return g, dg
}

const (
	stdPush    = "crsp/incoming/m1/std/base"
	customPush = "crsp/incoming/m1/custom/acme-web/node"
)

// R5.8, R5.41: under budget a push token is allowed; at the budget it is refused with ErrOverBudget.
func TestBudgetGatePush(t *testing.T) {
	src := &fakeBudgets{
		limit: map[registry.Kind]int64{registry.KindStandard: 40, registry.KindCustom: 30},
		used:  map[registry.Kind]int64{registry.KindStandard: 39, registry.KindCustom: 1},
	}
	_, dg := testGate(src, nil)
	ctx := context.Background()
	if err := dg.AllowPush(ctx, stdPush); err != nil {
		t.Fatalf("under budget: %v", err)
	}
	src.used[registry.KindStandard] = 40
	if err := dg.AllowPush(ctx, stdPush); !errors.Is(err, registry.ErrOverBudget) {
		t.Fatalf("at budget: err = %v", err)
	}
	// R5.8: standard full does not block custom.
	if err := dg.AllowPush(ctx, customPush); err != nil {
		t.Fatalf("custom blocked by a full standard budget: %v", err)
	}
	src.used[registry.KindCustom] = 31
	if err := dg.AllowPush(ctx, customPush); !errors.Is(err, registry.ErrOverBudget) {
		t.Fatalf("custom over: err = %v", err)
	}
}

// R5.50: each refusal opens the recommendation of its kind (the service keeps one per kind).
func TestBudgetGateRefusedNamesKind(t *testing.T) {
	src := &fakeBudgets{
		limit: map[registry.Kind]int64{registry.KindStandard: 10, registry.KindCustom: 10},
		used:  map[registry.Kind]int64{registry.KindStandard: 10, registry.KindCustom: 1},
	}
	_, dg := testGate(src, nil)
	_ = dg.AllowPush(context.Background(), stdPush)
	_ = dg.AllowPush(context.Background(), customPush)
	if len(src.refused) != 1 || src.refused[0] != registry.KindStandard {
		t.Fatalf("recommendations opened for %v, want [standard]", src.refused)
	}
}

// R5.41: a changed budget counts at the next push, with no restart.
func TestBudgetGateChangeTakesEffect(t *testing.T) {
	src := &fakeBudgets{
		limit: map[registry.Kind]int64{registry.KindStandard: 10, registry.KindCustom: 10},
		used:  map[registry.Kind]int64{registry.KindStandard: 10},
	}
	_, dg := testGate(src, nil)
	ctx := context.Background()
	if err := dg.AllowPush(ctx, stdPush); !errors.Is(err, registry.ErrOverBudget) {
		t.Fatalf("err = %v", err)
	}
	src.limit[registry.KindStandard] = 20
	if err := dg.AllowPush(ctx, stdPush); err != nil {
		t.Fatalf("after raising the budget: %v", err)
	}
}

// No budget source yet (the core opens before retention): nothing is refused for budget.
func TestBudgetGateWithoutSource(t *testing.T) {
	_, dg := testGate(nil, nil)
	if err := dg.AllowPush(context.Background(), stdPush); err != nil {
		t.Fatal(err)
	}
}

// R5.41, R5.50: a promotion is refused when its new layers go over the budget; shared layers count once.
func TestBudgetGateRoom(t *testing.T) {
	src := &fakeBudgets{
		limit: map[registry.Kind]int64{registry.KindStandard: 100, registry.KindCustom: 100},
		used:  map[registry.Kind]int64{registry.KindStandard: 90},
	}
	g, _ := testGate(src, fakeLedger{"sha256:base": true})
	layers := []registry.Layer{{Digest: "sha256:base", SizeBytes: 500}, {Digest: "sha256:new", SizeBytes: 10}}
	ctx := context.Background()
	if err := g.Room(ctx, registry.KindStandard, layers); err != nil {
		t.Fatalf("a known layer was counted again: %v", err)
	}
	layers[1].SizeBytes = 11
	if err := g.Room(ctx, registry.KindStandard, layers); !errors.Is(err, registry.ErrOverBudget) {
		t.Fatalf("err = %v", err)
	}
	if len(src.refused) != 1 || src.refused[0] != registry.KindStandard {
		t.Fatalf("recommendations = %v", src.refused)
	}
	if err := g.Room(ctx, registry.KindCustom, layers); err != nil {
		t.Fatalf("custom has room: %v", err)
	}
}
