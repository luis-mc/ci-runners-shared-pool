// Package runs analyses whole runs (docs/features/ci-run-insights): the critical path with savings (R5.23),
// superseded runs, two events on one head and re-tested trees (R5.24 to R5.26), and fail-fast order (R5.27).
// CIX8 contract: types and interfaces only.
package runs

import (
	"context"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

// Change is the what-if of a saving (R5.23).
type Change string

// Changes.
const (
	NoWait          Change = "no_wait"           // the job had not waited or held a slot
	MedianRun       Change = "median_run"        // the job had run at the median of its job key
	StartAfterNeeds Change = "start_after_needs" // the job had started as soon as its needs ended
)

// Saving is one way the run could have ended sooner.
type Saving struct {
	Job    string // the job's node id
	Change Change
	Sooner time.Duration
	Words  string // "The run would end {n} min sooner if {change}."
}

// WasteKind names a run that wasted runner time (R5.24 to R5.27).
type WasteKind string

// Waste kinds.
const (
	Superseded     WasteKind = "superseded"
	DuplicateEvent WasteKind = "duplicate_event"
	RetestedTree   WasteKind = "retested_tree"
	FailFast       WasteKind = "fail_fast"
)

// Waste is runner time a run spent for nothing.
type Waste struct {
	Kind    WasteKind
	Minutes float64
	Words   string
}

// Graph is one finished run attempt: its jobs (as insights.JobFact) and its needs edges by node id.
type Graph struct {
	Jobs  []insights.JobFact
	Needs map[string][]string
	// Medians is the median run time per job key (observability R5.40 sample), for MedianRun.
	Medians map[string]time.Duration
}

// Savings computes the top 3 savings of one run over the critical path with real times. Pure.
type Savings interface {
	Top(g Graph) []Saving
}

// TreeReader returns the tree sha of a commit, cached per commit; it may say ok false while the GitHub share
// is spent (R5.26, R6.1).
type TreeReader interface {
	Tree(ctx context.Context, repositoryID int64, sha string) (tree string, ok bool, err error)
}

// Reader answers getRunSavings for one run attempt.
type Reader interface {
	RunSavings(ctx context.Context, runID int64, attempt int) (savings []Saving, waste []Waste, reason string, err error)
}

// The runs analyser implements insights.Analyser with these kinds.
var Kinds = []insights.Kind{
	insights.KindRunCriticalPath, insights.KindRunSuperseded, insights.KindRunDuplicateEvent,
	insights.KindRunRetestedTree, insights.KindRunFailFast,
}
