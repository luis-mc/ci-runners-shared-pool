// Package scheduler is the slot queue seam: the controller side of the
// job-started gate and the job-completed hook (scheduling R5.6 to R5.39; owner
// decisions for M1 on queue order, fair share, priority and parallel limits).
//
// One queue per machine holds every job that waits for a slot there. The
// order of service is the configured order (default: repository priority,
// event class, fair share, queued time; R5.8, R5.9); a repository at its
// parallel limit is passed over, not the whole queue held (R5.10). A take is
// one transaction (R5.14); a slot is freed only with its claim (R5.15). The
// scheduler only holds new jobs: it never kills, suspends or signals a
// process (R5.27). Ages are judged on the controller clock (R6.4).
//
// The agent link (agent.proto GateRequest, GateBeat, GateCancel, JobCompleted,
// HolderReport) and the API (listSlots, freeStaleSlots) call SlotQueue.
package scheduler

import (
	"context"
	"time"
)

// JobRef identifies one job attempt (scheduling R5.28).
type JobRef struct {
	Repository  string // owner/name
	RunID       int64
	RunAttempt  int
	Job         string
	GitHubJobID int64
	Shard       string
	RunnerName  string
}

// Holder names the processes that hold a ticket or slot (R5.31).
type Holder struct {
	Machine       string
	HolderPID     int64
	GatePID       int64
	HolderStarted time.Time
}

// SlotKind is the slot class of R5.24 to R5.26.
type SlotKind string

// Slot kinds.
const (
	SlotCI    SlotKind = "ci"
	SlotLight SlotKind = "light"
	SlotHeavy SlotKind = "heavy"
)

// WaitReason is one of the fixed wait reasons (observability R5.5).
type WaitReason string

// Wait reasons.
const (
	WaitFreeSlot          WaitReason = "free_slot"
	WaitLoadOverBudget    WaitReason = "load_over_budget"
	WaitNoRunnerLabels    WaitReason = "no_runner_with_labels"
	WaitRepoParallelLimit WaitReason = "repository_parallel_limit"
	WaitMachineDrained    WaitReason = "machine_drained"
	WaitPaused            WaitReason = "paused"
	WaitOnBattery         WaitReason = "on_battery"
	WaitLostRace          WaitReason = "lost_race"
	// WaitLoadNotMeasured: the machine's load could not be measured, so the
	// gate holds the job (R5.22, owner decision D53).
	WaitLoadNotMeasured WaitReason = "load_not_measured"
	WaitUnknown         WaitReason = "unknown"
)

// GateRequest is a job-started hook asking for a slot (R5.12).
type GateRequest struct {
	GateID     string // chosen by the agent; names the ticket
	MachineID  string
	Job        JobRef
	EventClass int // 0 ready PR or default branch, 1 otherwise (R5.7)
	Holder     Holder
	// FirstQueued keeps the place of a ticket that was lost (R5.19); zero when new.
	FirstQueued time.Time
	// Load is the machine's newest load sample; the gate never waits for a
	// sample (machines R6.6).
	Load Load
	// Repository-lowered budget, if any; never above the machine's (R5.4).
	BudgetMemoryPct int
	BudgetCPUPct    int
}

// Load is a load sample as the agent reported it.
type Load struct {
	MemoryPct   float64
	MemoryKnown bool
	CPUPct      float64
	CPUKnown    bool
	SampledAt   time.Time
}

// Grant is a slot taken for a job.
type Grant struct {
	Kind    SlotKind
	Number  int
	Claim   string
	TakenAt time.Time
}

// Decision answers a gate request or beat: exactly one of Go, Wait, Timeout.
type Decision struct {
	Go       []Grant
	SlotWait time.Duration
	Wait     []WaitReason
	WaitText string
	Place    int
	// Timeout is "fail" or "run" when the maximum wait passed (R5.20).
	Timeout string
	// Class and MemoryCapGiB are the runner's class and the hard memory cap of
	// the job at its ceiling, set on a go when admission knows the runner's
	// class (R5.95). 0 means no cap.
	Class        string
	MemoryCapGiB int
}

// Freed is one slot or ticket freed, with its reason, for the slot log and
// the answer to freeStaleSlots (R5.37, R5.38).
type Freed struct {
	MachineID string
	Kind      SlotKind
	Number    int // 0 for a ticket
	Job       JobRef
	Reason    string
	TakenAt   time.Time // when the freed slot was taken; zero for a ticket
}

// AlertKind names a condition the scheduler reports to the owner.
type AlertKind string

// AlertJobStartedMissing: a job-completed hook arrived for a job that held no
// slot or ticket on the machine (R5.61).
const AlertJobStartedMissing AlertKind = "hook.job_started_missing"

// Alert is one condition the owner should see. Message holds no secret.
type Alert struct {
	Kind      AlertKind
	MachineID string
	Job       JobRef
	At        time.Time
	Message   string
}

// Status is the queue of one machine (R5.58).
type Status struct {
	MachineID string
	Slots     []HeldSlot
	Tickets   []Ticket
}

// HeldSlot is one held slot.
type HeldSlot struct {
	Grant
	Job         JobRef
	StaleReason string
}

// Ticket is one waiting job, with its place in the order of service.
type Ticket struct {
	GateID    string
	Job       JobRef
	Priority  int
	Class     int
	QueuedAt  time.Time
	BeatAt    time.Time
	ExpiresAt time.Time
	Place     int
	Reason    WaitReason
}

// SlotQueue is the controller's slot queue.
type SlotQueue interface {
	// Gate takes or keeps a ticket for the request and answers go, wait or
	// timeout. Before it checks the ticket it frees stale slots (R5.16).
	Gate(ctx context.Context, req GateRequest) (Decision, error)
	// Beat keeps a waiting ticket alive without changing its place, and answers
	// as Gate does. A beat never makes a ticket that is gone (R5.18).
	Beat(ctx context.Context, machineID, gateID string, load Load) (Decision, error)
	// Cancel drops a ticket at once (the hook went away).
	Cancel(ctx context.Context, machineID, gateID string) error
	// Release frees every slot and ticket the job holds, of every kind (R5.28).
	// It never fails the job: the caller logs an error and exits 0 (R5.30).
	Release(ctx context.Context, machineID string, job JobRef, claims []string) ([]Freed, error)
	// Renew extends the lease of the slots a holder holds, by holder id and claims
	// (R5.70); ErrLeaseLost when none is left.
	Renew(ctx context.Context, machineID, holderID string, claims []string) error
	// RenewJobs extends the lease of the slots of the jobs the agent says run on
	// the machine (R5.35, R5.70).
	RenewJobs(ctx context.Context, machineID string, running []JobRef) error
	// CompletedOnGitHub frees leftovers of a job GitHub reports completed (R5.36).
	CompletedOnGitHub(ctx context.Context, job JobRef) ([]Freed, error)
	// Sweep frees slots past the slot maximum age or lease end and drops expired tickets
	// (R5.33, R5.34).
	Sweep(ctx context.Context, now time.Time) ([]Freed, error)
	// FreeStale frees stale slots of a machine on demand; live slots named in
	// live are freed too (the caller asked for a yes, R5.37).
	FreeStale(ctx context.Context, machineID string, live []Grant) ([]Freed, error)
	// Status lists the slots and the waiting queue of a machine (R5.58).
	Status(ctx context.Context, machineID string) (Status, error)
}
