// Package workflows checks workflow files (docs/features/ci-run-insights R5.29, R5.30): G2 timeout-minutes,
// test retries (G3), new matrix axes, step order, missing caches and slow test kinds on pull requests (G6).
// The files are the ones crsp already fetches at a head commit for needs (github-integration R5.99). CIX8
// contract: types and interfaces only.
package workflows

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

// CheckID names one check; it is also the finding kind and the doctor check ID.
type CheckID string

// The checks of R5.29.
const (
	CheckTimeout       CheckID = CheckID(insights.KindWorkflowTimeout)
	CheckRetry         CheckID = CheckID(insights.KindWorkflowRetry)
	CheckMatrixAxis    CheckID = CheckID(insights.KindWorkflowMatrixAxis)
	CheckStepOrder     CheckID = CheckID(insights.KindWorkflowStepOrder)
	CheckCache         CheckID = CheckID(insights.KindWorkflowCache)
	CheckSlowTestsOnPR CheckID = CheckID(insights.KindWorkflowSlowTestsPR)
)

// Status is a check's result.
type Status string

// Statuses.
const (
	OK   Status = "ok"
	Fail Status = "fail"
	Skip Status = "skip"
)

// File is one workflow file at one commit.
type File struct {
	RepositoryID  int64
	HeadSHA       string
	Path          string // .github/workflows/ci.yml
	Content       []byte // nil when it could not be read; every check is then Skip with Reason
	Reason        string
	DefaultBranch bool
}

// Result is one check of one job (Job empty for a check of the whole file).
type Result struct {
	Check     CheckID
	Job       string
	Status    Status
	Line      int
	Detail    string // plain words, with the fix
	Guardrail insights.Guardrail
}

// Checker runs every check on one file. previous is the same file at the last default-branch commit
// checked, for CheckMatrixAxis (nil when none). Pure: no I/O.
type Checker interface {
	Check(f File, previous *File) []Result
}

// Store keeps results per commit in workflow_checks.
type Store interface {
	Save(ctx context.Context, f File, results []Result) error
	// Latest returns the results of the latest default-branch commit checked (listWorkflowChecks).
	Latest(ctx context.Context, repositoryID int64) (headSHA string, results []Result, err error)
	// ForCommit returns the results of one commit.
	ForCommit(ctx context.Context, repositoryID int64, headSHA string) ([]Result, error)
}

// The workflows analyser implements insights.Analyser with these kinds: one finding per repository and
// failing check of its latest default-branch commit (R5.30).
var Kinds = []insights.Kind{
	insights.KindWorkflowTimeout, insights.KindWorkflowRetry, insights.KindWorkflowMatrixAxis,
	insights.KindWorkflowStepOrder, insights.KindWorkflowCache, insights.KindWorkflowSlowTestsPR,
}
