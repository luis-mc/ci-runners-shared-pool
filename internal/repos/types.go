// Package repos is the pool switch, repository policy, the CRSP_RUNS_ON_*
// variables and the workflow runs-on check (github-integration R5.17 to
// R5.30). It reads and writes data through the Store interface declared here
// and reaches GitHub through the small GitHub interface, so it is tested with
// the fake GitHub server (R6.9).
package repos

import (
	"context"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
)

// Repository is one repository of an installation, with its pool state.
type Repository struct {
	ID             int64
	InstallationID int64
	FullName       string
	Owner          string
	Name           string
	Visibility     string
	DefaultBranch  string
	Archived       bool
	PoolOn         bool
	PoolChangedAt  time.Time
	PoolChangedBy  string
}

// ChangedByInstallation is the PoolChangedBy of a repository the reconcile
// turned off because it left the App's installation. The console reads it as
// the reason (github-integration R5.22).
const ChangedByInstallation = "installation"

// LeftInstallation is true when the pool of the repository is off because it
// left the App's installation.
func (r Repository) LeftInstallation() bool {
	return !r.PoolOn && r.PoolChangedBy == ChangedByInstallation
}

// Policy is a repository's policy (R5.25).
type Policy struct {
	Priority        int
	MaxParallelJobs int
	AllowForkPRs    bool
	Labels          []string
	RunnerGroups    []string
	BudgetMemoryPct int // 0 means not set
	BudgetCPUPct    int // 0 means not set
	// ExtraPackages are the extra distribution packages of the pool image,
	// sorted, without repeats (runners R5.51). Empty adds no layer.
	ExtraPackages []string
	// HostedFallback runs the repository's jobs on GitHub-hosted runners while
	// no crsp machine can take them. Off by default: jobs wait for a crsp
	// machine, which costs nothing (github-integration R5.42).
	HostedFallback bool
	// FallbackCapMinutes caps the fallback minutes of a calendar month (UTC).
	// 0 means no cap.
	FallbackCapMinutes int
	// HostedLabels is the hosted label per OS ("linux", "windows", "macos");
	// a missing OS uses DefaultHostedLabels.
	HostedLabels map[string]string
	// RunnerKinds is the owner's choice of which runners take the repository's
	// jobs: RunnerKindsEphemeralOnly or RunnerKindsAnyRunner. Empty means the
	// owner did not choose and the default of the visibility applies
	// (github-integration R5.60). Read it with EffectiveRunnerKinds.
	RunnerKinds string
	UpdatedAt   time.Time
	UpdatedBy   string
}

// Runner kinds a repository's jobs may use (github-integration R5.60).
const (
	RunnerKindsEphemeralOnly = "ephemeral_only"
	RunnerKindsAnyRunner     = "any_runner"
)

// DefaultRunnerKinds is the kind a repository gets while the owner has not
// chosen: a public repository runs on ephemeral runners only, a private or
// internal one may use any runner.
func DefaultRunnerKinds(visibility string) string {
	if visibility == "public" {
		return RunnerKindsEphemeralOnly
	}
	return RunnerKindsAnyRunner
}

// EffectiveRunnerKinds is the owner's choice, or the default of the visibility.
func (p Policy) EffectiveRunnerKinds(visibility string) string {
	if p.RunnerKindsSet() {
		return p.RunnerKinds
	}
	return DefaultRunnerKinds(visibility)
}

// RunnerKindsSet says whether the owner chose the kind.
func (p Policy) RunnerKindsSet() bool {
	return p.RunnerKinds == RunnerKindsEphemeralOnly || p.RunnerKinds == RunnerKindsAnyRunner
}

// EphemeralOnly is true when the repository's jobs go to ephemeral runners only.
func (p Policy) EphemeralOnly(visibility string) bool {
	return p.EffectiveRunnerKinds(visibility) == RunnerKindsEphemeralOnly
}

// DefaultPolicy is what a repository gets when the pool is first turned on:
// normal priority, 2 parallel jobs, fork pull requests off (R5.25, R5.26).
func DefaultPolicy() Policy {
	return Policy{Priority: 0, MaxParallelJobs: 2, Labels: []string{}, RunnerGroups: []string{}}
}

// Variable statuses (R5.28).
const (
	VarOK         = "ok"
	VarMissing    = "missing"
	VarWrong      = "wrong"
	VarNotManaged = "not_managed"
)

