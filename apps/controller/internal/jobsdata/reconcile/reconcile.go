// Package reconcile fixes the Job page faults (docs/features/ci-run-insights R5.39 to R5.41): it records who
// ran a job (a crsp runner, another runner by name, group and labels, or a GitHub-hosted runner), reads the
// steps of jobs that came only from the poller, and re-reads job rows that look stale so a job GitHub
// completed never stays "queued". Every GitHub call is a background call within the insights share (R6.1).
// CIX8 contract: types and interfaces only.
package reconcile

import (
	"context"
	"time"
)

// Origin is who ran a job.
type Origin string

// Origins (jobs.runner_origin). Empty means not known yet.
const (
	Unknown Origin = ""
	Crsp    Origin = "crsp"
	Other   Origin = "other"
	Hosted  Origin = "hosted"
)

// HostedGroup is the runner group name GitHub gives its hosted runners.
const HostedGroup = "GitHub Actions"

// RemoteStep is one step as GitHub's REST jobs API returns it.
type RemoteStep struct {
	Number      int
	Name        string
	Status      string
	Conclusion  string
	StartedAt   time.Time
	CompletedAt time.Time
}

// RemoteJob is one job as GitHub's REST jobs API returns it.
type RemoteJob struct {
	ID              int64
	RunID           int64
	RunAttempt      int
	Status          string // queued, in_progress, completed, waiting, ...
	Conclusion      string
	RunnerName      string
	RunnerGroupName string
	Labels          []string // the labels of the runner that took it, or the runs-on labels while queued
	CreatedAt       time.Time
	StartedAt       time.Time
	CompletedAt     time.Time
	Steps           []RemoteStep
}

// GitHub is the REST surface reconcile needs. Implemented in internal/github (rest_insights.go). A job or run
// GitHub no longer has returns ErrGone.
type GitHub interface {
	// Job reads one job (GET /repos/{owner}/{repo}/actions/jobs/{job_id}).
	Job(ctx context.Context, installationID int64, owner, repo string, jobID int64) (RemoteJob, error)
	// RunJobs reads every job of one run attempt
	// (GET /repos/{owner}/{repo}/actions/runs/{run_id}/attempts/{attempt}/jobs, all pages).
	RunJobs(ctx context.Context, installationID int64, owner, repo string, runID int64, attempt int) ([]RemoteJob, error)
}

// ErrGone is returned when GitHub no longer has the job or run.
var ErrGone = goneError{}

type goneError struct{}

func (goneError) Error() string { return "github no longer has it" }

// Runners says whether a runner name belongs to crsp (its runner registry).
type Runners interface {
	IsCrsp(ctx context.Context, runnerName string) (bool, error)
}

// Classify gives a job's origin from GitHub's runner fields and crsp's registry (R5.39): a runner crsp made
// is Crsp, the group HostedGroup is Hosted, any other named runner is Other, no runner yet is Unknown.
type Classify interface {
	Origin(ctx context.Context, runnerName, runnerGroup string) (Origin, error)
}

// StaleRun is one run attempt with job rows in queued or running that look stale.
type StaleRun struct {
	RepositoryID int64
	Repository   string // owner/repo
	RunID        int64
	RunAttempt   int
	OldestUpdate time.Time
}

// Store is what reconcile reads and writes in the jobs tables.
type Store interface {
	// StaleRuns lists run attempts with queued or running jobs not updated since before, and not checked
	// since checkedBefore, oldest first, up to limit (R5.41: 100 a pass).
	StaleRuns(ctx context.Context, before, checkedBefore time.Time, limit int) ([]StaleRun, error)
	// Apply merges what GitHub said about one job: state, result, times, runner origin, group and labels,
	// steps (with steps_source "rest") and checked_at. A job GitHub no longer has gets the result lost with
	// the reason. State never goes backward (the merge rules of internal/store jobsdata).
	Apply(ctx context.Context, repositoryID int64, j RemoteJob, origin Origin, checkedAt time.Time) error
	// MarkLost sets one job done with the result lost and the reason.
	MarkLost(ctx context.Context, jobID int64, reason string, at time.Time) error
}

// Reconciler is the loop of R5.40 and R5.41.
type Reconciler interface {
	// Pass re-reads the stale runs once (every 5 min: rows with no update for 10 min).
	Pass(ctx context.Context, now time.Time) (checked int, err error)
	// JobCompleted reads the steps of a job whose steps did not come from webhooks, once.
	JobCompleted(ctx context.Context, repositoryID, jobID int64) error
	// Watch reads a running job's steps at most every 60 s while its Job page is open.
	Watch(ctx context.Context, repositoryID, jobID int64) error
}
