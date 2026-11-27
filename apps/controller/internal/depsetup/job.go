package depsetup

import (
	"context"
	"time"
)

// Job states (API DependencyJob.state).
const (
	JobConsented     = "consented"
	JobInstalling    = "installing"
	JobPausedRestart = "paused_restart"
	JobVerifying     = "verifying"
	JobPreparing     = "preparing"
	JobDone          = "done"
	JobFailed        = "failed"
	JobStopped       = "stopped"
	JobUndoing       = "undoing"
	JobUndone        = "undone"
)

// Running says a job holds its machine: no second job starts while one is in it.
func Running(state string) bool {
	switch state {
	case JobConsented, JobInstalling, JobPausedRestart, JobVerifying, JobPreparing, JobUndoing:
		return true
	}
	return false
}

// LogCap is how many events a job keeps.
const LogCap = 300

// Added is one thing a job added (the undo record, D-DEP-7).
type Added struct {
	ItemID  string `json:"item_id"`
	Kind    string `json:"kind"`
	Ref     string `json:"ref"`
	Version string `json:"version"`
}

// Event is one progress event of a job (the shared progress contract).
type Event struct {
	JobID       string   `json:"job_id"`
	Phase       string   `json:"phase"`
	PhaseIndex  int      `json:"phase_index"`
	PhaseCount  int      `json:"phase_count"`
	PhaseWeight float64  `json:"phase_weight"`
	Pct         *float64 `json:"pct"`
	Reason      string   `json:"reason,omitempty"`
	Bytes       int64    `json:"bytes,omitempty"`
	Total       int64    `json:"total,omitempty"`
	Rate        int64    `json:"rate,omitempty"`
	EtaSec      int64    `json:"eta_sec,omitempty"`
	MachineName string   `json:"machine_name"`
	MachineRole string   `json:"machine_role"`
	MachineOS   string   `json:"machine_os"`
	Command     string   `json:"command,omitempty"`
	Stream      string   `json:"stream"`
	Line        string   `json:"line,omitempty"`
	Ts          int64    `json:"ts"`
}

// Job is one dependency setup run on one machine; it is persisted so a
// controller restart, an agent restart or a machine reboot never loses it.
type Job struct {
	ID            string
	MachineID     string
	State         string
	ChoiceID      string
	ItemIDs       []string
	RunnerOS      string
	RunnerKind    string
	Actor         string
	AcceptLicence bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Error         string
	EngineVersion string
	Added         []Added
	Last          *Event
	Log           []Event
	ManualSteps   []string
	// RestartSent is true once the user chose Restart now.
	RestartSent bool
}

// Repo keeps the jobs.
type Repo interface {
	// Put inserts or replaces a job by id.
	Put(ctx context.Context, j Job) error
	// Get returns one job of a machine; ok is false when there is none.
	Get(ctx context.Context, machineID, id string) (Job, bool, error)
	// List returns the jobs of a machine, newest first, at most limit.
	List(ctx context.Context, machineID string, limit int) ([]Job, error)
	// Unfinished returns the jobs of any machine in a running state.
	Unfinished(ctx context.Context, machineID string) ([]Job, error)
}
