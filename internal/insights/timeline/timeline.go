// Package timeline is the cross-project timeline (docs/features/ci-run-insights R5.21) and the idle runner
// next to a queue finding (R5.22). CIX8 contract: types and interfaces only.
package timeline

import (
	"context"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/insights"
)

// Marker flags a segment (R5.21).
type Marker string

// Markers.
const (
	// IdleWhileWaiting: an online crsp runner sat idle while a job it could have taken by labels waited.
	IdleWhileWaiting Marker = "idle_while_waiting"
	// AfterMergeWork: a push run on the default branch held a slot while a pull request job waited.
	AfterMergeWork Marker = "after_merge_work"
)

// SegmentKind is a job on the lane or an idle gap.
type SegmentKind string

// Segment kinds.
const (
	SegmentJob  SegmentKind = "job"
	SegmentIdle SegmentKind = "idle"
	// SegmentWait is the time a follow-up job (a job of a workflow_run run, such as Installers after CI) waited for
	// its runner, drawn on the lane of the runner that took it (CIX24, R5.130).
	SegmentWait SegmentKind = "wait"
)

// Segment is one span on a lane.
type Segment struct {
	Kind          SegmentKind
	Start, End    time.Time
	JobID         int64
	RunID         int64
	FollowUp      bool // the job belongs to a run another run started (event workflow_run)
	Repository    string
	JobIdent      string
	Event         string
	Markers       []Marker
	WaitingJobIDs []int64
}

// Lane is one runner: a crsp runner with its machine, or another runner by name.
type Lane struct {
	RunnerName  string
	Origin      insights.Origin
	MachineName string
	Labels      []string
	Segments    []Segment
}

// Builder answers getPoolTimeline: lanes for a range of at most 24 hours, sorted by origin then name.
// Idle gaps are known only for crsp runners (from runner states); other runners have job segments only.
type Builder interface {
	Timeline(ctx context.Context, w insights.Window) ([]Lane, error)
}

// The timeline analyser implements insights.Analyser with KindIdleNextToQueue: idle for 2 min or more while
// jobs for another OS or label set waited, adding up to 15 min or more of waiting a week (R5.22).
var Kinds = []insights.Kind{insights.KindIdleNextToQueue}
