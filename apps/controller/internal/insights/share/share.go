// Package share holds the GitHub rate share of the insights work (ci-run-insights R6.1): the Budget and its
// priorities. It is a leaf package so that internal/jobsdata/reconcile and internal/insights/collect can both
// use it without an import cycle (collect reaches the store and the GitHub client, which hold reconcile's types).
package share

import (
	"context"
	"time"
)

// Priority orders background GitHub work inside the insights share (R6.1): a lower value goes first.
type Priority int

// Priorities, in the order of R6.1.
const (
	PriorityReconcile Priority = iota
	PrioritySteps
	PriorityWorkflowFiles
	PriorityArtifacts
	PriorityLogs
	PriorityCommitTrees
)

// Budget hands out GitHub REST calls of one installation within the insights share: at most 25 percent of
// the hourly limit, and never past the half-limit stop of github-integration R6.6 (R6.1).
type Budget interface {
	// Take reserves n calls at priority p. It returns ok false and the time the share frees up when the
	// share is spent; the caller queues the work and the screens say how much waits (InsightBacklog).
	Take(ctx context.Context, installationID int64, p Priority, n int) (ok bool, until time.Time)
	// Backlog is the number of jobs whose collection waits for the share, and until when.
	Backlog() (jobs int, until time.Time)
}
