package timers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// ServiceStopper is the agent's stop-runner command: the controller's own
// machine stops the runner's service or container directly, any other machine
// through the agent link (agent.proto StopRunner). *agentlink.Agent and the
// runners package's agent both have it.
type ServiceStopper interface {
	StopService(ctx context.Context, machineID, service string) error
}

// AgentAdapter stops a job's runner through the agent. The runner ref is the
// service name or container the agent started for the job; stopping it ends
// that job and no other.
type AgentAdapter struct{ S ServiceStopper }

// StopJobRunner implements Agent.
func (a AgentAdapter) StopJobRunner(ctx context.Context, machineID, runnerRef string) error {
	if runnerRef == "" {
		return fmt.Errorf("the job has no runner to stop")
	}
	return a.S.StopService(ctx, machineID, runnerRef)
}

// RunCanceller cancels a run through GitHub: *github.REST.CancelWorkflowRun.
type RunCanceller interface {
	CancelWorkflowRun(ctx context.Context, installationID int64, owner, repo string, runID int64) error
}

// GitHubAdapter cancels a run by its repository's full name. Installation finds
// the App installation of the repository.
type GitHubAdapter struct {
	C            RunCanceller
	Installation func(ctx context.Context, repo string) (int64, error)
}

// CancelRun implements GitHub.
func (g GitHubAdapter) CancelRun(ctx context.Context, repo string, runID int64) error {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		return fmt.Errorf("%q is not owner/name", repo)
	}
	id, err := g.Installation(ctx, repo)
	if err != nil {
		return err
	}
	return g.C.CancelWorkflowRun(ctx, id, owner, name, runID)
}

// AuditAdapter writes timer events to the audit log. The actor of "system" is
// crsp itself; any other is the login of the owner who pressed the button.
type AuditAdapter struct{ R audit.Recorder }

// Record implements Audit.
func (a AuditAdapter) Record(ctx context.Context, e Event) error {
	actor := audit.Actor{Login: e.Actor, Source: audit.SourceWeb}
	if e.Actor == ActorSystem || e.Actor == "" {
		actor = audit.System
	}
	target := audit.Target{Type: "run", ID: fmt.Sprintf("%s#%d", e.Run.Repo, e.Run.RunID)}
	details := map[string]any{"summary": e.Text, "repository": e.Run.Repo, "run_id": e.Run.RunID, "attempt": e.Run.Attempt}
	if e.Job != nil {
		target = audit.Target{Type: "job", ID: fmt.Sprintf("%d", e.Job.JobID)}
		details["job_id"] = e.Job.JobID
	}
	return a.R.Record(ctx, actor, e.Action, target, details)
}

// StoreAdapter keeps the clocks in the store (migration 00039).
type StoreAdapter struct{ DB *store.Store }

const layout = time.RFC3339

func ts(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(layout)
}

func parseTS(s string) time.Time {
	t, _ := time.Parse(layout, s)
	return t
}

func jobRow(c JobClock) store.JobClockRow {
	return store.JobClockRow{JobID: c.Key.JobID, Repo: c.Key.Repo, RunID: c.Key.RunID, RunAttempt: c.Key.Attempt,
		StartedAt: ts(c.StartedAt), LimitSec: int(c.Limit / time.Second), Extended: c.Extended, State: string(c.State),
		Deadline: ts(c.Deadline), Note: c.Note, MachineID: c.MachineID, RunnerRef: c.RunnerRef,
		QueuedMs: c.Queued.Milliseconds(), StartupMs: c.Startup.Milliseconds(), HoldMs: c.Hold.Milliseconds(),
		RunMs: c.Run.Milliseconds(), JobTimerMs: c.JobTimer.Milliseconds(), RunTimerMs: c.RunTimer.Milliseconds()}
}

func jobClock(r store.JobClockRow) JobClock {
	ms := func(v int64) time.Duration { return time.Duration(v) * time.Millisecond }
	return JobClock{Key: JobKey{Repo: r.Repo, RunID: r.RunID, Attempt: r.RunAttempt, JobID: r.JobID},
		MachineID: r.MachineID, RunnerRef: r.RunnerRef, StartedAt: parseTS(r.StartedAt), Limit: time.Duration(r.LimitSec) * time.Second,
		Extended: r.Extended, State: State(r.State), Deadline: parseTS(r.Deadline), Note: r.Note,
		Queued: ms(r.QueuedMs), Startup: ms(r.StartupMs), Hold: ms(r.HoldMs), Run: ms(r.RunMs), JobTimer: ms(r.JobTimerMs), RunTimer: ms(r.RunTimerMs)}
}

func runRow(c RunClock) store.RunClockRow {
	return store.RunClockRow{Repo: c.Key.Repo, RunID: c.Key.RunID, RunAttempt: c.Key.Attempt,
		StartedAt: ts(c.StartedAt), LimitSec: int(c.Limit / time.Second), Extended: c.Extended, State: string(c.State),
		Deadline: ts(c.Deadline), Note: c.Note, TimerMs: c.Elapsed.Milliseconds()}
}

