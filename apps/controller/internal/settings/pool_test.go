package settings

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
)

func TestPoolDefaultsSaveAndAudit(t *testing.T) {
	ctx := context.Background()
	s, l := setup(t)
	got, err := s.GetPool(ctx)
	if err != nil || got != DefaultPool || got.DrainTimeoutMinutes != 60 {
		t.Fatalf("%+v %v", got, err)
	}
	want := PoolSettings{BudgetMemoryPct: 70, BudgetCPUPct: 50, SizeSlotsFromMachine: false, DefaultJobSlots: 4, DrainTimeoutMinutes: 120}
	if _, err := s.UpdatePool(ctx, owner, want); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.GetPool(ctx); again != want {
		t.Fatalf("not persisted: %+v", again)
	}
	p, _ := l.List(ctx, audit.Filter{})
	if len(p.Items) != 1 || p.Items[0].Action != "settings.pool.update" || !strings.Contains(p.Items[0].Summary, "85 -> 70") {
		t.Fatalf("%+v", p.Items)
	}
	if _, err := s.UpdatePool(ctx, owner, want); err != nil {
		t.Fatal(err)
	}
	if p, _ := l.List(ctx, audit.Filter{}); len(p.Items) != 1 {
		t.Fatalf("a no-op change was audited: %+v", p.Items)
	}
}

func TestPoolBoundsRefused(t *testing.T) {
	ctx := context.Background()
	s, _ := setup(t)
	bad := DefaultPool
	bad.BudgetMemoryPct, bad.BudgetCPUPct, bad.DefaultJobSlots, bad.DrainTimeoutMinutes = 9, 96, 65, 4
	_, err := s.UpdatePool(ctx, owner, bad)
	var re *RangeError
	if !errors.As(err, &re) || len(re.Problems) != 4 {
		t.Fatalf("%v", err)
	}
	if got, _ := s.GetPool(ctx); got != DefaultPool {
		t.Fatalf("a refused change was applied: %+v", got)
	}
	for _, edge := range []PoolSettings{{10, 10, true, 0, 5}, {95, 95, true, 64, 1440}} {
		if _, err := s.UpdatePool(ctx, owner, edge); err != nil {
			t.Fatalf("%+v: %v", edge, err)
		}
	}
	if got, _ := s.GetPool(ctx); got.BudgetMemoryPct != 95 {
		t.Fatalf("%+v", got)
	}
}

func TestQueueOrderMustBeAPermutation(t *testing.T) {
	ctx := context.Background()
	s, l := setup(t)
	if got, _ := s.GetQueue(ctx); !got.FairShare || got.NoRunnerWaitMinutes != 2 || strings.Join(got.Order, ",") != "priority,event_class,fair_share,queued_at" {
		t.Fatalf("%+v", got)
	}
	for name, order := range map[string][]string{
		"duplicate": {"priority", "priority", "fair_share", "queued_at"},
		"unknown":   {"priority", "event_class", "fair_share", "nope"},
		"short":     {"priority", "queued_at"},
		"empty":     {},
	} {
		q := DefaultQueue()
		q.Order = order
		if _, err := s.UpdateQueue(ctx, owner, q); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	q := DefaultQueue()
	for _, n := range []int{0, 61} {
		q.NoRunnerWaitMinutes = n
		if _, err := s.UpdateQueue(ctx, owner, q); err == nil {
			t.Fatalf("%d minutes accepted", n)
		}
	}
	ok := QueueSettings{Order: []string{"queued_at", "fair_share", "event_class", "priority"}, FairShare: false, NoRunnerWaitMinutes: 60}
	if _, err := s.UpdateQueue(ctx, owner, ok); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetQueue(ctx); strings.Join(got.Order, ",") != "queued_at,fair_share,event_class,priority" || got.FairShare {
		t.Fatalf("%+v", got)
	}
	p, _ := l.List(ctx, audit.Filter{})
	found := false
	for _, it := range p.Items {
		if it.Action == "settings.queue.update" && strings.Contains(it.Summary, "priority,event_class") {
			found = true
		}
	}
	if !found {
		t.Fatalf("%+v", p.Items)
	}
}
