package listenergate

import (
	"context"
	"errors"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
)

// Timings of the gate.
const (
	// WakeEvery is how often a gated runner is woken: GitHub removes a persistent
	// runner that has not connected for more than 14 days.
	WakeEvery = 7 * 24 * time.Hour
	// WakeHold is how long a weekly wake keeps the listener on.
	WakeHold = time.Minute
	// StopRetry is the wait before a stop that failed is tried again.
	StopRetry = 30 * time.Second
)

// Audit actions of the gate (the audit log's action column).
const (
	ActionWouldStart = "listener.would_start"
	ActionWouldStop  = "listener.would_stop"
	ActionStart      = "listener.start"
	ActionStop       = "listener.stop"
	ActionWake       = "listener.wake"
	ActionFailed     = "listener.failed"
)

// ErrJobAssigned is returned by a stop that would cut off an assigned job.
var ErrJobAssigned = errors.New("a job is assigned to the runner, so its listener is not stopped")

// Settings are the gate's settings. Enforce is false by default.
type Settings struct {
	Enforce bool
}

// Runner is a gated persistent runner on a host, as the gate needs it.
type Runner struct {
	ID             string // the runners record id
	Name           string
	MachineID      string
	OS             string
	ServiceRef     string // what the agent starts and stops
	GitHubRunnerID int64
	InstallationID int64
	Scope          github.RunnerScope
}

// LeaseState is where the lease of one job is.
type LeaseState string

// The lease states.
const (
	// Observed: observe mode wrote would_start; nothing was changed.
	Observed LeaseState = "observed"
	// Started: the label is on and the listener was started.
	Started LeaseState = "started"
	// Stopping: the job ended; the listener or the label is not gone yet and the
	// sweep tries again.
	Stopping LeaseState = "stopping"
	// Finished: the listener was stopped (or kept for another job) and the label removed.
	Finished LeaseState = "finished"
	// Failed: the start failed; nothing is left on.
	Failed LeaseState = "failed"
)

// Open is true while the lease holds the listener. A stopping lease does not:
// two jobs that ended together must not each wait for the other.
func (s LeaseState) Open() bool { return s == Started }

// Lease is the one lease of a GitHub job id.
type Lease struct {
	JobID     int64
	Runner    Runner
	JobLabel  string
	State     LeaseState
	StartedAt time.Time
	UpdatedAt time.Time
}

// Store keeps the leases and the wake times.
type Store interface {
	// Claim stores l when no lease of its job id exists; it returns the lease
	// that is stored and whether it was just made (the compare-and-set).
	Claim(ctx context.Context, l Lease) (Lease, bool, error)
	Get(ctx context.Context, jobID int64) (Lease, bool, error)
	Save(ctx context.Context, l Lease) error
	// ListState lists the leases in one state.
	ListState(ctx context.Context, s LeaseState) ([]Lease, error)
	// OpenFor lists the open leases of one runner.
	OpenFor(ctx context.Context, runnerID string) ([]Lease, error)
	// LastWake is when the runner was last woken; the zero time when never.
	LastWake(ctx context.Context, runnerID string) (time.Time, error)
	// WakeStarted records that a weekly wake began at t; WakeEnded that it ended.
	WakeStarted(ctx context.Context, runnerID string, t time.Time) error
	WakeEnded(ctx context.Context, runnerID string) error
	// Waking lists the runner ids whose wake began and has not ended, with the start.
	Waking(ctx context.Context) (map[string]time.Time, error)
}

// Labels is the runner labels API (github.RunnerLabeler).
type Labels interface {
	AddRunnerLabels(ctx context.Context, installationID int64, scope github.RunnerScope, runnerID int64, labels []string) error
	RemoveRunnerLabel(ctx context.Context, installationID int64, scope github.RunnerScope, runnerID int64, label string) error
}

// ListenerState is what the agent saw.
type ListenerState string

// The listener states.
const (
	ListenerOffline ListenerState = "offline"
	ListenerOnline  ListenerState = "online"
	ListenerBusy    ListenerState = "busy"
	ListenerUnknown ListenerState = "unknown"
)

// Service starts, stops and reads the listener of a runner through the agent
// of its machine. A stop waits for a clean exit and is refused while a job is
// assigned (jobAssigned, or a worker running on the machine).
type Service interface {
	StartListener(ctx context.Context, machineID, ref string, maintenance bool) error
	StopListener(ctx context.Context, machineID, ref string, jobAssigned bool) error
	ListenerState(ctx context.Context, machineID, ref string) (ListenerState, error)
}

// Runners lists the gated runners of the gate.
type Runners interface {
	// Gated lists the persistent runners that run on a machine itself.
	Gated(ctx context.Context) ([]Runner, error)
}

// Event is one audit row.
type Event struct {
	Action string
	JobID  int64
	Runner string // the runner name
	Label  string
	Detail string
}

// Audit writes the events.
type Audit interface{ Record(ctx context.Context, e Event) error }
