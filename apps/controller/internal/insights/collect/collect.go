// Package collect fetches the CI data insights read (docs/features/ci-run-insights R5.2, R5.3, R5.6, R6.1,
// R6.2; owner decision D4): job logs and report artifacts, redacted and stored as files on the owner's
// machine, within a share of the GitHub rate budget. The steps and stale-state reads of the Job page are in
// internal/jobsdata/reconcile; both share the Budget. CIX8 contract: types and interfaces only.
package collect

import (
	"context"
	"io"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/share"
)

// Priority, its values and Budget live in internal/insights/share (a leaf, so reconcile does not import
// this package); they are aliased here so the collector and its callers keep their names.
type Priority = share.Priority

// Priorities, in the order of R6.1.
const (
	PriorityReconcile     = share.PriorityReconcile
	PrioritySteps         = share.PrioritySteps
	PriorityWorkflowFiles = share.PriorityWorkflowFiles
	PriorityArtifacts     = share.PriorityArtifacts
	PriorityLogs          = share.PriorityLogs
	PriorityCommitTrees   = share.PriorityCommitTrees
)

// Budget hands out GitHub REST calls of one installation within the insights share (R6.1).
type Budget = share.Budget

// GitHub is the REST surface the collector needs. Implemented in internal/github (rest_insights.go); every
// call is a background call (github-integration R6.6).
type GitHub interface {
	// JobLog opens the plain-text log of one job (GET /repos/{owner}/{repo}/actions/jobs/{job_id}/logs,
	// following the redirect). size is -1 when unknown. A log GitHub no longer has is ErrGone.
	JobLog(ctx context.Context, installationID int64, owner, repo string, jobID int64) (r io.ReadCloser, size int64, err error)
	// RunArtifacts lists the artifacts of one run attempt (name, id, size, expired).
	RunArtifacts(ctx context.Context, installationID int64, owner, repo string, runID int64) ([]Artifact, error)
	// DownloadArtifact opens the zip of one artifact.
	DownloadArtifact(ctx context.Context, installationID int64, owner, repo string, artifactID int64) (io.ReadCloser, int64, error)
	// CommitTree returns the tree sha of a commit (GET /repos/{owner}/{repo}/git/commits/{sha}), for R5.26.
	CommitTree(ctx context.Context, installationID int64, owner, repo, sha string) (string, error)
}

// ErrGone is returned when GitHub no longer has the log or artifact (404 or 410).
var ErrGone = goneError{}

type goneError struct{}

func (goneError) Error() string { return "github no longer has it" }

// Artifact is one artifact of a run.
type Artifact struct {
	ID      int64
	Name    string
	Size    int64
	Expired bool
}

// Key names one stored file: a job log or one member of a report artifact.
type Key struct {
	RepositoryID int64
	RunID        int64
	RunAttempt   int
	JobID        int64  // set for a job log
	ArtifactID   int64  // set for a report file
	Member       string // the file inside the artifact zip
}

// Stored is what Vault.Put wrote.
type Stored struct {
	Path     string // relative to the data dir
	Bytes    int64  // stored (gzipped) size
	RawBytes int64
	Lines    int
	SHA256   string
}

// Vault stores CI files under <data dir>/ci-data/<repository id>/ (R5.2, R5.3). Every byte passes the
// redactor (internal/redact) first; the runner's env: block keeps names only (R5.4). A redactor error stores
// nothing (spec section 8: closed). Files over the caps are refused with ErrTooLarge.
type Vault interface {
	// PutLog stores one job log, redacted and gzipped.
	PutLog(ctx context.Context, k Key, r io.Reader) (Stored, error)
	// PutReport stores one report file, redacted and gzipped.
	PutReport(ctx context.Context, k Key, r io.Reader) (Stored, error)
	// Open opens a stored file, decompressed, for a parser.
	Open(ctx context.Context, path string) (io.ReadCloser, error)
	// Remove deletes a stored file.
	Remove(ctx context.Context, path string) error
}

// ErrTooLarge is returned for a log over 64 MiB, a report over 50 MiB or a run over 200 MiB of reports.
var ErrTooLarge = tooLargeError{}

type tooLargeError struct{}

func (tooLargeError) Error() string { return "over the size cap" }

// Limits are the settings of R5.5 and R6.2.
type Limits struct {
	Keep                bool          // insights_keep_ci_data
	Days                time.Duration // insights_ci_data_days
	BytesPerRepository  int64         // insights_ci_data_mib_per_repository
	TestResultsDays     time.Duration
	ResourceSamplesDays time.Duration
}

// Collector is the background loop: after a job completes it fetches the log, after a run completes it
// fetches the report artifacts, parses them (insights/tests, insights/guardrails) and records the rows.
type Collector interface {
	// JobCompleted queues the log of one finished job.
	JobCompleted(ctx context.Context, repositoryID, runID int64, runAttempt int, jobID int64)
	// RunCompleted queues the artifacts of one finished run attempt.
	RunCompleted(ctx context.Context, repositoryID, runID int64, runAttempt int)
	// Run works the queue until ctx ends.
	Run(ctx context.Context) error
}

// Retention deletes CI files past their days, then the oldest files of a repository over its size cap, and
// their index rows (R5.6). The hourly retention job (observability R5.32) calls it.
type Retention interface {
	Prune(ctx context.Context, now time.Time, l Limits) (removed int, freed int64, err error)
}
