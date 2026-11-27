package assign

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// Persistent runners are registered with self-hosted and the OS only; their
// routing labels are custom labels added and removed through the runner labels
// API (runners R5.117). T7 will drive it; the calls sit here behind an
// interface now. GitHub allows 100 labels per runner, read-only ones included,
// and fails the 101st add with HTTP 500. A secondary rate limit was hit by bulk
// writes, so nothing is written unless the set changed, and only a few labels
// at a time.

// LabelBatch is how many labels one Sync writes; the rest wait for the next call.
const LabelBatch = 5

// RunnerLabels is the runner labels API (github.RunnerLabeler).
type RunnerLabels interface {
	AddRunnerLabels(ctx context.Context, installationID int64, scope github.RunnerScope, runnerID int64, labels []string) error
	RemoveRunnerLabel(ctx context.Context, installationID int64, scope github.RunnerScope, runnerID int64, label string) error
}

// LabelPlan is what one Sync does.
type LabelPlan struct {
	Add, Remove []string
	// Deferred counts the changes left for a later call (the batch limit).
	Deferred int
	// Refused lists the labels not added because the runner is at its cap.
	Refused []string
}

// Empty is true when nothing is written.
func (p LabelPlan) Empty() bool { return len(p.Add) == 0 && len(p.Remove) == 0 }

// PlanLabels compares the custom labels a runner has with those it should have.
// readOnly counts against the cap (the labels made at registration). Removals go
// first, so room is made before labels are added; at most LabelBatch changes are
// planned.
func PlanLabels(have, want []string, readOnly int) LabelPlan {
	h, w := map[string]bool{}, map[string]bool{}
	for _, l := range have {
		h[strings.ToLower(l)] = true
	}
	for _, l := range want {
		w[strings.ToLower(l)] = true
	}
	var add, remove []string
	for l := range w {
		if !h[l] {
			add = append(add, l)
		}
	}
	for l := range h {
		if !w[l] {
			remove = append(remove, l)
		}
	}
	sort.Strings(add)
	sort.Strings(remove)
	var p LabelPlan
	budget := LabelBatch
	for _, l := range remove {
		if budget == 0 {
			p.Deferred++
			continue
		}
		p.Remove = append(p.Remove, l)
		budget--
	}
	room := runners.MaxLabels - readOnly - len(have) + len(p.Remove)
	for _, l := range add {
		switch {
		case room <= 0:
			p.Refused = append(p.Refused, l)
		case budget == 0:
			p.Deferred++
		default:
			p.Add = append(p.Add, l)
			room--
			budget--
		}
	}
	return p
}

// SyncLabels writes a plan: it writes only on a change.
func SyncLabels(ctx context.Context, api RunnerLabels, installationID int64, scope github.RunnerScope, runnerID int64, p LabelPlan) error {
	for _, l := range p.Remove {
		if err := api.RemoveRunnerLabel(ctx, installationID, scope, runnerID, l); err != nil {
			return fmt.Errorf("remove label %s: %w", l, err)
		}
	}
	if len(p.Add) > 0 {
		if err := api.AddRunnerLabels(ctx, installationID, scope, runnerID, p.Add); err != nil {
			return fmt.Errorf("add labels: %w", err)
		}
	}
	return nil
}
