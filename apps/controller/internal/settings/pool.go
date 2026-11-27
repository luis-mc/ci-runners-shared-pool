package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// Pool and queue settings (machines R5.22, scheduling R5.8 to R5.10,
// observability R5.5). Pool settings are the defaults a machine gets when it
// joins; queue settings decide the order of service. Both live in the
// settings table as one JSON value each, and every change writes one audit
// row in the same transaction.

const (
	keyPool  = "pool_defaults"
	keyQueue = "queue_order"
)

// PoolSettings are the defaults for new machines.
type PoolSettings struct {
	BudgetMemoryPct      int  `json:"budget_memory_pct"`
	BudgetCPUPct         int  `json:"budget_cpu_pct"`
	SizeSlotsFromMachine bool `json:"size_slots_from_machine"`
	DefaultJobSlots      int  `json:"default_job_slots"`
	DrainTimeoutMinutes  int  `json:"drain_timeout_minutes"`
}

// DefaultPool are the defaults of machines R5.22 (85% memory, 60% CPU, 2
// slots, 60 minute drain); slots are sized from the machine (owner decision).
var DefaultPool = PoolSettings{
	BudgetMemoryPct: 85, BudgetCPUPct: 60, SizeSlotsFromMachine: true, DefaultJobSlots: 2, DrainTimeoutMinutes: 60,
}

// QueueKeys are the four keys of the order of service (scheduling R5.8).
var QueueKeys = []string{"priority", "event_class", "fair_share", "queued_at"}

// QueueSettings decide the order of service.
type QueueSettings struct {
	Order               []string `json:"order"`
	FairShare           bool     `json:"fair_share"`
	NoRunnerWaitMinutes int      `json:"no_runner_wait_minutes"`
}

// DefaultQueue is the order of scheduling R5.8 with fair share on and the
// two minute wait of observability R5.5.
func DefaultQueue() QueueSettings {
	return QueueSettings{Order: append([]string(nil), QueueKeys...), FairShare: true, NoRunnerWaitMinutes: 2}
}

// Bounds of the contract (machines R5.22, observability R5.5).
var (
	PoolMemoryRange = Range{10, 95}
	PoolCPURange    = Range{10, 95}
	PoolSlotsRange  = Range{0, 64}
	PoolDrainRange  = Range{5, 1440}
	NoRunnerRange   = Range{1, 60}
)

func (r Range) has(v int) bool { return v >= r.Min && v <= r.Max }

func (p PoolSettings) problems() []string {
	var out []string
	check := func(name string, v int, r Range) {
		if !r.has(v) {
			out = append(out, fmt.Sprintf("%s must be between %d and %d", name, r.Min, r.Max))
		}
	}
	check("budget_memory_pct", p.BudgetMemoryPct, PoolMemoryRange)
	check("budget_cpu_pct", p.BudgetCPUPct, PoolCPURange)
	check("default_job_slots", p.DefaultJobSlots, PoolSlotsRange)
	check("drain_timeout_minutes", p.DrainTimeoutMinutes, PoolDrainRange)
	return out
}

func (q QueueSettings) problems() []string {
	var out []string
	seen := map[string]bool{}
	valid := map[string]bool{}
	for _, k := range QueueKeys {
		valid[k] = true
	}
	for _, k := range q.Order {
		switch {
		case !valid[k]:
			out = append(out, fmt.Sprintf("order has the unknown key %q; the keys are %s", k, strings.Join(QueueKeys, ", ")))
		case seen[k]:
			out = append(out, fmt.Sprintf("order names %s twice", k))
		}
		seen[k] = true
	}
	if len(out) == 0 && len(q.Order) != len(QueueKeys) {
		out = append(out, "order must name each of "+strings.Join(QueueKeys, ", ")+" once")
	}
	if !NoRunnerRange.has(q.NoRunnerWaitMinutes) {
		out = append(out, fmt.Sprintf("no_runner_wait_minutes must be between %d and %d", NoRunnerRange.Min, NoRunnerRange.Max))
	}
	return out
}

// GetPool returns the pool defaults; a stored value that no longer fits its
// ranges (an edited database) falls back to the defaults.
func (s *Service) GetPool(ctx context.Context) (PoolSettings, error) {
	p := DefaultPool
	raw, ok, err := s.raw(ctx, keyPool)
	if err != nil || !ok {
		return p, err
	}
	var v PoolSettings
	if json.Unmarshal([]byte(raw), &v) != nil || len(v.problems()) > 0 {
		return p, nil
	}
	return v, nil
}

