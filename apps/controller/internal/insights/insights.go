// Package insights is CI run insights (docs/features/ci-run-insights): crsp audits every CI job of the
// repositories it watches and turns what it finds into findings, each with its fix in plain words and the
// minutes a week it would save. This file is the CIX8 contract: the shared types and the interfaces every
// analyser family (one sub-package each) builds against. It holds no behaviour yet.
//
// The flow (spec section 3): the collector (insights/collect) fills the store with CI data; each Analyser
// reads JobSource and its own readers over a Window and returns Findings; the Engine hands them to a
// FindingStore, which upserts the open ones and resolves the ones a full window no longer reports (R5.8).
// No analyser writes pool, machine, runner or repository settings (R6.6): a setup change is an action plan
// (insights/actions) an admin starts after Review.
//
// The store implementation lives in internal/store (insights*.go, queries/insights_*.sql); this package and
// its sub-packages never import internal/store (STACKSPEC 3.2).
package insights

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// ErrNotFound and ErrResolved are what Dismiss returns for an unknown and for a resolved finding.
var (
	ErrNotFound = errors.New("no such finding")
	ErrResolved = errors.New("the finding is resolved")
)

// Kind is the kind of a finding (spec section 7, Kinds and minutes).
type Kind string

// The finding kinds. A subject is the stable key of a finding inside its kind.
const (
	KindWaitHold            Kind = "wait.hold"               // R5.14, subject: job key
	KindHousekeepingLane    Kind = "housekeeping.lane"       // R5.28, subject: job key
	KindPoolSize            Kind = "pool.size"               // R5.17, subject: label set
	KindIdleNextToQueue     Kind = "pool.idle_next_to_queue" // R5.22, subject: idle label set > waiting label set
	KindRepoSlots           Kind = "repo.slots"              // R5.18, subject: repository
	KindRunCriticalPath     Kind = "run.critical_path"       // R5.23, subject: workflow + job + change
	KindRunSuperseded       Kind = "run.superseded"          // R5.24, subject: workflow
	KindRunDuplicateEvent   Kind = "run.duplicate_event"     // R5.25, subject: workflow + events
	KindRunRetestedTree     Kind = "run.retested_tree"       // R5.26, subject: workflow
	KindRunFailFast         Kind = "run.fail_fast"           // R5.27, subject: workflow
	KindWorkflowTimeout     Kind = "workflow.timeout"        // R5.29 G2, subject: workflow file
	KindWorkflowRetry       Kind = "workflow.retry"          // R5.29 G3
	KindWorkflowMatrixAxis  Kind = "workflow.matrix_axis"    // R5.29
	KindWorkflowStepOrder   Kind = "workflow.step_order"     // R5.29
	KindWorkflowCache       Kind = "workflow.cache"          // R5.29
	KindWorkflowSlowTestsPR Kind = "workflow.slow_tests_on_pr"
	KindGuardrailG1         Kind = "guardrail.g1" // R5.31, R5.32, subject: repository
	KindGuardrailG2         Kind = "guardrail.g2"
	KindGuardrailG3         Kind = "guardrail.g3"
	KindGuardrailG4         Kind = "guardrail.g4"
	KindGuardrailG5         Kind = "guardrail.g5"
	KindGuardrailG6         Kind = "guardrail.g6"
	KindGuardrailG7         Kind = "guardrail.g7"
	KindTestSlow            Kind = "test.slow"          // R5.34, subject: test id
	KindTestFlaky           Kind = "test.flaky"         // R5.35
	KindTestDuplicate       Kind = "test.duplicate"     // R5.36
	KindTestNoAssertions    Kind = "test.no_assertions" // R5.37
)

// Scope says whether a finding is about one repository or about the pool.
type Scope string

// Scopes.
const (
	ScopeRepo Scope = "repo"
	ScopePool Scope = "pool"
)

// Area is the screen area a finding belongs to (the InsightArea enum of the contract).
type Area string

// Areas.
const (
	AreaWaits      Area = "waits"
	AreaPool       Area = "pool"
	AreaRuns       Area = "runs"
	AreaWorkflows  Area = "workflows"
	AreaGuardrails Area = "guardrails"
	AreaTests      Area = "tests"
)

// ActionKind is the setup change a finding's fix makes (R5.10); empty when the fix is outside crsp.
type ActionKind string

// Action kinds.
const (
	ActionNone              ActionKind = ""
	ActionMachineSlots      ActionKind = "machine_slots"
	ActionRepoParallelLimit ActionKind = "repo_parallel_limit"
	ActionLightLane         ActionKind = "light_lane"
)

// Guardrail is one of the dev-standards test guardrails "G1" to "G7", or "" for none.
type Guardrail string

// Finding is what an analyser reports (R5.7). Title, Fix and Snippet are plain words shown as text, never
// as markup. Evidence is a JSON object whose keys the kind defines; the finding page shows it. MinutesSavedWeek
// is the estimate of R5.9: the minutes of the last 7 days the fix would have removed, rounded.
type Finding struct {
	Kind             Kind
	Scope            Scope
	RepositoryID     int64  // 0 for the pool scope
	Repository       string // owner/repo, empty for the pool scope
	Subject          string
	Area             Area
	Title            string
	Fix              string
	Snippet          string
	Evidence         json.RawMessage
	MinutesSavedWeek int
	Guardrail        Guardrail
	Action           ActionKind
}

// StoredFinding is a Finding with its store identity and its times (UTC).
type StoredFinding struct {
	Finding
	ID         int64
	FirstSeen  time.Time
	LastSeen   time.Time
	ResolvedAt time.Time // zero while open
	// DismissedUntil is when a dismissed finding shows again (D17); zero when it was never dismissed or the
	// time has passed.
	DismissedUntil time.Time
}

