package doctor

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/internal/leftovers"
)

// Env is what a check may read (spec 7.1). Every source is read-only; a
// check never writes through it (R5.7).
type Env struct {
	// Now is the time of the run. Checks use it, never time.Now, so tests are exact.
	Now   time.Time
	Scope Scope
	Fleet Fleet
	// GitHub is nil when the App is not set up; GitHub checks then skip.
	GitHub GitHub
	// KVM is nil when no agent reports KVM; the kvm checks then skip.
	KVM KVMSource
	// VM is nil when the VM backend of this machine is not read (the live
	// controller does not read it; `crsp vm doctor` does); the vm checks then skip.
	VM VMSource
	// Engines is nil when no agent reports its container engine; machine.engine then skips.
	Engines EngineSource
	// Demand is nil when the demand source is not running; demand.quiet then skips.
	Demand DemandSource
	// Ephemeral says whether the pool can make an ephemeral runner; nil makes
	// repo.ephemeral_capacity skip.
	Ephemeral EphemeralSource
	// Postgres is nil when no Postgres server is read; runner.postgres_logins then skips.
	Postgres PostgresSource
	// PoolName is the pool's name label; RunnerPrefix starts the name of every
	// runner the product makes (default "crsp-").
	PoolName string
	// SessionWait is how long after a restart a refused session is expected
	// (pool setting session_wait_minutes); zero means 3 minutes.
	SessionWait  time.Duration
	RunnerPrefix string

	memo *memo
}

// DefaultRunnerPrefix starts the name of runners the product makes.
const DefaultRunnerPrefix = "crsp-"

// Prefix returns the runner name prefix in use.
func (e Env) Prefix() string {
	if e.RunnerPrefix != "" {
		return e.RunnerPrefix
	}
	return DefaultRunnerPrefix
}

type memoEntry struct {
	once sync.Once
	val  any
	err  error
}

type memo struct {
	mu sync.Mutex
	m  map[string]*memoEntry
}

// Memo runs fn once per key in one run and returns its result to every
// caller, so a full run reads each repository's variables, workflow files and
// runner list once (R6.2). Without a run (nil memo) it just calls fn.
func (e Env) Memo(key string, fn func() (any, error)) (any, error) {
	if e.memo == nil {
		return fn()
	}
	e.memo.mu.Lock()
	ent, ok := e.memo.m[key]
	if !ok {
		ent = &memoEntry{}
		e.memo.m[key] = ent
	}
	e.memo.mu.Unlock()
	ent.once.Do(func() { ent.val, ent.err = fn() })
	return ent.val, ent.err
}

// ErrUnreachable is returned by a source that could not be reached (an agent
// that is offline, GitHub that does not answer). A check turns it into skip (R5.5).
var ErrUnreachable = errors.New("unreachable")

// GitHub is the part of the GitHub adapter the doctor reads with.
type GitHub interface {
	GetApp(ctx context.Context) (github.App, error)
	GetInstallation(ctx context.Context, installationID int64) (github.Installation, error)
	ListRunners(ctx context.Context, installationID int64, scope github.RunnerScope) ([]github.Runner, error)
	ListVariables(ctx context.Context, installationID int64, owner, repo string) ([]github.Variable, error)
	ListWorkflowFiles(ctx context.Context, installationID int64, owner, repo string, etags map[string]string) ([]github.WorkflowFile, error)
}

// Fleet is the controller's own data: machines, runners and repositories.
type Fleet interface {
	Machines(ctx context.Context) ([]Machine, error)
	// Runners lists the runners the product manages, from the database.
	Runners(ctx context.Context) ([]Runner, error)
	// Services lists the runner services found in the OS service list of a
	// machine, asked from its agent (R5.11). ErrUnreachable when the agent does not answer.
	Services(ctx context.Context, machineID string) ([]OSService, error)
	// Repositories lists repositories that have the pool on.
	Repositories(ctx context.Context) ([]Repository, error)
}

// CacheUsage is what an agent measured of one runner's cache folder: the bytes
// it holds and its declared cap (0 when it has none).
type CacheUsage struct {
	MachineID, RunnerName string
	// Kind is tool_cache or rust_cache.
	Kind      string
	Size, Cap int64
}

// CacheFleet is the optional part of Fleet that lists the measured caches
// (caches R5.42). A Fleet that does not have it makes the cache checks skip.
type CacheFleet interface {
	Caches(ctx context.Context) ([]CacheUsage, error)
}

