// Package machines is the controller side of the agent link (decision 0004;
// machines R5.1 to R5.43, R6.1 to R6.8): the machine record, join codes and
// machine keys (stored only as hashes), settings, drain and removal, offline
// detection, and the AgentService the agents dial.
//
// Data goes through the Repository interface. The in-memory MemRepo ships with
// this package; the SQLite adapter over the tables of migration 00004 belongs
// to internal/store.
package machines

import (
	"errors"
	"github.com/luis-mc/ci-runners-shared-pool/internal/poolsize"
	"time"
)

// Machine states (machines R5.22).
const (
	StateActive   = "active"
	StateDraining = "draining"
	StateDrained  = "drained"
	StateRemoving = "removing"
)

// Change kinds and states (machines R5.30 to R5.34).
const (
	KindDrain          = "drain"
	KindSettings       = "settings"
	KindRemove         = "remove"
	KindAgentUpdate    = "agent_update"
	KindRunnerUpdate   = "runner_update"
	KindServiceRestart = "service_restart"

	ChangeWaiting  = "waiting"
	ChangeRunning  = "running"
	ChangeDone     = "done"
	ChangeCancel   = "cancelled"
	ChangeNeedsYou = "needs_you"
)

// Errors the API maps to the error envelope.
var (
	ErrNotFound        = errors.New("machine not found")
	ErrUnauthenticated = errors.New("unknown or revoked machine key")
	ErrInvalid         = errors.New("invalid value")
	ErrConflict        = errors.New("conflict")
	ErrNotSupported    = errors.New("not supported in this build")
	ErrOffline         = errors.New("machine is offline")
)

// NeedsConfirmationError says what a destructive step needs a yes for (UX-62).
// Names lists every item that goes or waits.
type NeedsConfirmationError struct {
	Message string
	Names   []string
}

func (e *NeedsConfirmationError) Error() string { return e.Message }

// JoinError is a refused join with a reason fit to show ("this code was already used").
type JoinError struct{ Reason string }

func (e *JoinError) Error() string { return e.Reason }

// Gpu is one GPU (R5.10).
type Gpu struct {
	Vendor      string `json:"vendor"`
	Model       string `json:"model"`
	MemoryBytes int64  `json:"memory_bytes"`
}

// Facts are what the agent detects (R5.10).
type Facts struct {
	HostName       string    `json:"host_name"`
	OS             string    `json:"os"` // linux, windows or macos
	OSName         string    `json:"os_name"`
	OSVersion      string    `json:"os_version"`
	KernelVersion  string    `json:"kernel_version"`
	Arch           string    `json:"arch"` // x64 or arm64
	CPUModel       string    `json:"cpu_model"`
	Cores          int       `json:"cores"`
	Threads        int       `json:"threads"`
	MemoryBytes    int64     `json:"memory_bytes"`
	DiskTotalBytes int64     `json:"disk_total_bytes"`
	DiskFreeBytes  int64     `json:"disk_free_bytes"`
	GPUs           []Gpu     `json:"gpus"`
	AgentVersion   string    `json:"agent_version"`
	DetectedAt     time.Time `json:"detected_at"`
	// WSL is what `.wslconfig` declares, on Windows with WSL2 (R5.54).
	WSL WSLFacts `json:"wsl"`
}

// WSLFacts are the declared WSL2 limits. A value the file does not set is 0.
type WSLFacts struct {
	ConfigPresent        bool  `json:"config_present"`
	MemoryBytes          int64 `json:"memory_bytes"`
	Processors           int   `json:"processors"`
	NestedVirtualization bool  `json:"nested_virtualization"`
}

// Backend is one detected isolation backend (R5.11).
type Backend struct {
	Name     string `json:"name"`
	Present  bool   `json:"present"`
	Usable   bool   `json:"usable"`
	Version  string `json:"version"`
	Reason   string `json:"reason"`
	Rootless bool   `json:"rootless"`
}

// Machine is one computer in the pool.
type Machine struct {
	ID           string
	Name         string
	OS           string
	Arch         string
	Facts        Facts
	Backends     []Backend
	State        string
	IsController bool
	AgentVersion string
	Online       bool
	// Accepting and PauseReason are computed from Taking without offline (R5.62).
	// They are filled when a machine is read; another feature reads `Online &&
	// Accepting` to decide on hosted fallback. A machine that reported nothing
	// accepts.
	Accepting   bool
	PauseReason string
	// Taking is the one computed answer the console reads (OPS29): taking jobs
	// or not, with the reason; it includes offline.
	Taking    Availability
	LastSeen  time.Time
	JoinedAt  time.Time
	RemovedAt time.Time
	// RejoinedAt is when the same machine last joined again with a new key and
	// kept its record, settings and runners (R5.70); zero when it never did.
	RejoinedAt time.Time
}

