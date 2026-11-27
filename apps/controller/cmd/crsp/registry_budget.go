package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// budgetSource is what REG6 gives the gate: the budgets with their use, read
// fresh at each call, and the over_budget recommendation of a refused kind.
// *retention.Service implements it.
type budgetSource interface {
	Budgets(ctx context.Context) ([]registry.Budget, error)
	PushRefused(ctx context.Context, kind registry.Kind) error
}

// layerLedger says whether the layer accounting (REG4) already counts a layer.
type layerLedger interface {
	LayerKnown(ctx context.Context, digest string) (bool, error)
}

// budgetGate wires REG6's budgets into the core's DiskGate and Promoter (spec
// R5.8, R5.41, R5.49, R5.50). The source is set after the core opens, because
// the retention service needs the core's registry; until then nothing is
// refused for budget. Every call reads the budgets again, so a change of a
// budget takes effect at once.
type budgetGate struct {
	src    atomic.Pointer[budgetSource]
	ledger layerLedger
	log    *slog.Logger
}

func newBudgetGate(ledger layerLedger, log *slog.Logger) *budgetGate {
	return &budgetGate{ledger: ledger, log: log}
}

// set gives the gate its budget source.
func (g *budgetGate) set(src budgetSource) { g.src.Store(&src) }

func (g *budgetGate) source() budgetSource {
	if p := g.src.Load(); p != nil {
		return *p
	}
	return nil
}

func (g *budgetGate) budget(ctx context.Context, src budgetSource, kind registry.Kind) (registry.Budget, error) {
	all, err := src.Budgets(ctx)
	if err != nil {
		return registry.Budget{}, fmt.Errorf("read the registry budgets: %w", err)
	}
	for _, b := range all {
		if b.Kind == kind {
			return b, nil
		}
	}
	return registry.Budget{}, fmt.Errorf("no registry budget for kind %q", kind)
}

// OverBudget is core.DiskGate.OverBudget: the kind uses all its budget or more.
// Only that kind is looked at, so a full standard budget never blocks custom.
func (g *budgetGate) OverBudget(ctx context.Context, kind registry.Kind) (bool, error) {
	src := g.source()
	if src == nil {
		return false, nil
	}
	b, err := g.budget(ctx, src, kind)
	if err != nil {
		return false, err
	}
	return b.UsedBytes >= b.LimitBytes, nil
}

// Refused is core.DiskGate.Refused: open (or keep open) the over_budget
// recommendation of the kind. A failure is logged; the refusal stands.
func (g *budgetGate) Refused(ctx context.Context, kind registry.Kind) {
	src := g.source()
	if src == nil {
		return
	}
	if err := src.PushRefused(ctx, kind); err != nil {
		g.log.Warn("registry: could not open the over_budget recommendation", "kind", kind, "err", err)
	}
}

// Room is core.Promoter.Room: refuse a promotion whose new layers (those the
// accounting does not count yet; shared layers are counted once) would take the
// kind over its budget.
func (g *budgetGate) Room(ctx context.Context, kind registry.Kind, layers []registry.Layer) error {
	src := g.source()
	if src == nil {
		return nil
	}
	b, err := g.budget(ctx, src, kind)
	if err != nil {
		return err
	}
	var add int64
	for _, l := range layers {
		known, err := g.ledger.LayerKnown(ctx, l.Digest)
		if err != nil {
			return err
		}
		if !known {
			add += l.SizeBytes
		}
	}
	if b.UsedBytes+add <= b.LimitBytes {
		return nil
	}
	g.Refused(ctx, kind)
	return fmt.Errorf("%w: the %s images would go over their budget", registry.ErrOverBudget, kind)
}
