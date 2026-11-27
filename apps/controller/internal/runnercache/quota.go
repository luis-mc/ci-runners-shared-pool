package runnercache

import (
	"errors"
	"fmt"
)

const (
	// GiB is one gibibyte.
	GiB int64 = 1 << 30

	// DefaultToolQuotaGiB is the default quota of a runner's tool cache.
	DefaultToolQuotaGiB = 20
	// DefaultRustQuotaGiB is the default cap of a runner's Rust build cache.
	DefaultRustQuotaGiB = 40
	// DefaultMinFreePct is the free space under which new jobs are held.
	DefaultMinFreePct = 10
	// DefaultRustKeyGiB is the default cap of one cache key of the Rust build
	// cache (caches R5.45). It is lower than the whole cap on purpose: one run
	// that rebuilds a workspace must not fill the whole cap alone.
	DefaultRustKeyGiB = 25
	// DefaultBudgetGiB is the default pool-wide budget of all cache folders on
	// one machine (caches R5.48).
	DefaultBudgetGiB = 150
	// DefaultAlertGiB is the size past which one cache file or folder raises
	// an alert (caches R5.49).
	DefaultAlertGiB = 20
	// ResumeMarginPct is added to the minimum before held jobs go again, so
	// the guard does not flap around the limit.
	ResumeMarginPct = 2
)

// Quota is the declared cache sizing of one machine's runners, in GiB per
// runner. A zero field takes its default. Quotas come from configuration.
type Quota struct {
	ToolGiB int `json:"tool_gib" yaml:"tool_gib"`
	RustGiB int `json:"rust_gib" yaml:"rust_gib"`
	// RustKeyGiB caps one cache key (top-level folder) of the Rust cache.
	RustKeyGiB int `json:"rust_key_gib" yaml:"rust_key_gib"`
	// BudgetGiB is the pool-wide budget of every cache folder on a machine.
	BudgetGiB int `json:"budget_gib" yaml:"budget_gib"`
	// AlertGiB is the size of one cache file or folder that raises an alert.
	AlertGiB int `json:"alert_gib" yaml:"alert_gib"`
	// MinFreePct is the disk guard threshold in percent of the volume.
	MinFreePct int `json:"min_free_pct" yaml:"min_free_pct"`
}

// WithDefaults fills the zero fields.
func (q Quota) WithDefaults() Quota {
	if q.ToolGiB == 0 {
		q.ToolGiB = DefaultToolQuotaGiB
	}
	if q.RustGiB == 0 {
		q.RustGiB = DefaultRustQuotaGiB
	}
	if q.RustKeyGiB == 0 {
		q.RustKeyGiB = DefaultRustKeyGiB
	}
	if q.BudgetGiB == 0 {
		q.BudgetGiB = DefaultBudgetGiB
	}
	if q.AlertGiB == 0 {
		q.AlertGiB = DefaultAlertGiB
	}
	if q.MinFreePct == 0 {
		q.MinFreePct = DefaultMinFreePct
	}
	return q
}

// Validate refuses a quota that cannot work. Call it on a defaulted value.
func (q Quota) Validate() error {
	switch {
	case q.ToolGiB < 0 || q.RustGiB < 0 || q.RustKeyGiB < 0 || q.BudgetGiB < 0 || q.AlertGiB < 0:
		return errors.New("a cache quota cannot be negative")
	case q.MinFreePct < 1 || q.MinFreePct > 50:
		return fmt.Errorf("min_free_pct %d is outside 1 to 50", q.MinFreePct)
	}
	return nil
}

// ToolBytes is the tool cache quota in bytes.
func (q Quota) ToolBytes() int64 { return int64(q.ToolGiB) * GiB }

// RustBytes is the Rust build cache cap in bytes.
func (q Quota) RustBytes() int64 { return int64(q.RustGiB) * GiB }

// RustKeyBytes is the cap of one Rust cache key in bytes. A key cap above the
// whole cap is the whole cap.
func (q Quota) RustKeyBytes() int64 {
	k := int64(q.RustKeyGiB) * GiB
	if w := q.RustBytes(); k <= 0 || (w > 0 && k > w) {
		return w
	}
	return k
}

// BudgetBytes is the pool-wide cache budget in bytes.
func (q Quota) BudgetBytes() int64 { return int64(q.BudgetGiB) * GiB }

// AlertBytes is the size alert threshold in bytes.
func (q Quota) AlertBytes() int64 { return int64(q.AlertGiB) * GiB }