// Settings are the per-machine settings (R5.22, scheduling R5.1).
type Settings struct {
	Version             int64
	State               string
	BudgetMemoryPct     int
	BudgetCPUPct        int
	JobSlots            int
	LightSlots          int
	HeavySlots          int
	LightJobs           []string
	HeavyJobs           []string
	SlotMaxAgeMinutes   int
	GatePollSeconds     int
	GateMaxWaitMinutes  int
	GateOnTimeout       string
	AllowedBackends     []string
	CacheCapBytes       int64
	DrainTimeoutMinutes int
	WindowsSymlinkRight bool
	// PoolMemoryGiB and PoolCPUs are the owner's pool size, checked against the
	// load budget (R5.58). 0 means not set.
	PoolMemoryGiB float64
	PoolCPUs      int
	// The machine policy (CRSP75, R5.61 to R5.64). An empty Mode is automatic and
	// a zero CPUCapPct is 100.
	Mode           string
	PauseOnBattery bool
	// PauseUntil is a timed pause from the tray; the zero time means none.
	// PausedUntilResumed is a pause from the tray that ends at Resume.
	PauseUntil         time.Time
	PausedUntilResumed bool
	CPUCapPct          int
	// The Docker disk compact policy (caches R5.51 to R5.54, machines R5.66). An empty
	// mode is automatic; 0 and "" take the defaults of EffectiveCompact.
	CompactMode         string
	CompactThresholdGiB int
	CompactIdleMinutes  int
}

// Machine modes (R5.61).
const (
	ModeAutomatic = "automatic"
	ModeAlways    = "always"
	ModePaused    = "paused"
)

// Pause reasons a machine reports when it does not accept new jobs (R5.62).
// The names are read by other features: do not rename them.
const (
	PauseReasonPaused    = "paused"
	PauseReasonOnBattery = "on_battery"
	PauseReasonBudget    = "budget"
	// PauseReasonBaseImage: a backend the machine may use has no runner base
	// image (container image or VM base disk) on the machine (runners R5.74).
	PauseReasonBaseImage   = "base_image_missing"
	PauseReasonDiskLow     = "disk_low"
	PauseOneHour           = "one_hour"
	PauseUntilResumedValue = "until_resumed"
	PauseNone              = "none"
)

// CPUCaps are the CPU cap values in percent (R5.64).
var CPUCaps = []int{25, 50, 75, 100}

// EffectiveMode is the mode with the default filled in.
func (s Settings) EffectiveMode() string {
	if s.Mode == "" {
		return ModeAutomatic
	}
	return s.Mode
}

// EffectiveCPUCap is the CPU cap with the default filled in.
func (s Settings) EffectiveCPUCap() int {
	if s.CPUCapPct == 0 {
		return 100
	}
	return s.CPUCapPct
}

// UserPaused says whether the user holds the machine paused at now: the mode is
// paused, or a pause from the tray is running (R5.61).
func (s Settings) UserPaused(now time.Time) bool {
	if s.EffectiveMode() == ModePaused || s.PausedUntilResumed {
		return true
	}
	return !s.PauseUntil.IsZero() && now.Before(s.PauseUntil)
}

// DefaultSettings are the defaults of R5.22: budget 85% memory and 60% CPU,
// 2 slots, persistent runner only, 20 GiB cache cap, 60 minute drain timeout.
func DefaultSettings() Settings {
	return Settings{
		Version: 1, State: StateActive,
		BudgetMemoryPct: 85, BudgetCPUPct: 60, JobSlots: 2, LightSlots: 1, HeavySlots: 1,
		LightJobs: []string{}, HeavyJobs: []string{},
		SlotMaxAgeMinutes: 120, GatePollSeconds: 15, GateMaxWaitMinutes: 10, GateOnTimeout: "fail",
		AllowedBackends: []string{"persistent"}, CacheCapBytes: 20 << 30, DrainTimeoutMinutes: 60,
		Mode: ModeAutomatic, PauseOnBattery: true, CPUCapPct: 100,
	}
}

// SettingsUpdate is a partial change; nil fields stay as they are.
type SettingsUpdate struct {
	Name                *string
	BudgetMemoryPct     *int
	BudgetCPUPct        *int
	JobSlots            *int
	LightSlots          *int
	HeavySlots          *int
	LightJobs           *[]string
	HeavyJobs           *[]string
	GateOnTimeout       *string
	AllowedBackends     *[]string
	CacheCapBytes       *int64
	DrainTimeoutMinutes *int
	WindowsSymlinkRight *bool
	PoolMemoryGiB       *float64
	PoolCPUs            *int
	// Mode is automatic, always or paused (R5.61).
	Mode           *string
	PauseOnBattery *bool
	// CPUCapPct is 25, 50, 75 or 100 (R5.64).
	CPUCapPct *int
	// The Docker disk compact policy (caches R5.51 to R5.54): on, off or empty for
	// automatic; the threshold in GiB; idle minutes.
	CompactMode         *string
	CompactThresholdGiB *int
	CompactIdleMinutes  *int
	// Pause is one_hour, until_resumed or none: a pause from the tray (R5.61).
	Pause *string
	// ConfirmShrink is the explicit yes to a pool size below the current one
	// (R5.58). It is never implied by Confirm.
	ConfirmShrink bool
	// Confirm is the yes once the jobs a restart waits for are named.
	Confirm bool
}

