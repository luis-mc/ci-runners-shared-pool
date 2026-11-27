package collect

import (
	"context"
	"sync"
	"time"
)

// Share of the installation's hourly limit the insights may use (R6.1).
const Share = 0.25

// DefaultHourlyLimit is the limit assumed for an installation GitHub has not told us about yet.
const DefaultHourlyLimit = 5000

// ceilings is how much of the share each priority may use: a lower priority stops earlier and so leaves
// room for the work before it (R6.1: reconcile, steps, workflow files, run artifacts, logs, commit trees).
var ceilings = map[Priority]float64{
	PriorityReconcile:     1.00,
	PrioritySteps:         0.90,
	PriorityWorkflowFiles: 0.80,
	PriorityArtifacts:     0.65,
	PriorityLogs:          0.50,
	PriorityCommitTrees:   0.35,
}

// HourlyBudget is the Budget over clock hours. It never grants more than Share of an installation's limit
// in one hour, and nothing while the installation is past the half-limit stop of github-integration R6.6.
// Webhook-driven and user-driven calls do not pass through it, so they are never held back.
type HourlyBudget struct {
	// Now is the clock.
	Now func() time.Time
	// Limit is the installation's hourly limit; DefaultHourlyLimit when nil or not positive.
	Limit func(installationID int64) int
	// Halted says the installation is at the half-limit stop (github-integration R6.6).
	Halted func(installationID int64) bool

	mu      sync.Mutex
	hour    time.Time
	used    map[int64]int
	backlog int
}

var _ Budget = (*HourlyBudget)(nil)

func (b *HourlyBudget) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

// Take reserves n calls. When the share (or the ceiling of p) is spent it returns false and the start of the
// next hour.
func (b *HourlyBudget) Take(_ context.Context, installationID int64, p Priority, n int) (bool, time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now().UTC()
	hour := now.Truncate(time.Hour)
	if !hour.Equal(b.hour) {
		b.hour, b.used = hour, map[int64]int{}
	}
	until := hour.Add(time.Hour)
	if n <= 0 {
		return true, time.Time{}
	}
	if b.Halted != nil && b.Halted(installationID) {
		return false, until
	}
	limit := DefaultHourlyLimit
	if b.Limit != nil {
		if l := b.Limit(installationID); l > 0 {
			limit = l
		}
	}
	c, ok := ceilings[p]
	if !ok {
		c = ceilings[PriorityCommitTrees]
	}
	allowed := int(float64(limit) * Share * c)
	if b.used[installationID]+n > allowed {
		return false, until
	}
	b.used[installationID] += n
	return true, time.Time{}
}

// Used is the number of calls granted to an installation in the current hour.
func (b *HourlyBudget) Used(installationID int64) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.now().UTC().Truncate(time.Hour).Equal(b.hour) {
		return 0
	}
	return b.used[installationID]
}

// SetBacklog records how many jobs wait for the share; the collector and reconcile call it with the length of
// their queues.
func (b *HourlyBudget) SetBacklog(jobs int) {
	b.mu.Lock()
	b.backlog = jobs
	b.mu.Unlock()
}

// Backlog is the number of jobs waiting for the share, and when the share frees up.
func (b *HourlyBudget) Backlog() (int, time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.backlog, b.now().UTC().Truncate(time.Hour).Add(time.Hour)
}