// GetQueue returns the queue settings, with the same fallback.
func (s *Service) GetQueue(ctx context.Context) (QueueSettings, error) {
	q := DefaultQueue()
	raw, ok, err := s.raw(ctx, keyQueue)
	if err != nil || !ok {
		return q, err
	}
	var v QueueSettings
	if json.Unmarshal([]byte(raw), &v) != nil || len(v.problems()) > 0 {
		return q, nil
	}
	return v, nil
}

func (s *Service) raw(ctx context.Context, key string) (string, bool, error) {
	rows, err := s.db.ListSettings(ctx)
	if err != nil {
		return "", false, err
	}
	for _, r := range rows {
		if r.Key == key {
			return r.Value, true, nil
		}
	}
	return "", false, nil
}

// UpdatePool validates p, stores it and writes one audit row. A refused
// change writes a "refused" audit row and changes nothing.
func (s *Service) UpdatePool(ctx context.Context, actor audit.Actor, p PoolSettings) (PoolSettings, error) {
	before, err := s.GetPool(ctx)
	if err != nil {
		return PoolSettings{}, err
	}
	target := audit.Target{Type: "settings", ID: "pool"}
	if problems := p.problems(); len(problems) > 0 {
		_ = audit.New(s.db).RecordRefused(ctx, actor, "settings.pool.update", target, map[string]any{"summary": strings.Join(problems, "; ")})
		return PoolSettings{}, &RangeError{Problems: problems}
	}
	changes := map[string]any{}
	diff := func(name, old, now string) {
		if old != now {
			changes[name] = old + " -> " + now
		}
	}
	diff("budget_memory_pct", strconv.Itoa(before.BudgetMemoryPct), strconv.Itoa(p.BudgetMemoryPct))
	diff("budget_cpu_pct", strconv.Itoa(before.BudgetCPUPct), strconv.Itoa(p.BudgetCPUPct))
	diff("size_slots_from_machine", strconv.FormatBool(before.SizeSlotsFromMachine), strconv.FormatBool(p.SizeSlotsFromMachine))
	diff("default_job_slots", strconv.Itoa(before.DefaultJobSlots), strconv.Itoa(p.DefaultJobSlots))
	diff("drain_timeout_minutes", strconv.Itoa(before.DrainTimeoutMinutes), strconv.Itoa(p.DrainTimeoutMinutes))
	if len(changes) == 0 {
		return p, nil
	}
	b, _ := json.Marshal(p)
	if err := s.db.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.SetSetting(keyPool, string(b), actor.Login); err != nil {
			return err
		}
		return audit.RecordTx(tx, actor, "settings.pool.update", target, changes)
	}); err != nil {
		return PoolSettings{}, err
	}
	return p, nil
}

// UpdateQueue validates q, stores it and writes one audit row.
func (s *Service) UpdateQueue(ctx context.Context, actor audit.Actor, q QueueSettings) (QueueSettings, error) {
	before, err := s.GetQueue(ctx)
	if err != nil {
		return QueueSettings{}, err
	}
	target := audit.Target{Type: "settings", ID: "queue"}
	if problems := q.problems(); len(problems) > 0 {
		_ = audit.New(s.db).RecordRefused(ctx, actor, "settings.queue.update", target, map[string]any{"summary": strings.Join(problems, "; ")})
		return QueueSettings{}, &RangeError{Problems: problems}
	}
	changes := map[string]any{}
	if o, n := strings.Join(before.Order, ","), strings.Join(q.Order, ","); o != n {
		changes["order"] = o + " -> " + n
	}
	if before.FairShare != q.FairShare {
		changes["fair_share"] = strconv.FormatBool(before.FairShare) + " -> " + strconv.FormatBool(q.FairShare)
	}
	if before.NoRunnerWaitMinutes != q.NoRunnerWaitMinutes {
		changes["no_runner_wait_minutes"] = strconv.Itoa(before.NoRunnerWaitMinutes) + " -> " + strconv.Itoa(q.NoRunnerWaitMinutes)
	}
	if len(changes) == 0 {
		return q, nil
	}
	b, _ := json.Marshal(q)
	if err := s.db.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.SetSetting(keyQueue, string(b), actor.Login); err != nil {
			return err
		}
		return audit.RecordTx(tx, actor, "settings.queue.update", target, changes)
	}); err != nil {
		return QueueSettings{}, err
	}
	return q, nil
}
