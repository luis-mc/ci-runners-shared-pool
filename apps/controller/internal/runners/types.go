package runners

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/poolsize"
)

// ScopePool is the scope of a pool runner: a machine, a system and labels,
// with no repository of its own. Its GitHub registrations are hidden records
// (PoolRunnerID set) that the pool reconcile keeps, one for each repository
// that uses the pool (runners R5.88).
const ScopePool = "pool"

// Scope is where a runner registers (runners R5.2).
type Scope struct {
	Kind         string // repository, organization, or pool (a pool runner)
	RepositoryID int64
	Organization string
	Group        string
}

// Record is the runner record (runners R5.2). It has no token or password.
type Record struct {
	ID        string
	Name      string
	MachineID string
	OS        string
	Arch      string
	Kind      string // persistent or ephemeral
	Backend   string
	Isolation string
	Labels    []string
	// Route and SizeClass are what routing and scheduling read. The name is
	// for people only (runners R5.70, R5.71).
	Route          Route
	SizeClass      SizeClass
	Scope          Scope
	RunnerVersion  string
	State          State
	Managed        bool
	ServiceName    string
	Account        string
	Folder         string
	GitHubRunnerID int64
	CurrentJob     string // empty when none
	// PoolRunnerID is set on a hidden registration: the id of the pool runner
	// whose machine slot it serves. Empty on every other record (runners R5.88).
	PoolRunnerID string
	// KeepDependencies is the persistent runner's setting: downloaded
	// dependencies stay between jobs (runners R5.90). Temporary files of a job
	// are always removed, whatever this says. Meaningless for ephemeral.
	KeepDependencies bool
	// DependencyCacheBytes is the size of the runner's own dependency cache
	// folder, as the agent last measured it (runners R5.90).
	DependencyCacheBytes int64
	// UncleanReason is set when the clean-up of a job failed; the runner takes
	// no new job until it is clean (runners R5.91).
	UncleanReason string
	// FailedReason is the whole plain reason a runner is in the failed state,
	// including what the clean-up left behind. Empty in every other state.
	FailedReason string
	// Reason is why the runner is in its state; set with a failure (UX-19).
	Reason    string
	CreatedAt time.Time
}

// KeepsState is true for a persistent runner (runners R5.7).
func (r Record) KeepsState() bool { return r.Kind == "persistent" }

// Hygiene is the part of a persistent runner's record that its clean-up
// writes: the dependency setting, the size of its own dependency cache and the
// reason it is unclean ("" when clean).
type Hygiene struct {
	KeepDependencies     bool
	DependencyCacheBytes int64
	UncleanReason        string
}

// Filter narrows a runner list.
type Filter struct {
	MachineID string
	OS        string
	State     string
	Managed   *bool
}

// BackendOption is what a machine's agent reports for one backend and whether
// the user allowed it (machines R5.11, runners R5.9).
type BackendOption struct {
	Name     backend.Name
	Usable   bool
	Allowed  bool
	Reason   string
	Kinds    []string
	OS       []string
	Verified []string // capability labels the agent checked
}

// Machine is the part of a machine the runner rules need.
type Machine struct {
	ID       string
	Name     string
	OS       string
	Arch     string
	Backends []BackendOption
	// ContainerOS is the OS the machine's container engine runs containers for
	// when the engine answers (machines R5.18, runners R5.47); a Windows machine
	// with Docker Desktop on WSL2 says "linux". Empty when not known.
	ContainerOS string
	// Online is true while the machine's agent is connected.
	Online  bool
	Symlink bool // a machine or label setting asks for the symlink right (R5.14)
	// PoolCPUs and JobSlots are the owner's pool size and the runners that run
	// at once; a runner's CPU count is their split (machines R5.54, R5.58).
	// PoolCPUs 0 means no pool size is set and no CPU count is exported.
	PoolCPUs int
	JobSlots int
	// PoolMemoryGiB is the owner's pool memory. With PoolCPUs it becomes the
	// limits of the pool container of each repository (runners R5.27).
	PoolMemoryGiB float64
}

