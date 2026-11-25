// Package history holds what the pool saved: the per-minute rate table of
// GitHub-hosted runners and the minutes and money saved report
// (observability R5.15 to R5.19).
package history

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

// SettingKey is the settings row that holds the owner's changed rate table.
const SettingKey = "savings_rates"

// SizeStandard is the size a machine has until the owner maps it to another.
const SizeStandard = "standard"

// Rate is the price of one billed minute of one GitHub-hosted runner kind.
type Rate struct {
	OS        string  `json:"os"`
	Size      string  `json:"size"`
	PerMinute float64 `json:"per_minute"`
}

// Table is the rate table. SourceURL and CopiedOn say where the shipped rates came from and when (R5.16).
type Table struct {
	SourceURL string `json:"source_url"`
	CopiedOn  string `json:"copied_on"`
	// Changed is true when the owner changed a rate or a machine size.
	Changed bool   `json:"changed"`
	Rates   []Rate `json:"rates"`
	// MachineSizes maps a machine name to the size its jobs are priced at.
	MachineSizes map[string]string `json:"machine_sizes"`
}

// ShippedSourceURL is the page the shipped rates were copied from.
const ShippedSourceURL = "https://docs.github.com/en/billing/reference/actions-runner-pricing"

// ShippedCopiedOn is the date the shipped rates were copied (UTC date). They are the rates of
// internal/compare/prices.json; re-copy them from the page and move this date together.
const ShippedCopiedOn = "2026-11-15"

// Shipped returns the table that comes with the product.
func Shipped() Table {
	return Table{
		SourceURL: ShippedSourceURL,
		CopiedOn:  ShippedCopiedOn,
		Rates: []Rate{
			{OS: "linux", Size: "arm", PerMinute: 0.005},
			{OS: "linux", Size: SizeStandard, PerMinute: 0.008},
			{OS: "windows", Size: SizeStandard, PerMinute: 0.016},
			{OS: "macos", Size: SizeStandard, PerMinute: 0.08},
		},
		MachineSizes: map[string]string{},
	}
}

// Validate refuses a table the report cannot use and returns it in a stable order.
func (t Table) Validate() (Table, error) {
	if len(t.Rates) == 0 {
		return t, errors.New("the rate table needs at least one rate")
	}
	seen := map[string]bool{}
	out := t
	out.Rates = append([]Rate(nil), t.Rates...)
	for i, r := range out.Rates {
		r.OS, r.Size = strings.ToLower(strings.TrimSpace(r.OS)), strings.ToLower(strings.TrimSpace(r.Size))
		if r.OS != "linux" && r.OS != "windows" && r.OS != "macos" {
			return t, fmt.Errorf("rate %d: the system must be linux, windows or macos", i+1)
		}
		if r.Size == "" {
			return t, fmt.Errorf("rate %d: the size is empty", i+1)
		}
		if math.IsNaN(r.PerMinute) || math.IsInf(r.PerMinute, 0) || r.PerMinute < 0 || r.PerMinute > 10 {
			return t, fmt.Errorf("rate %d: the price per minute must be between 0 and 10 dollars", i+1)
		}
		if seen[r.OS+"/"+r.Size] {
			return t, fmt.Errorf("rate %d: %s %s is listed twice", i+1, r.OS, r.Size)
		}
		seen[r.OS+"/"+r.Size] = true
		out.Rates[i] = r
	}
	sort.Slice(out.Rates, func(a, b int) bool {
		if out.Rates[a].OS != out.Rates[b].OS {
			return out.Rates[a].OS < out.Rates[b].OS
		}
		return out.Rates[a].Size < out.Rates[b].Size
	})
	out.MachineSizes = map[string]string{}
	for m, sz := range t.MachineSizes {
		sz = strings.ToLower(strings.TrimSpace(sz))
		if m == "" || sz == "" {
			continue
		}
		known := false
		for _, r := range out.Rates {
			if r.Size == sz {
				known = true
			}
		}
		if !known {
			return t, fmt.Errorf("machine %s: no rate has the size %q", m, sz)
		}
		out.MachineSizes[m] = sz
	}
	return out, nil
}

// PerMinute is the price of a job on os, on a machine of the given name. The bool is false when the
// table has no rate for it, so the report can say the job is not priced.
func (t Table) PerMinute(os, machine string) (float64, bool) {
	size := SizeStandard
	if s, ok := t.MachineSizes[machine]; ok {
		size = s
	}
	for _, r := range t.Rates {
		if r.OS == os && r.Size == size {
			return r.PerMinute, true
		}
	}
	for _, r := range t.Rates {
		if r.OS == os && r.Size == SizeStandard {
			return r.PerMinute, true
		}
	}
	return 0, false
}

// Rates reads and changes the stored table.
type Rates struct {
	DB  *store.Store
	Now func() time.Time
}

// Get returns the owner's table, or the shipped one when none is stored.
func (r *Rates) Get(ctx context.Context) (Table, error) {
	raw, ok, err := r.DB.RawSetting(ctx, SettingKey)
	if err != nil {
		return Table{}, err
	}
	if !ok {
		return Shipped(), nil
	}
	var t Table
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return Shipped(), nil
	}
	if t, err = t.Validate(); err != nil {
		return Shipped(), nil
	}
	return t, nil
}

// Set stores a changed table and writes the audit row in the same transaction (R5.17, observability R5.25).
// The source URL and date stay those of the shipped table: they say where the shipped rates came from.
func (r *Rates) Set(ctx context.Context, actor audit.Actor, in Table) (Table, error) {
	t, err := in.Validate()
	if err != nil {
		return Table{}, err
	}
	s := Shipped()
	t.SourceURL, t.CopiedOn, t.Changed = s.SourceURL, s.CopiedOn, true
	return t, r.write(ctx, actor, "savings.rates.change", t, fmt.Sprintf("rate table changed (%d rates)", len(t.Rates)))
}

// Reset restores the shipped table (R5.17).
func (r *Rates) Reset(ctx context.Context, actor audit.Actor) (Table, error) {
	t := Shipped()
	return t, r.write(ctx, actor, "savings.rates.reset", t, "rate table reset to the shipped rates")
}

func (r *Rates) write(ctx context.Context, actor audit.Actor, action string, t Table, summary string) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return r.DB.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.SetSetting(SettingKey, string(b), actor.Login); err != nil {
			return err
		}
		return audit.RecordTx(tx, actor, action, audit.Target{Type: "settings", ID: SettingKey}, map[string]any{"summary": summary})
	})
}