// LeftoverFleet is the optional part of Fleet that scans a machine for runner
// leftovers no controller owns (R5.48). The scan reads only. ErrUnreachable
// when the agent does not answer; any other error is shown as the reason the
// check skipped. A Fleet that does not have it makes machine.runner_leftovers skip.
type LeftoverFleet interface {
	Leftovers(ctx context.Context, machineID string) (leftovers.Scan, error)
}

// Machine is a machine as the checks need it.
type Machine struct {
	ID, Name, OS string
	Online       bool
	LastSeen     time.Time
	// State is active, drained, draining or removing.
	State string
	// Load is nil when no sample exists yet.
	Load *Load
	// BudgetMemoryPct and BudgetCPUPct are the machine's load budget.
	BudgetMemoryPct, BudgetCPUPct int
	// OverBudgetSince is when the load went over the budget and stayed; zero when under it.
	OverBudgetSince time.Time
	// LoadWarnSince is when the machine went over its budget with jobs running
	// and the controller's load watch raised its warning; zero when none (scheduling R5.77).
	LoadWarnSince time.Time
	// RunningJobs name the jobs that hold a slot ("repo job").
	RunningJobs []string
	// DiskFreeBytes and DiskTotalBytes describe the work volume; nil when unknown.
	DiskFreeBytes, DiskTotalBytes *int64
	// PauseReason is why the machine takes no new jobs ("disk_low", "paused",
	// "on_battery", ...); empty while it accepts.
	PauseReason string
	// Admin is true when the doctor on this machine runs with administrator
	// rights, for checks that need them (R5.13). Reported by the agent.
	Admin bool
	// LightSlots is the machine's light slot setting (scheduling R5.1).
	LightSlots int
	// JobSlots is the machine's job slot setting and GateMaxWaitMin its hold
	// limit; 0 means unknown. RunnerPeak is the most runners one repository
	// can use on the machine (pool runners plus its own), RunnerPeakRepo the
	// repository that has them; empty when only pool runners exist
	// (scheduling R5.73, R5.75).
	JobSlots, GateMaxWaitMin int
	RunnerPeak               int
	RunnerPeakRepo           string
	// DockerCompact is the Docker disk compact policy and what the agent
	// reported; nil when the machine has no Docker disk file to compact.
	DockerCompact *DockerCompact
	// PoolMemory is the pool size and the memory of the machine, against what
	// the PC itself uses (machine.pool.memory, OPS61); zero when not known.
	PoolMemory PoolMemory
	// ResetSigns say why the machine's settings or runners may have gone back
	// to the defaults: a rejoin, default settings after chosen ones, runner
	// records of a machine that is not in the pool (machine.reset, R5.61).
	ResetSigns []string
}

// PoolMemory is what the machine.pool.memory check reads (machines R5.68).
type PoolMemory struct {
	// TotalBytes is the memory of the machine.
	TotalBytes int64
	// PoolGiB is the configured pool memory; 0 when none is set.
	PoolGiB float64
	// WSLCapBytes is the `.wslconfig` memory cap; 0 when the file sets none.
	WSLCapBytes int64
	// HostP95Bytes is the 7-day p95 of the memory the PC uses without the pool.
	// HostReady is true once the agent has 24 hours of samples.
	HostP95Bytes int64
	HostReady    bool
}

// DockerCompact is what the machine.docker.compact check reads (caches R5.51 to R5.54).
type DockerCompact struct {
	// Enabled is the policy: on, or automatic with a disk file.
	Enabled bool
	// CanCompact is true when the agent has the administrator rights.
	CanCompact bool
	// FileBytes and UsedBytes are the sizes the agent reported.
	FileBytes, UsedBytes int64
	UsedKnown            bool
}

// Load is the newest load sample.
type Load struct {
	MemoryPct, CPUPct float64
	MemoryKnown       bool
	CPUKnown          bool
	SampledAt         time.Time
}