// MaxParallel is the CPU count a runner on this machine gives its jobs, or 0
// when the owner has set no pool size (runners R5.53).
func (m Machine) MaxParallel() int {
	if m.PoolCPUs <= 0 {
		return 0
	}
	return poolsize.PerRunner(m.PoolCPUs, m.JobSlots)
}

// PlanState and step states mirror the API enums.
const (
	PlanPlanned  = "planned"
	PlanApplying = "applying"
	PlanDone     = "done"
	PlanFailed   = "failed"
	PlanStopped  = "stopped"
)

// Step actions (the runner ones of the API enum).
const (
	ActAddRunner       = "add_runner"
	ActStopOldService  = "stop_old_service"
	ActRemoveOldRunner = "remove_old_runner"
	ActDeleteOldFolder = "delete_old_folder"
)

// Step is one named change. A step that deletes needs its own yes (UX-62).
type Step struct {
	ID       string
	Action   string
	Text     string
	Target   string
	NeedsYes bool
	WaitsFor string
	State    string // planned, running, done, failed, skipped, waiting
	Error    string
}

// AddSpec is one runner to add, kept with the plan. It has no token.
type AddSpec struct {
	Name      string
	MachineID string
	OS        string
	Arch      string
	Kind      string
	Backend   string
	Labels    []string
	Scope     Scope
	Symlink   bool
	// SizeClass is the declared size; empty means the default class.
	SizeClass SizeClass
	// MaxParallel is CI_MAX_PARALLEL for the runner's jobs; 0 sets none.
	MaxParallel int
	// PoolRunnerID makes the runner a hidden registration of that pool runner.
	PoolRunnerID string
	// KeepDependencies is the persistent runner's setting (runners R5.90);
	// false for an ephemeral runner, which keeps nothing.
	KeepDependencies bool
}

// Plan is a planned change. Nothing changes until it is applied.
type Plan struct {
	ID        string
	Area      string
	State     string
	CreatedAt time.Time
	CreatedBy string
	Steps     []Step
	Adds      []AddSpec
	// OldRunnerID is the runner a replacement removes.
	OldRunnerID string
}

// Store keeps runner records and plans.
type Store interface {
	InsertRunner(ctx context.Context, r Record) error
	GetRunner(ctx context.Context, id string) (Record, error)
	ListRunners(ctx context.Context, f Filter) ([]Record, error)
	SetRunnerState(ctx context.Context, id string, to State, detail string) error
	SetRunnerService(ctx context.Context, id, serviceName, account string) error
	SetRunnerRegistration(ctx context.Context, id string, githubRunnerID int64, version string) error
	DeleteRunner(ctx context.Context, id string) error
	// SetRunnerPool makes a runner a hidden registration of a pool runner
	// (the migration of a repository runner, runners R5.88).
	SetRunnerPool(ctx context.Context, id, poolRunnerID string) error
	// SetRunnerHygiene stores the dependency setting, the cache size and the
	// unclean reason of a persistent runner (runners R5.90, R5.91).
	SetRunnerHygiene(ctx context.Context, id string, h Hygiene) error
	SavePlan(ctx context.Context, p Plan) error
	GetPlan(ctx context.Context, id string) (Plan, error)
}

// ErrNotFound is returned by a Store and the Service for a missing runner or plan.
var ErrNotFound = errors.New("not found")

// Machines gives the machine facts.
type Machines interface {
	GetMachine(ctx context.Context, id string) (Machine, error)
}

// Resolver turns a scope into what GitHub calls need.
type Resolver interface {
	Resolve(ctx context.Context, s Scope) (Target, error)
}