// JobRef names one running job (R5.30).
type JobRef struct {
	Repository  string
	RunID       int64
	RunAttempt  int
	Job         string
	GitHubJobID int64
	RunnerName  string
	StartedAt   time.Time
}

// Load is the newest load report of a machine.
type Load struct {
	MemoryPct     float64
	MemoryKnown   bool
	CPUPct        float64
	CPUKnown      bool
	UnknownReason string
	SlotsUsed     int
	// WSLVMBytes and ContainersBytes are what the agent reports for the WSL VM
	// and the containers (R5.17); they feed the pool-size check.
	WSLVMBytes      int64
	ContainersBytes int64
	// HostUseP95Bytes is the rolling 7-day p95 of the memory the PC uses
	// without the pool; HostUseReady is true once the agent has 24 h of
	// samples and HostUseSpan is how long they cover (OPS61, R5.68).
	HostUseP95Bytes int64
	HostUseReady    bool
	HostUseSpan     time.Duration
	SampledAt       time.Time
	// ReceivedAt is controller time, used for the stale mark (R6.7).
	ReceivedAt time.Time
}

// HostMeasured is true when the agent's host-use p95 counts: it says so and its
// samples cover the 24 hours the p95 needs (OPS61).
func (l Load) HostMeasured() bool {
	return l.HostUseReady && l.HostUseSpan >= poolsize.MeasuredMinHours*time.Hour
}

// LoadPoint is one stored sample (R6.8).
type LoadPoint struct {
	At        time.Time
	MemoryPct *float64
	CPUPct    *float64
	SlotsUsed int
}

// Change is a change waiting for a drain (R5.30 to R5.33).
type Change struct {
	ID         string
	MachineID  string
	Kind       string
	State      string
	Detail     ChangeDetail
	StartedBy  string
	StartedAt  time.Time
	Deadline   time.Time
	FinishedAt time.Time
}

// ChangeDetail is what a change carries.
type ChangeDetail struct {
	// Settings is the settings the change applies when the machine is idle.
	Settings *Settings `json:"settings,omitempty"`
	// Base is the settings the change was made from. When the change runs only the
	// fields that differ between Base and Settings are written, so a setting the
	// user changed while the change waited is not undone (RES6).
	Base   *Settings `json:"base,omitempty"`
	Reason string    `json:"reason,omitempty"`
	// WaitingFor names the jobs it waits for now.
	WaitingFor []JobRef `json:"waiting_for,omitempty"`
	// RunnerNames are the runners the change touches (R5.57). Empty means every
	// runner of the machine. The drain waits only for jobs on these runners.
	RunnerNames []string `json:"runner_names,omitempty"`
	// Holder is the job that holds the drain longest (R5.55).
	Holder *Holder `json:"holder,omitempty"`
	// StuckAlerted is true once the owner was told that the holder runs far past
	// its usual duration, so the alert goes out once (R5.56).
	StuckAlerted bool `json:"stuck_alerted,omitempty"`
	// OverdueSince is when the drain timeout passed with jobs still running.
	// The change keeps waiting; nothing is stopped (R5.33).
	OverdueSince time.Time `json:"overdue_since,omitempty"`
}

// Holder is the job a drain waits for longest, with its usual duration from job
// history (R5.55). Usual is zero when there is no history for the job.
type Holder struct {
	Job       JobRef        `json:"job"`
	Usual     time.Duration `json:"usual,omitempty"`
	OverUsual bool          `json:"over_usual,omitempty"`
}

// CodeKind is a join code kind.
type CodeKind string

// Code kinds: a code from the install command, and the credential the
// controller writes for its own agent (R5.1).
const (
	CodeKindCode  CodeKind = "code"
	CodeKindLocal CodeKind = "local"
)

// JoinCode is a one-time code. Only its hash is kept (R6.2).
type JoinCode struct {
	ID        string
	Hash      string
	Kind      CodeKind
	CreatedBy string
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    time.Time
	RevokedAt time.Time
	MachineID string
}

// CodeState is the state the wizard polls (R5.4).
type CodeState string

// Join code states.
const (
	CodeWaiting CodeState = "waiting"
	CodeJoined  CodeState = "joined"
	CodeExpired CodeState = "expired"
	CodeRevoked CodeState = "revoked"
)

// State computes the state at time now.
func (c JoinCode) State(now time.Time) CodeState {
	switch {
	case !c.UsedAt.IsZero():
		return CodeJoined
	case !c.RevokedAt.IsZero():
		return CodeRevoked
	case !now.Before(c.ExpiresAt):
		return CodeExpired
	}
	return CodeWaiting
}