// DismissFor is how long a dismissed finding stays hidden: 7 days, then it comes back if it is still true
// (owner decision D17). The engine keeps refreshing a hidden finding and resolves it when its condition ends,
// so "still true" needs nothing more.
const DismissFor = 7 * 24 * time.Hour

// Dismisser hides one open finding for DismissFor. It is separate from FindingStore on purpose: no store
// method closes or deletes a finding; only a full successful pass resolves one (R5.8).
type Dismisser interface {
	// Dismiss hides the finding until now + DismissFor and returns that time. ErrNotFound when no such finding,
	// ErrResolved when it is resolved already.
	Dismiss(ctx context.Context, id int64, now time.Time) (until time.Time, err error)
}

// Window is the half-open time range [From, To) an analyser pass reads, in UTC.
type Window struct {
	From, To time.Time
}

// Origin says who ran a job (R5.39).
type Origin string

// Origins. OriginUnknown is a job not placed yet, or one GitHub has not said.
const (
	OriginUnknown Origin = ""
	OriginCrsp    Origin = "crsp"
	OriginOther   Origin = "other"
	OriginHosted  Origin = "hosted"
)

// JobFact is the read-only view of one job attempt every analyser shares, taken from the jobs and runs
// tables (observability R5.35). A zero time means unknown (observability R5.9), never "at the epoch".
// QueuedAt and SlotAt are set only for jobs that went through crsp's slot queue.
type JobFact struct {
	ID            int64 // the GitHub job id
	RepositoryID  int64
	Repository    string
	RunID         int64
	RunAttempt    int
	Workflow      string // the workflow file path
	JobIdent      string // repository + workflow + job name with matrix values
	JobName       string
	NodeID        string // the job's id inside its run, used by needs edges
	Branch        string
	HeadSHA       string
	Event         string // push, pull_request, schedule, ...
	DefaultBranch bool   // the run's branch is the repository's default branch
	PullRequest   int
	Labels        []string // runs-on labels the job asked for
	Origin        Origin
	RunnerName    string
	RunnerGroup   string
	RunnerLabels  []string
	MachineID     string
	Light         bool // the scheduler's light class
	Result        string
	WaitReason    string // observability R5.5 wait reason code, the last one seen
	CreatedAt     time.Time
	QueuedAt      time.Time
	SlotAt        time.Time
	StartedAt     time.Time
	CompletedAt   time.Time
}

// JobSource reads job facts. Implemented over internal/store.
type JobSource interface {
	// JobsBetween returns every job attempt created in w, of one repository (repositoryID > 0) or of all
	// (0), oldest first. Jobs of every runner origin are included (R5.1).
	JobsBetween(ctx context.Context, w Window, repositoryID int64) ([]JobFact, error)
}

// Analyser is one analyser family's pass. Kinds lists every kind it owns: after a successful pass over a
// full window, the engine resolves the open findings of those kinds that the pass did not return (R5.8). A
// pass that returns an error resolves nothing (R6.8). An analyser never writes settings (R6.6).
type Analyser interface {
	// Name is a stable name for logs and insight_analyser_runs, such as "waits".
	Name() string
	// Kinds are the finding kinds this analyser owns.
	Kinds() []Kind
	// Analyse reads the window and returns every finding whose condition holds in it. Output order does not
	// matter; the store keys findings by (kind, scope, repository, subject).
	Analyse(ctx context.Context, w Window) ([]Finding, error)
}

// SyncResult says what one Sync did.
type SyncResult struct {
	Opened, Updated, Resolved int
}

// Filter selects findings for the Do next list (R5.11). Zero values mean "any".
type Filter struct {
	RepositoryID int64
	Area         Area
	Guardrail    Guardrail
	Resolved     bool // false: open findings; true: resolved ones
	Limit        int
	Cursor       string
}

// Summary is what the Overview shows (R5.11, UX-52).
type Summary struct {
	Open             int
	MinutesSavedWeek int
	EnoughData       bool // false under 50 finished jobs in the window
}

// FindingStore keeps findings. Implemented over internal/store (queries/insights_findings.sql).
type FindingStore interface {
	// Sync records one successful pass of an analyser that owns kinds: each found finding is opened (new
	// key) or updated (last_seen = now, text, evidence and minutes refreshed); each open finding of those
	// kinds that is not in found is resolved at now. found holds only findings of those kinds.
	Sync(ctx context.Context, kinds []Kind, found []Finding, now time.Time) (SyncResult, error)
	// List returns one page, ranked by minutes saved a week, then first seen, then id (R5.11), and the next
	// cursor ("" on the last page).
	List(ctx context.Context, f Filter) (items []StoredFinding, total int, next string, err error)
	// Get returns one finding by id, open or resolved.
	Get(ctx context.Context, id int64) (StoredFinding, error)
	// Summary counts the open findings and sums their minutes.
	Summary(ctx context.Context) (Summary, error)
}

// RunLog records analyser passes in insight_analyser_runs (R6.3, R6.8).
type RunLog interface {
	// Begin records the start of a pass and returns its id.
	Begin(ctx context.Context, analyser string, w Window, at time.Time) (int64, error)
	// End records how the pass ended; ok is true only for a full, successful window.
	End(ctx context.Context, id int64, at time.Time, ok bool, found, resolved int, errText string) error
}

// Engine runs the analysers every 15 minutes, one at a time, over the last 7 days (R6.3). Implemented in this
// package by the CIX engine task.
type Engine interface {
	// RunOnce runs every analyser once over the window ending at now and syncs its findings.
	RunOnce(ctx context.Context, now time.Time) error
	// Run calls RunOnce on its schedule until ctx ends.
	Run(ctx context.Context) error
}