// Runner is a runner the product manages.
type Runner struct {
	ID, Name    string
	MachineID   string
	MachineName string
	OS          string
	Kind        string // persistent or ephemeral
	// UncleanReason is why the clean-up after a job failed; the runner takes
	// no new job until it is clean (runners R5.90).
	UncleanReason string
	// Labels are the labels the runner carries.
	Labels []string
	// LegacyLight is true for a light runner an older version made; the
	// reconcile removes it (decision D110). LightRemovalProblem is why the last
	// try to remove it failed; empty while no try failed.
	LegacyLight         bool
	LightRemovalProblem string
	// GitHubRunnerID is set once registered.
	GitHubRunnerID int64
	// ServiceName is the OS service name of a persistent runner.
	ServiceName string
	// InstallationID and Scope say where it registered.
	InstallationID int64
	Scope          github.RunnerScope
	// FolderReadable is false when the agent could not read the runner folder
	// (it is locked to the runner's account, R5.13).
	FolderReadable bool
	// HookProblems lists problems with the job hooks found by the agent: a
	// missing hook, a .cmd hook, one in a user profile (R5.14). Empty is fine.
	HookProblems []string
	// HookProbe is what the agent saw when it ran the runner's job-started hook
	// as the service account in probe mode; nil when it did not run it (R5.29).
	HookProbe *HookProbe
	// ListenerKnown is true when the agent looked for the runner's listener
	// processes (Runner.Listener started from the runner folder). ListenerPIDs
	// are their process ids; ServiceRunning is the service state (R5.30).
	ListenerKnown  bool
	ListenerPIDs   []int
	ServiceRunning bool
	// SessionRefused is true when the listener's recent output says "a session
	// for this runner already exists"; RestartedAt is when the pool, VM or
	// service of the runner was last started (runners R5.67, doctor R5.39).
	SessionRefused bool
	RestartedAt    time.Time
	// ToolCache is what the agent saw in the runner's tool cache; nil when it
	// did not look (R5.35).
	ToolCache *ToolCacheProbe
	// CacheWrite is what the agent saw when it tried to write the shared tool
	// folders as the runner's own user; nil when it did not look (caches R5.57).
	CacheWrite *CacheWriteProbe
	// Tools is what the agent saw of the common tools on the runner's PATH as
	// its service sees it; nil when it did not look (doctor R5.63).
	Tools *ToolsProbe
	// SymlinkWanted is true when the machine or label setting asks for the
	// symbolic link right for this runner (runners R5.14). Symlink is what the
	// agent read; nil when it did not look (R5.42).
	SymlinkWanted bool
	Symlink       *SymlinkProbe
	// Logon is what the agent saw of the service account's right to log on as
	// a service and of the service itself, after it gave the right again; nil
	// when it did not look (runners R5.89, doctor R5.45).
	Logon *LogonProbe
}

// LogonProbe is what the agent saw of one Windows runner service: the account,
// whether it holds SeServiceLogonRight, and whether the service runs.
type LogonProbe struct {
	Service string
	Account string
	// Known is false when the agent could not read the rights of the account.
	Known bool
	// Held is true when the account holds SeServiceLogonRight.
	Held bool
	// Running is true when the service runs.
	Running bool
	// Problem is why the service did not start after the agent's one retry.
	Problem string
}

// SymlinkProbe is what the agent read about the symbolic link right of a
// Windows runner's account. It only reads; Developer Mode is never turned on.
type SymlinkProbe struct {
	// Held is true when the account holds SeCreateSymbolicLinkPrivilege.
	Held bool
	// DeveloperMode is true when Windows Developer Mode is on (a non-admin
	// account can then link without the right).
	DeveloperMode bool
	// Err is set when the agent could not read the right.
	Err string
}

// SymlinkProbeFrom maps what an agent reported about symbolic links to the
// probe of a runner (R5.42). known false gives nil, so the check skips. The
// agent can link either by the right or by Developer Mode; when it can link
// and Developer Mode is off, it is the right that holds.
func SymlinkProbeFrom(known, canLink, developerMode bool, errText string) *SymlinkProbe {
	if !known {
		if errText == "" {
			return nil
		}
		return &SymlinkProbe{Err: errText}
	}
	return &SymlinkProbe{Held: canLink && !developerMode, DeveloperMode: developerMode}
}

// ToolsProbe is the common tools found on one runner's PATH. Err is set when
// the agent could not run the probe; the check then skips the runner.
type ToolsProbe struct {
	Err   string
	Tools []ToolProbe
}

// ToolProbe is one tool: found or not, and the first line of its version output.
type ToolProbe struct {
	Name    string
	Found   bool
	Version string
}