// VarLight is the variable an older version wrote for light jobs, with the
// plain label LightValue (the light runner label), so a workflow reads it as
// `runs-on: ${{ vars.RUNS_ON_LIGHT }}`. Light runners are gone (decision D110,
// 19 Nov 2026): the reconcile rewrites a variable that still holds LightValue
// to the pool's normal labels (github-integration R5.65).
const (
	VarLight   = "RUNS_ON_LIGHT"
	LightValue = "crsp-light"
)

// ManagedVariable is a CRSP_RUNS_ON_* variable as the controller last wrote it.
type ManagedVariable struct {
	RepositoryID int64
	Name         string
	Value        string
	Status       string
	WrittenAt    time.Time
	CheckedAt    time.Time
}

// Finding kinds of the runs-on check (R5.30).
const (
	KindNoRunner    = "no_runner_with_labels" // fail
	KindNotGeneric  = "not_generic"           // warning: literal labels instead of the variable
	KindUsesVar     = "uses_variable"         // fail: the variable the workflow reads is not set
	KindHosted      = "hosted"                // not stored
	SeverityFail    = "fail"
	SeverityWarning = "warning"
)

// Finding is one runs-on finding with file and line.
type Finding struct {
	RepositoryID int64
	Path         string
	Line         int
	Labels       []string
	Kind         string
	Text         string
}

// Severity is fail or warning, from the kind.
func (f Finding) Severity() string {
	if f.Kind == KindNotGeneric {
		return SeverityWarning
	}
	return SeverityFail
}

// Plan step actions (the OpenAPI PlanStep enum).
const (
	ActPoolOn         = "pool_on"
	ActPoolOff        = "pool_off"
	ActWriteVariable  = "write_variable"
	ActDeleteVariable = "delete_variable"
)

// Step states.
const (
	StepPlanned = "planned"
	StepRunning = "running"
	StepDone    = "done"
	StepFailed  = "failed"
	StepSkipped = "skipped"
)

// Plan states.
const (
	PlanPlanned  = "planned"
	PlanApplying = "applying"
	PlanDone     = "done"
	PlanFailed   = "failed"
	PlanStale    = "stale"
	PlanStopped  = "stopped"
)

// Step is one thing a plan does.
type Step struct {
	ID       string `json:"id"`
	Action   string `json:"action"`
	Target   string `json:"target,omitempty"`
	Text     string `json:"text"`
	NeedsYes bool   `json:"needs_yes"`
	State    string `json:"state"`
	Error    string `json:"error,omitempty"`
	// RepositoryID ties the step to its repository.
	RepositoryID int64 `json:"repository_id"`
	// Pool is the wanted pool state of a pool_on or pool_off step.
	Pool bool `json:"pool,omitempty"`
	// Value is what a write_variable step writes.
	Value string `json:"value,omitempty"`
}