// OrgScoper is implemented by a Resolver that knows the pool per organization
// (github-integration R5.21). OrgScope turns the scope of a repository of an
// organization whose pool is on into the organization scope with the runner
// group name; for any other scope it returns false.
type OrgScoper interface {
	OrgScope(ctx context.Context, s Scope) (Scope, bool, error)
}

// Target is a resolved scope.
type Target struct {
	Scope          github.RunnerScope
	InstallationID int64
	URL            string // https://github.com/<owner>[/<repo>]
	Group          string
}

// GitHub is the part of the GitHub adapter the runner flows use. Tokens are
// fetched right before use and dropped (runners R6.2).
type GitHub interface {
	CreateRegistrationToken(ctx context.Context, installationID int64, scope github.RunnerScope) (string, error)
	CreateRemoveToken(ctx context.Context, installationID int64, scope github.RunnerScope) (string, error)
	ListRunners(ctx context.Context, installationID int64, scope github.RunnerScope) ([]github.Runner, error)
	// GenerateJITConfig gives the config of one ephemeral runner (runners
	// R5.25, github-integration R5.34). The caller passes it on and drops it.
	GenerateJITConfig(ctx context.Context, installationID int64, scope github.RunnerScope, in github.JITRequest) (github.JITConfig, error)
	// DeleteRunner removes a runner from GitHub. GitHub refuses a runner that
	// has a job, so a job that starts at that moment is not cut.
	DeleteRunner(ctx context.Context, installationID int64, scope github.RunnerScope, runnerID int64) error
}

// RemoveRequest asks an agent to take a runner away.
type RemoveRequest struct {
	RunnerID    string
	ServiceName string
	// RemoveToken deregisters the runner from GitHub; used once.
	RemoveToken string
	// KeepFiles deregisters only: the service entry, account and folder stay
	// until a later step with its own yes (a replace, runners R5.6).
	KeepFiles bool
}

// Agent is what the controller needs from a machine's agent, through the agent
// link (decision 0004). The link adapter maps each call to its message.
type Agent interface {
	AddRunner(ctx context.Context, machineID string, spec backend.RunnerSpec, cfg backend.StartConfig) (backend.Handle, error)
	// StopService stops a service without removing anything.
	StopService(ctx context.Context, machineID, serviceName string) error
	// RemoveRunner deregisters the runner with the remove token and checks the
	// result. Unless KeepFiles is set it then cleans up: the service, the
	// account and the folder.
	RemoveRunner(ctx context.Context, machineID string, req RemoveRequest) error
	// CleanUp deletes the service entry, the account and the folder of a
	// runner (the last replace step, with its own yes).
	CleanUp(ctx context.Context, machineID, serviceName string) error
}

// Auditor writes the audit entry of every add, remove and replace (R6.8).
type Auditor interface {
	Audit(ctx context.Context, actor, action, machineID, subject, detail string)
}

// RefusalError is a request the controller refuses with a reason.
type RefusalError struct{ Reason string }

func (e *RefusalError) Error() string { return e.Reason }

// ConfirmItem is one thing a removal takes away.
type ConfirmItem struct{ Kind, Text string }

// NeedsConfirmation asks for a yes that names each item that goes and the job
// the removal waits for (UX-62, runners R5.4).
type NeedsConfirmation struct {
	Items []ConfirmItem
}

func (e *NeedsConfirmation) Error() string { return "this removal needs a yes" }

// OSName is the display name of an OS.
func OSName(os string) string {
	switch os {
	case "windows":
		return "Windows"
	case "macos":
		return "macOS"
	case "linux":
		return "Linux"
	}
	return os
}

func refuse(format string, a ...any) error { return &RefusalError{Reason: fmt.Sprintf(format, a...)} }

// ErrNoPoolRepository refuses a runner plan with no scope while no repository
// uses the pool: persistent runners register through a repository until OPS23.
var ErrNoPoolRepository error = &RefusalError{Reason: "no repository uses the pool yet; choose one in Repositories first"}