// CacheWriteProbe is the result of trying to create a file in each shared tool
// folder (cargo-target, cargo-home, rustup-home, xwin, ...) as the runner's own
// user. Root and Group are the shared tools folder and the group that owns it.
type CacheWriteProbe struct {
	Root  string
	Group string
	Dirs  []CacheDirProbe
}

// CacheDirProbe is one folder tried; Err is empty when the runner could write.
type CacheDirProbe struct {
	Dir string
	Err string
}

// ToolCacheProbe is what the agent found in a runner's tool cache (_work/_tool
// or the configured tool cache folder), seen as the runner's account.
type ToolCacheProbe struct {
	// Dir is the folder that was looked at.
	Dir string
	// Err is set when the account could not read a file there (for example
	// "Access is denied"). File is the file that failed.
	Err  string
	File string
	// OwnerKnown is true when the agent read the owner and inheritance of the
	// files instead of opening one. WrongOwner lists files whose owner is not
	// the runner account; NotInherited lists files with inheritance off.
	OwnerKnown   bool
	WrongOwner   []string
	NotInherited []string
}

// HookProbe is the result of one run of a runner's real job-started hook script
// as the runner's service account, with CRSP_HOOK_PROBE=1, so the probe takes
// no slot and starts no job (R5.29).
type HookProbe struct {
	// Err is set when the script could not be started at all (no PowerShell,
	// no /bin/sh, a script the account may not read or run).
	Err      string
	ExitCode int
	// Output is what the script printed, secrets removed by the redactor.
	Output string
}

// OSService is one runner service from the OS service list (R5.11).
type OSService struct {
	// Name is the service, unit or launchd label, such as "actions.runner.org-repo.name".
	Name string
	// RunnerName is the runner name parsed from Name.
	RunnerName string
	// Scope is "owner-repo" or the organization, parsed from Name.
	Scope string
	// Running is true when the service runs now.
	Running bool
	// Automatic is true when it starts with the machine. AutomaticKnown is
	// false when the OS did not say.
	Automatic      bool
	AutomaticKnown bool
}

// Repository is a repository with the pool on.
type Repository struct {
	ID             int64
	FullName       string
	Owner, Name    string
	InstallationID int64
	// DefaultBranch is where the workflows are read from.
	DefaultBranch string
	// Expected holds the value the product would write for each variable,
	// keyed by variable name, for OSes the policy enables. A JSON list.
	Expected map[string]string
	// EphemeralOnly is true when the repository's jobs go to ephemeral runners
	// only (github-integration R5.60).
	EphemeralOnly bool
	// Where is where the repository's jobs go now: "crsp", "hosted" or
	// "waiting"; HostedOSes are the OSes on GitHub-hosted runners, WaitingOSes
	// the ones whose jobs wait for a machine. FallbackOn is the setting
	// (github-integration R5.42 to R5.46).
	Where       string
	HostedOSes  []string
	WaitingOSes []string
	FallbackOn  bool
}

// InScopeMachine is a helper for checks.
func (e Env) InScopeMachine(m Machine) bool { return e.Scope.MatchMachine(m.ID, m.Name) }

// InScopeRepository is a helper for checks.
func (e Env) InScopeRepository(r Repository) bool { return e.Scope.MatchRepository(r.ID) }

// KVMState is what the agent saw of KVM for one machine and pool (doctor
// R5.32, machines R5.53). A machine with KVM off in the pool is not listed.
type KVMState struct {
	Machine Machine
	// HostDevice is true when /dev/kvm exists on the host (the WSL VM on Windows).
	HostDevice bool
	// ContainerUsable is true when /dev/kvm opens inside a pool container.
	ContainerUsable bool
	// LabelPresent is true when the pool's runners carry the kvm label.
	LabelPresent bool
	// LoaderRunning is true when the privileged kvm loader service of the
	// compose file runs (WSL2 only).
	LoaderRunning bool
	// WaitedOut is true when the pool gave up waiting for /dev/kvm and started without it.
	WaitedOut bool
	// NestedKnown is false when .wslconfig could not be read; Nested is then ignored.
	NestedKnown, Nested bool
	// ModuleKnown is false when the WSL VM could not be asked; ModuleLoaded is
	// then ignored. ModuleLoaded is true when kvm_intel or kvm_amd is loaded in
	// the WSL VM (Windows only, machines R5.53).
	ModuleKnown, ModuleLoaded bool
}