// Plan is a planned change. Nothing changes until it is applied (R5.20).
type Plan struct {
	ID        string
	State     string
	StateHash string
	Steps     []Step
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Installation is a stored installation.
type Installation struct {
	ID           int64
	AccountLogin string
	AccountType  string
	Selection    string
	Permissions  map[string]string
	Suspended    bool
	Removed      bool
}

// ListFilter filters ListRepositories; results are ordered by full name.
type ListFilter struct {
	InstallationID int64
	Pool           *bool
	Query          string
	AfterName      string
	Limit          int
}

// Store is the data this package needs. MemStore is the in-memory
// implementation; the SQL adapter lives with the database package.
type Store interface {
	UpsertInstallation(ctx context.Context, i Installation) error
	MarkInstallationRemoved(ctx context.Context, id int64) error
	ListInstallations(ctx context.Context) ([]Installation, error)

	// UpsertRepository keeps the pool state of an existing row.
	UpsertRepository(ctx context.Context, r Repository) error
	GetRepository(ctx context.Context, id int64) (Repository, error)
	ListRepositories(ctx context.Context, f ListFilter) ([]Repository, error)
	SetRepositoryPool(ctx context.Context, id int64, on bool, by string, at time.Time) error

	// GetOrgPool returns the pool mode of an installation, or auth.ErrNotFound
	// when none was ever set (R5.21).
	GetOrgPool(ctx context.Context, installationID int64) (OrgPool, error)
	SetOrgPool(ctx context.Context, o OrgPool) error

	GetPolicy(ctx context.Context, repoID int64) (Policy, error)
	UpsertPolicy(ctx context.Context, repoID int64, p Policy) error

	ListManagedVariables(ctx context.Context, repoID int64) ([]ManagedVariable, error)
	UpsertManagedVariable(ctx context.Context, v ManagedVariable) error
	SetManagedVariableStatus(ctx context.Context, repoID int64, name, status string, at time.Time) error
	DeleteManagedVariable(ctx context.Context, repoID int64, name string) error

	ReplaceFindings(ctx context.Context, repoID int64, fs []Finding, at time.Time) error
	ListFindings(ctx context.Context, repoID int64) ([]Finding, time.Time, error)

	// FallbackSeconds is the seconds a repository's jobs ran on the hosted
	// label in a month ("2026-11"); AddFallbackSeconds adds to it (R5.43).
	FallbackSeconds(ctx context.Context, repoID int64, month string) (int64, error)
	AddFallbackSeconds(ctx context.Context, repoID int64, month string, seconds int64) error

	InsertPlan(ctx context.Context, p Plan) error
	GetPlan(ctx context.Context, id string) (Plan, error)
	UpdatePlan(ctx context.Context, p Plan) error
}

// GitHub is what repos needs from the GitHub adapter.
type GitHub interface {
	ListUserInstallations(ctx context.Context, tok github.UserToken) ([]github.Installation, error)
	ListInstallationRepositories(ctx context.Context, tok github.UserToken, installationID int64) ([]github.Repository, error)
	UserCanAdmin(ctx context.Context, tok github.UserToken, owner, repo string) (bool, error)
	CollaboratorPermission(ctx context.Context, installationID int64, owner, repo, login string) (string, error)
	ListAppInstallations(ctx context.Context) ([]github.Installation, error)
	ListAppRepositories(ctx context.Context, installationID int64) ([]github.Repository, error)
	ListVariables(ctx context.Context, installationID int64, owner, repo string) ([]github.Variable, error)
	SetVariable(ctx context.Context, installationID int64, owner, repo string, v github.Variable) error
	DeleteVariable(ctx context.Context, installationID int64, owner, repo, name string) error
	ListWorkflowFiles(ctx context.Context, installationID int64, owner, repo string, etags map[string]string) ([]github.WorkflowFile, error)
	// Organization pool (R5.21): the owner check and the runner group.
	OrgMembership(ctx context.Context, tok github.UserToken, org string) (role string, active bool, err error)
	OrgMemberRole(ctx context.Context, installationID int64, org, login string) (role string, active bool, err error)
	ListRunnerGroups(ctx context.Context, installationID int64, org string) ([]github.RunnerGroup, error)
	CreateRunnerGroup(ctx context.Context, installationID int64, org, name string, repoIDs []int64) (github.RunnerGroup, error)
	SelectRunnerGroup(ctx context.Context, installationID int64, org string, groupID int64) error
	ListRunnerGroupRepositories(ctx context.Context, installationID int64, org string, groupID int64) ([]int64, error)
	SetRunnerGroupRepositories(ctx context.Context, installationID int64, org string, groupID int64, repoIDs []int64) error
	SetForkApproval(ctx context.Context, installationID int64, owner, repo, policy string) error
	ListOpenJobs(ctx context.Context, installationID int64, owner, repo string) ([]github.Job, error)
}

// PoolRegistrar keeps the hidden GitHub registrations of the long-lived pool
// runners: one for each repository that uses the pool (runners R5.88).
type PoolRegistrar interface {
	// SyncPoolRegistrations adds the registrations of a repository that
	// started using the pool and drains and removes those of a repository that
	// stopped. It reads the pool-on repositories from the store.
	SyncPoolRegistrations(ctx context.Context, actor string) error
}

// TokenSource gives the GitHub user token of a signed-in user, from memory.
type TokenSource interface {
	GitHubToken(userID int64) (github.UserToken, error)
}

// Auditor writes the audit log (R5.16). auth.Service implements it.
type Auditor interface {
	Audit(ctx context.Context, p auth.Principal, action, targetType, subject, detail string)
}

// Capacity says which operating systems some machine can serve, and the
// labels that machines carry for each (R5.28). The key is "Linux", "Windows"
// or "macOS"; a missing key means no machine serves that OS.
type Capacity interface {
	OSLabels(ctx context.Context) (map[string][]string, error)
}

// UpSource says, per OS, whether some crsp machine can take jobs now: online
// on the agent link and accepting jobs (not paused, not on battery). The key
// is "Linux", "Windows" or "macOS"; an OS that has no machine is absent
// (github-integration R5.43).
type UpSource interface {
	UpOS(ctx context.Context) (map[string]bool, error)
}

// System is the principal for background work.
var System = auth.Principal{UserID: 0, Login: "controller", Role: auth.RoleOwner, Source: "controller"}
