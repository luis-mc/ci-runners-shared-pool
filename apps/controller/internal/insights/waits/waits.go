// Package waits is the wait, hold and run split of every job (docs/features/ci-run-insights R5.12 to R5.14)
// and the housekeeping lane advice (R5.28). CIX8 contract: types and interfaces only.
package waits

import (
	"context"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

// Split is one job's time in three parts (R5.12). A part that cannot be known has Known false and is never
// counted as zero (observability R5.9).
type Split struct {
	Wait, Hold, Run                time.Duration
	WaitKnown, HoldKnown, RunKnown bool
	// GitHubWait is created_at to queued_at, the part of Wait before crsp's slot queue (crsp jobs only).
	GitHubWait time.Duration
}

// Splitter computes the split. For a crsp job: wait = created_at to slot_at, hold = slot_at to started_at,
// run = started_at to completed_at. For another runner's job: wait = created_at to started_at, no hold.
// Pure: no clock, no I/O.
type Splitter interface {
	Split(j insights.JobFact) Split
}

// HoldReason is why a hold went over the cap (R5.14).
type HoldReason string

// Hold reasons, read from the runner set-up log and the wait reasons.
const (
	HoldImagePull    HoldReason = "image_pull"
	HoldRunnerStart  HoldReason = "runner_start"
	HoldRegistration HoldReason = "registration"
	HoldLoad         HoldReason = "load"
	HoldUnknown      HoldReason = "unknown"
)

// HoldExplainer names the reason of one long hold from what crsp logged for that job.
type HoldExplainer interface {
	Explain(ctx context.Context, j insights.JobFact) (HoldReason, error)
}

// Stats is count, total, median, p90 (the slowest 10%) and p95 of one part over known values.
type Stats struct {
	Known         int
	Total         time.Duration
	P50, P90, P95 time.Duration
}

// Group is how getWaitSplit groups rows (R5.13).
type Group string

// Groups.
const (
	ByRepository Group = "repository"
	ByJobKey     Group = "job_key"
	ByLabel      Group = "label"
	ByDay        Group = "day"
)

// Row is one group of the split.
type Row struct {
	Key             string
	Jobs            int
	Wait, Hold, Run Stats
	HoldsOverCap    int
	HoldReasons     map[HoldReason]int
}

// Reader answers getWaitSplit.
type Reader interface {
	// SplitBy groups the jobs of the last days (7, 14 or 30) by g, of one repository or all (0).
	SplitBy(ctx context.Context, days int, g Group, repositoryID int64, holdCap time.Duration) ([]Row, error)
}

// The waits analyser implements insights.Analyser with the kinds below. Holds over the cap that add up to
// 10 min or more a week per job key give KindWaitHold; job keys with a median run under 1 min and a median
// wait over 2 min give KindHousekeepingLane (action light_lane when a machine has room for a light slot).
var Kinds = []insights.Kind{insights.KindWaitHold, insights.KindHousekeepingLane}