func runClock(r store.RunClockRow) RunClock {
	return RunClock{Key: RunKey{Repo: r.Repo, RunID: r.RunID, Attempt: r.RunAttempt},
		StartedAt: parseTS(r.StartedAt), Limit: time.Duration(r.LimitSec) * time.Second, Extended: r.Extended,
		State: State(r.State), Deadline: parseTS(r.Deadline), Note: r.Note, Elapsed: time.Duration(r.TimerMs) * time.Millisecond}
}

// SaveJob implements Store.
func (s StoreAdapter) SaveJob(ctx context.Context, c JobClock) error {
	return s.DB.SaveJobClock(ctx, jobRow(c))
}

// SaveRun implements Store.
func (s StoreAdapter) SaveRun(ctx context.Context, c RunClock) error {
	return s.DB.SaveRunClock(ctx, runRow(c))
}

// Open implements Store.
func (s StoreAdapter) Open(ctx context.Context) ([]JobClock, []RunClock, error) {
	js, err := s.DB.OpenJobClocks(ctx)
	if err != nil {
		return nil, nil, err
	}
	rs, err := s.DB.OpenRunClocks(ctx)
	if err != nil {
		return nil, nil, err
	}
	var jo []JobClock
	for _, r := range js {
		jo = append(jo, jobClock(r))
	}
	var ro []RunClock
	for _, r := range rs {
		ro = append(ro, runClock(r))
	}
	return jo, ro, nil
}

// LoadJob implements Store.
func (s StoreAdapter) LoadJob(ctx context.Context, k JobKey) (JobClock, bool, error) {
	r, ok, err := s.DB.JobClock(ctx, k.JobID)
	if err != nil || !ok {
		return JobClock{}, false, err
	}
	return jobClock(r), true, nil
}

// LoadRun implements Store.
func (s StoreAdapter) LoadRun(ctx context.Context, k RunKey) (RunClock, bool, error) {
	r, ok, err := s.DB.RunClock(ctx, 0, k.RunID, k.Attempt)
	if err != nil || !ok {
		return RunClock{}, false, err
	}
	return runClock(r), true, nil
}

// SettingsStore reads and saves the timer settings.
type SettingsStore struct{ DB *store.Store }

// Get returns the saved settings, or the defaults.
func (s SettingsStore) Get(ctx context.Context) Settings {
	v, _ := s.DB.TimerSettingsJSON(ctx)
	return Parse(v)
}

// Save validates and saves the settings. A refused set changes nothing.
func (s SettingsStore) Save(ctx context.Context, next Settings, by string) error {
	if p := next.Validate(); len(p) > 0 {
		return &ProblemError{Problems: p}
	}
	return s.DB.SaveTimerSettings(ctx, next.Encode(), by)
}

// ProblemError carries the refused fields of a save.
type ProblemError struct{ Problems []Problem }

func (e *ProblemError) Error() string {
	if len(e.Problems) == 0 {
		return "the timer settings are not valid"
	}
	return e.Problems[0].Message
}

// Hook adapts the engine to scheduler.Lifecycle: the gate's first go is the
// job's first real step, and the release of its slots is its end (scheduling
// R5.99). Calls go to a goroutine, because the scheduler must not wait on the
// store.
type Hook struct {
	E *Engine
	// RunnerRef maps a runner name to what the agent stops (the service name or
	// the container); nil uses the runner name as it is.
	RunnerRef func(machineID, runnerName string) string
	// LimitMin is the job's own timer from the repository's .crsp.yml; nil or 0
	// means the default.
	LimitMin func(repo, job string) int
	// Go runs f; nil starts a goroutine. Tests pass a direct call.
	Go func(f func())
	// Log gets what could not be done; nil drops it.
	Log func(msg string, err error)
}

var _ scheduler.Lifecycle = (*Hook)(nil)

func (h *Hook) run(f func()) {
	if h.Go != nil {
		h.Go(f)
		return
	}
	go f()
}

func (h *Hook) fail(msg string, err error) {
	if err != nil && h.Log != nil {
		h.Log(msg, err)
	}
}

func keyOf(j scheduler.JobRef) (JobKey, bool) {
	if j.GitHubJobID == 0 || j.Repository == "" || j.RunID == 0 {
		return JobKey{}, false
	}
	return JobKey{Repo: j.Repository, RunID: j.RunID, Attempt: j.RunAttempt, JobID: j.GitHubJobID}, true
}

// JobStarted implements scheduler.Lifecycle.
func (h *Hook) JobStarted(machineID string, job scheduler.JobRef, hold time.Duration) {
	k, ok := keyOf(job)
	if !ok {
		return
	}
	h.run(func() {
		ref := job.RunnerName
		if h.RunnerRef != nil {
			ref = h.RunnerRef(machineID, job.RunnerName)
		}
		limit := 0
		if h.LimitMin != nil {
			limit = h.LimitMin(job.Repository, job.Job)
		}
		h.fail("job timer not started", h.E.JobStarted(context.Background(), JobStart{Key: k, MachineID: machineID, RunnerRef: ref, LimitMin: limit, Hold: hold}))
	})
}

// JobEnded implements scheduler.Lifecycle.
func (h *Hook) JobEnded(_ string, job scheduler.JobRef) {
	k, ok := keyOf(job)
	if !ok {
		return
	}
	h.run(func() {
		if err := h.E.JobEnded(context.Background(), k); err != nil && !errors.Is(err, ErrUnknown) {
			h.fail("job timer not ended", err)
		}
	})
}
