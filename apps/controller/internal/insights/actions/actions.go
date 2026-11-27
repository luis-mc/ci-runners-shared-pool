// Package actions turns a finding whose fix changes the crsp setup into a plan the owner reviews and an admin
// starts (docs/features/ci-run-insights R5.10, R6.6; owner decision D4 of the spec): Review, then the work,
// then Done (design system UX-30). Nothing changes before Start; every change writes an audit row; a change
// that lowers slots names the running jobs it waits for (UX-62). No auto-resize. CIX8 contract: types and
// interfaces only.
package actions

import (
	"context"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

// State is a plan's state.
type State string

// States.
const (
	Draft   State = "draft"
	Running State = "running"
	Done    State = "done"
	Failed  State = "failed"
	Stopped State = "stopped"
)

// Change is one setting a plan changes.
type Change struct {
	Target        string // machine id or repository (owner/repo)
	TargetName    string
	Field         string // job_slots, light_slots or max_parallel_jobs
	From, To      int
	Words         string
	JobsWaitedFor []string
}

// ChangeResult is what happened to one change.
type ChangeResult struct {
	Target, Field string
	State         string // pending, done, failed, skipped
	Error         string
}

// Plan is the Review data and, once started, the progress.
type Plan struct {
	ID         string
	FindingID  int64
	Action     insights.ActionKind
	State      State
	Changes    []Change
	Results    []ChangeResult
	Estimate   string
	CreatedBy  string
	CreatedAt  time.Time
	StartedAt  time.Time
	FinishedAt time.Time
}

// Planner makes and runs plans.
type Planner interface {
	// Plan makes a draft from an open finding with an action (createInsightPlan); it changes nothing. It
	// returns ErrNoAction for a finding without an action or a resolved one.
	Plan(ctx context.Context, findingID int64, by string) (Plan, error)
	// Get returns a plan with its progress (getInsightPlan).
	Get(ctx context.Context, id string) (Plan, error)
	// Start runs a draft plan (startInsightPlan): each change through the owning feature's own setter
	// (machines pool size, scheduling repository limit or light slots), each with its audit row.
	Start(ctx context.Context, id string, by string) (Plan, error)
}

// ErrNoAction is returned for a finding that has no action or is resolved (the API answers 409).
var ErrNoAction = noActionError{}

type noActionError struct{}

func (noActionError) Error() string { return "the finding has no setup change to make" }

// Setter is one feature's setter a plan calls; each implementation writes its own audit row in the same
// transaction as the change (observability R5.25).
type Setter interface {
	Apply(ctx context.Context, c Change, by string) error
}