// EngineState is the container engine an agent found on its machine (runners
// R5.47, machines R5.18).
type EngineState struct {
	Machine Machine
	// Kind is docker-wsl2-windows-cli, docker-wsl2-wsl, docker-desktop,
	// docker-native or none.
	Kind    string
	Usable  bool
	Version string
	Reason  string
}

// EngineSource lists the engine per machine; ErrUnreachable when an agent does not answer.
type EngineSource interface {
	Engines(ctx context.Context) ([]EngineState, error)
}

// KVMSource lists the KVM state per machine; ErrUnreachable when an agent does not answer.
type KVMSource interface {
	KVM(ctx context.Context) ([]KVMState, error)
}

// QuietDemand is a repository whose queued jobs for the pool the demand source
// has not seen for the allowed time (github-integration R5.37).
type QuietDemand struct {
	Repository string
	// Queued is how many queued jobs GitHub lists for the pool.
	Queued int
	// Since is when the quiet started.
	Since time.Time
	// Restarted is true once the controller restarted the demand loop for it.
	Restarted   bool
	RestartedAt time.Time
}

// EphemeralSource says whether an ephemeral pool ceiling slot exists on an
// online machine (doctor R5.47).
type EphemeralSource interface {
	EphemeralCapacity(ctx context.Context) (bool, error)
}

// UncleanLister is an optional part of Fleet: the runners with a failed
// clean-up, pool runners included (doctor R5.46). Without it the check reads
// Fleet.Runners.
type UncleanLister interface {
	UncleanRunners(ctx context.Context) ([]Runner, error)
}

// DemandSource lists the repositories whose demand source is quiet while jobs
// wait. The controller's watchdog keeps it; a check only reads it.
type DemandSource interface {
	QuietDemand(ctx context.Context) ([]QuietDemand, error)
}

// VMTool is one program the VM backend runs.
type VMTool struct {
	Name  string
	Found bool
}

// VMDisk is what the cache holds of one distro: the downloaded image, its
// prepared base disk and the package list that base was built with (caches
// R5.46).
type VMDisk struct {
	Distro string
	// ImagePresent is false when the cache has no image of the distro.
	ImagePresent bool
	// ImageChecked is false when the file could not be read to its end;
	// ImageOK is then ignored. ImageOK is true when the digest of the file
	// starts with the digest in its name.
	ImageChecked, ImageOK bool
	// BasePresent is true when the prepared base disk of the image exists.
	BasePresent bool
	// Packages is the manifest of the base disk; empty when it has none.
	Packages []string
}

// VMState is what the VM backend of one machine looks like (caches R5.46,
// `crsp vm doctor`).
type VMState struct {
	Machine Machine
	// Accelerator names the hypervisor this OS uses: KVM, Hyper-V or
	// Virtualization.framework. AccelKnown is false when it could not be asked.
	Accelerator         string
	AccelKnown, AccelOK bool
	AccelDetail         string
	Tools               []VMTool
	SeedTool            VMTool
	Disks               []VMDisk
	CacheDir            string
	// FreeKnown is false when the free space could not be read.
	FreeKnown             bool
	FreeBytes, FloorBytes int64
	// Bridge is the network bridge the VMs may use; BridgeKnown is false when
	// the OS has no such thing to ask or it could not be asked.
	Bridge                     string
	BridgeKnown, BridgePresent bool
	// Needed lists the packages the runners need in the base disk.
	Needed []string
}

// VMSource reads the VM backend of this machine.
type VMSource interface {
	VM(ctx context.Context) (VMState, error)
}

// PostgresLogin is a Postgres login of a pool's server and the shared owner
// role it belongs to (runners R5.97).
type PostgresLogin struct {
	Name, OwnerRole string
	// SelfGrant: createrole_self_grant is 'set, inherit' on the login (OPS54).
	SelfGrant bool
	// Attrs is the login's role attributes as a stable text; Members the roles
	// it is a member of, sorted. The check compares them across the logins of
	// one owner role.
	Attrs   string
	Members []string
}

// PostgresSource lists the runner logins of the pool's Postgres server
// (doctor R5.59). Read only; removing one is a separate action.
type PostgresSource interface {
	PostgresLogins(ctx context.Context) ([]PostgresLogin, error)
}
