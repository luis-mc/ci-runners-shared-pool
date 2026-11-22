package timers

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// State of a clock.
type State string

// The states (scheduling R5.100).
const (
	StateRunning State = "running"
	StateGrace   State = "grace"
	StateStopped State = "stopped"
	StateEnded   State = "ended"
)

// RunKey names one run attempt.
type RunKey struct {
	Repo    string // owner/name, as the scheduler names it
	RunID   int64
	Attempt int
}

// JobKey names one job of a run attempt. JobID is GitHub's job id.
type JobKey struct {
	Repo    string // owner/name
	RunID   int64
	Attempt int
	JobID   int64
}

// Run is the run the job belongs to.
func (k JobKey) Run() RunKey { return RunKey{k.Repo, k.RunID, k.Attempt} }

// Errors the actions answer with.
var (
	ErrUnknown      = errors.New("no such timer")
	ErrNotInGrace   = errors.New("the timer has not run out")
	ErrExtendedOnce = errors.New("the timer was extended once already")
	ErrCovered      = errors.New("the run timer ran out too: cancel the run instead")
)

// Agent stops one job's runner: the container or the process (scheduling R5.100).
// Only that job ends.
type Agent interface {
	StopJobRunner(ctx context.Context, machineID, runnerRef string) error
}

// GitHub cancels a whole run. There is no per-job cancel.
type GitHub interface {
	CancelRun(ctx context.Context, repo string, runID int64) error
}

// Event is one audit row of a timer (scheduling R5.102). Every timer action is
// one, and each shows on the job and run records.
type Event struct {
	Action string // timer.job.grace, timer.job.stop, ...
	Actor  string // "system" for crsp, else the login of the owner
	Job    *JobKey
	Run    RunKey
	Text   string // owner's words, one line
}

// Audit writes the events.
type Audit interface {
	Record(ctx context.Context, e Event) error
}

// Store keeps the clocks, so a restart of the controller loses none.
type Store interface {
	SaveJob(ctx context.Context, c JobClock) error
	SaveRun(ctx context.Context, c RunClock) error
	Open(ctx context.Context) ([]JobClock, []RunClock, error)
	LoadJob(ctx context.Context, k JobKey) (JobClock, bool, error)
	LoadRun(ctx context.Context, k RunKey) (RunClock, bool, error)
}

// JobClock is the stored form of a job's clock and the durations of its life
// (scheduling R5.105).
type JobClock struct {
	Key                  JobKey
	MachineID, RunnerRef string
	StartedAt            time.Time
	Limit                time.Duration
	Extended             bool
	State                State
	Deadline             time.Time // the end of the grace, zero outside it
	Note                 string
	Queued, Startup      time.Duration
	Hold, Run            time.Duration
	JobTimer, RunTimer   time.Duration // elapsed when the job ended
}

// RunClock is the stored form of a run's clock.
type RunClock struct {
	Key       RunKey
	StartedAt time.Time
	Limit     time.Duration
	Extended  bool
	State     State
	Deadline  time.Time
	Note      string
	Elapsed   time.Duration // when the run ended
}

// JobStart is what the hook path gives when a job's first real step begins.
type JobStart struct {
	Key                  JobKey
	MachineID, RunnerRef string
	// LimitMin is the job's own timer from .crsp.yml; 0 means the default.
	LimitMin        int
	Queued, Startup time.Duration
	Hold            time.Duration
}

// JobView is the state of a job's timer for the API.
type JobView struct {
	Key       JobKey
	State     State
	StartedAt time.Time
	Limit     time.Duration
	Elapsed   time.Duration
	// Deadline is the end of the grace; zero when the timer has not run out.
	Deadline time.Time
	// Text is the state in the owner's words, with the countdown.
	Text string
	// Note is what was done: "stopped by crsp: job timer 30:00".
	Note string
	// CanStopNow and CanExtend say which of the two actions the grace offers.
	CanStopNow, CanExtend bool
	Extended              bool
	// CoveredByRun is true when the run timer ran out too: the run is cancelled
	// and this job is not stopped on its own.
	CoveredByRun bool
}

// RunView is the state of a run's timer for the API.
type RunView struct {
	Key          RunKey
	State        State
	StartedAt    time.Time
	Limit        time.Duration
	Elapsed      time.Duration
	Deadline     time.Time
	Text, Note   string
	CanCancelNow bool
	CanExtend    bool
	Extended     bool
}

// QueuedItem is a job that waits in GitHub's queue. OPS80 gives the list.
type QueuedItem struct {
	Key      JobKey
	QueuedAt time.Time
}

// QueuedHandler alerts when a job waited longer than the queued timer, before
// GitHub's 24 h cancel (scheduling R5.103). OPS80 supplies it.
type QueuedHandler interface {
	QueuedTooLong(ctx context.Context, item QueuedItem, waited time.Duration) error
}

// StartupItem is a runner that took a job and has not started it. OPS80 gives
// the list.
type StartupItem struct {
	Key                  JobKey
	MachineID, RunnerRef string
	AssignedAt           time.Time
}

// StartupHandler acts when a runner did not start its job in time: remove the
// runner, admit the job again and alert (scheduling R5.103). OPS80 supplies it.
type StartupHandler interface {
	StartupTooLong(ctx context.Context, item StartupItem, waited time.Duration) error
}

func jobText(what string, limit time.Duration, left time.Duration, verb string) string {
	return fmt.Sprintf("%s timer ran out at %s; %s in %d s", what, mmss(limit), verb, int((left+time.Second-1)/time.Second))
}
