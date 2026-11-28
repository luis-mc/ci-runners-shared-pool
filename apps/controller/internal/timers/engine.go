package timers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Actor of what crsp does by itself.
const ActorSystem = "system"

// Audit actions of the timers (scheduling R5.102).
const (
	ActJobGrace        = "timer.job.grace"
	ActJobGraceCleared = "timer.job.grace_cleared"
	ActJobStop         = "timer.job.stop"
	ActJobStopNow      = "timer.job.stop_now"
	ActJobStopFailed   = "timer.job.stop_failed"
	ActJobExtend       = "timer.job.extend"
	ActRunGrace        = "timer.run.grace"
	ActRunGraceCleared = "timer.run.grace_cleared"
	ActRunCancel       = "timer.run.cancel"
	ActRunCancelNow    = "timer.run.cancel_now"
	ActRunCancelFailed = "timer.run.cancel_failed"
	ActRunExtend       = "timer.run.extend"
	// Observe mode (enforce off): what crsp would have done, written and not done.
	ActJobWouldStop   = "timer.job.would_stop"
	ActRunWouldCancel = "timer.run.would_cancel"
	ActQueuedAlert    = "timer.queued.alert"
	ActStartupExpired = "timer.startup.expired"
)

// ObserveSuffix ends the note of a timer that ran out in observe mode. A clock
// with this note has been reported and is not reported again, also after a
// restart of the controller.
const ObserveSuffix = "(timers in observe mode)"

func observedNote(note string) bool { return strings.HasSuffix(note, ObserveSuffix) }

// Config wires the engine. Store, Queued and Startup may be nil.
type Config struct {
	Settings func() Settings
	Clock    Clock
	Agent    Agent
	GitHub   GitHub
	Audit    Audit
	Store    Store
	Queued   QueuedHandler
	Startup  StartupHandler
}

type job struct {
	JobClock
	acting bool
}

type run struct {
	RunClock
	acting bool
}

// Engine runs the clocks. Its methods are safe to call from many goroutines;
// no call to the agent, GitHub, the audit log or the store is made while the
// engine's lock is held.
type Engine struct {
	cfg  Config
	mu   sync.Mutex
	jobs map[JobKey]*job
	runs map[RunKey]*run

	queuedDone  map[int64]bool
	startupDone map[int64]bool
}

// New returns an engine with no clocks. Call Restore to load the open ones.
func New(cfg Config) *Engine {
	if cfg.Settings == nil {
		cfg.Settings = Defaults
	}
	return &Engine{cfg: cfg, jobs: map[JobKey]*job{}, runs: map[RunKey]*run{},
		queuedDone: map[int64]bool{}, startupDone: map[int64]bool{}}
}

// Restore loads the clocks that were open when the controller stopped.
func (e *Engine) Restore(ctx context.Context) error {
	if e.cfg.Store == nil {
		return nil
	}
	js, rs, err := e.cfg.Store.Open(ctx)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, c := range js {
		e.jobs[c.Key] = &job{JobClock: c}
	}
	for _, c := range rs {
		e.runs[c.Key] = &run{RunClock: c}
	}
	return nil
}

// JobStarted starts the job's clock at its first real step, which is when the
// job-started hook returns, not at assignment (scheduling R5.99). The run
// clock starts with the first job of the run. A second call for the same job
// changes nothing.
func (e *Engine) JobStarted(ctx context.Context, st JobStart) error {
	now := e.cfg.Clock.Now()
	set := e.cfg.Settings()
	limit := set.Job()
	if st.LimitMin > 0 && st.LimitMin < set.RunMin {
		limit = time.Duration(st.LimitMin) * time.Minute
	}
	e.mu.Lock()
	if _, ok := e.jobs[st.Key]; ok {
		e.mu.Unlock()
		return nil
	}
	j := &job{JobClock: JobClock{Key: st.Key, MachineID: st.MachineID, RunnerRef: st.RunnerRef, StartedAt: now,
		Limit: limit, State: StateRunning, Queued: st.Queued, Startup: st.Startup, Hold: st.Hold}}
	e.jobs[st.Key] = j
	var newRun *RunClock
	if _, ok := e.runs[st.Key.Run()]; !ok {
		r := &run{RunClock: RunClock{Key: st.Key.Run(), StartedAt: now, Limit: set.Run(), State: StateRunning}}
		e.runs[r.Key] = r
		c := r.RunClock
		newRun = &c
	}
	jc := j.JobClock
	e.mu.Unlock()
	var errs []error
	if e.cfg.Store != nil {
		errs = append(errs, e.cfg.Store.SaveJob(ctx, jc))
		if newRun != nil {
			errs = append(errs, e.cfg.Store.SaveRun(ctx, *newRun))
		}
	}
	return errors.Join(errs...)
}

// JobEnded ends the job's clock. A grace that was running is cleared and
// nothing is stopped (scheduling R5.100).
func (e *Engine) JobEnded(ctx context.Context, k JobKey) error {
	now := e.cfg.Clock.Now()
	e.mu.Lock()
	j, ok := e.jobs[k]
	if !ok {
		e.mu.Unlock()
		return ErrUnknown
	}
	wasGrace := j.State == StateGrace
	j.JobTimer = now.Sub(j.StartedAt)
	if r, ok := e.runs[k.Run()]; ok {
		j.RunTimer = now.Sub(r.StartedAt)
	}
	j.Run = j.JobTimer
	if j.State != StateStopped {
		j.State = StateEnded
	}
	j.Deadline = time.Time{}
	delete(e.jobs, k)
	jc := j.JobClock
	e.mu.Unlock()
	var errs []error
	if wasGrace {
		errs = append(errs, e.audit(ctx, Event{Action: ActJobGraceCleared, Actor: ActorSystem, Job: &k, Run: k.Run(),
			Text: "The job ended during the grace; nothing was stopped."}))
	}
	if e.cfg.Store != nil {
		errs = append(errs, e.cfg.Store.SaveJob(ctx, jc))
	}
	return errors.Join(errs...)
}

// RunEnded ends the run's clock, when the last job of the run has ended.
func (e *Engine) RunEnded(ctx context.Context, k RunKey) error {
	now := e.cfg.Clock.Now()
	e.mu.Lock()
	r, ok := e.runs[k]
	if !ok {
		e.mu.Unlock()
		return ErrUnknown
	}
	wasGrace := r.State == StateGrace
	r.Elapsed = now.Sub(r.StartedAt)
	if r.State != StateStopped {
		r.State = StateEnded
	}
	r.Deadline = time.Time{}
	delete(e.runs, k)
	rc := r.RunClock
	e.mu.Unlock()
	var errs []error
	if wasGrace {
		errs = append(errs, e.audit(ctx, Event{Action: ActRunGraceCleared, Actor: ActorSystem, Run: k,
			Text: "The run ended during the grace; nothing was cancelled."}))
	}
	if e.cfg.Store != nil {
		errs = append(errs, e.cfg.Store.SaveRun(ctx, rc))
	}
	return errors.Join(errs...)
}

// Tick moves every clock on: a timer that ran out starts its grace, a grace
// that ended acts. It is called by Watch, and by tests with a fake clock.
func (e *Engine) Tick(ctx context.Context) error {
	now := e.cfg.Clock.Now()
	var events []Event
	var saveJobs []JobClock
	var saveRuns []RunClock
	var stopJobs []*job
	var cancelRuns []*run

	enforce := e.cfg.Settings().Enforce
	e.mu.Lock()
	for _, r := range e.runs {
		switch {
		case r.State == StateRunning && !observedNote(r.Note) && !now.Before(r.StartedAt.Add(r.Limit)):
			r.State = StateGrace
			r.Deadline = r.StartedAt.Add(r.Limit).Add(Grace)
			events = append(events, Event{Action: ActRunGrace, Actor: ActorSystem, Run: r.Key,
				Text: fmt.Sprintf("Run timer ran out at %s; %s in %d s.", mmss(r.Limit), runVerb(enforce), int(Grace/time.Second))})
			saveRuns = append(saveRuns, r.RunClock)
		}
	}
	for _, r := range e.runs {
		if r.State == StateGrace && !r.acting && !now.Before(r.Deadline) {
			if !enforce {
				// Observe mode: report, send nothing. The clock goes back to running
				// with the note, so the report is made once.
				r.State = StateRunning
				r.Deadline = time.Time{}
				r.Note = fmt.Sprintf("would cancel at %s %s", mmss(r.Limit), ObserveSuffix)
				events = append(events, Event{Action: ActRunWouldCancel, Actor: ActorSystem, Run: r.Key, Text: r.Note})
				saveRuns = append(saveRuns, r.RunClock)
				continue
			}
			r.acting = true
			cancelRuns = append(cancelRuns, r)
		}
	}
	for _, j := range e.jobs {
		if j.State == StateRunning && !observedNote(j.Note) && !now.Before(j.StartedAt.Add(j.Limit)) {
			j.State = StateGrace
			j.Deadline = j.StartedAt.Add(j.Limit).Add(Grace)
			k := j.Key
			events = append(events, Event{Action: ActJobGrace, Actor: ActorSystem, Job: &k, Run: k.Run(),
				Text: fmt.Sprintf("Job timer ran out at %s; %s in %d s.", mmss(j.Limit), jobVerb(enforce), int(Grace/time.Second))})
			saveJobs = append(saveJobs, j.JobClock)
		}
	}
	for _, j := range e.jobs {
		if j.State == StateGrace && !j.acting && !now.Before(j.Deadline) && !e.coveredLocked(j) {
			if !enforce {
				j.State = StateRunning
				j.Deadline = time.Time{}
				j.Note = fmt.Sprintf("would stop at %s %s", mmss(j.Limit), ObserveSuffix)
				k := j.Key
				events = append(events, Event{Action: ActJobWouldStop, Actor: ActorSystem, Job: &k, Run: k.Run(), Text: j.Note})
				saveJobs = append(saveJobs, j.JobClock)
				continue
			}
			j.acting = true
			stopJobs = append(stopJobs, j)
		}
	}
	e.mu.Unlock()

	var errs []error
	for _, ev := range events {
		errs = append(errs, e.audit(ctx, ev))
	}
	if e.cfg.Store != nil {
		for _, c := range saveJobs {
			errs = append(errs, e.cfg.Store.SaveJob(ctx, c))
		}
		for _, c := range saveRuns {
			errs = append(errs, e.cfg.Store.SaveRun(ctx, c))
		}
	}
	// A run that is cancelled is cancelled alone: its jobs are covered.
	for _, r := range cancelRuns {
		errs = append(errs, e.cancelRun(ctx, r, ActorSystem, ActRunCancel))
	}
	for _, j := range stopJobs {
		errs = append(errs, e.stopJob(ctx, j, ActorSystem, ActJobStop))
	}
	return errors.Join(errs...)
}

// coveredLocked: the run timer ran out as well, so the run is cancelled and the
// job is not stopped by itself (scheduling R5.100). The caller holds the lock.
func (e *Engine) coveredLocked(j *job) bool {
	r, ok := e.runs[j.Key.Run()]
	return ok && (r.State == StateGrace || r.State == StateStopped)
}

func jobVerb(enforce bool) string {
	if enforce {
		return "stopping"
	}
	return "would stop"
}

func runVerb(enforce bool) string {
	if enforce {
		return "cancelling"
	}
	return "would cancel"
}

func (e *Engine) audit(ctx context.Context, ev Event) error {
	if e.cfg.Audit == nil {
		return nil
	}
	return e.cfg.Audit.Record(ctx, ev)
}

// stopJob stops one job's runner through the agent and records it. The job is
// marked acting by the caller.
func (e *Engine) stopJob(ctx context.Context, j *job, actor, action string) error {
	k := j.Key
	err := e.cfg.Agent.StopJobRunner(ctx, j.MachineID, j.RunnerRef)
	now := e.cfg.Clock.Now()
	e.mu.Lock()
	j.acting = false
	if err != nil {
		// Try again after another grace; the job stays in its grace.
		j.State = StateGrace
		j.Deadline = now.Add(Grace)
		jc := j.JobClock
		e.mu.Unlock()
		return errors.Join(e.audit(ctx, Event{Action: ActJobStopFailed, Actor: ActorSystem, Job: &k, Run: k.Run(),
			Text: "Crsp could not stop the job's runner; it tries again in 30 s."}), e.saveJob(ctx, jc), err)
	}
	j.State = StateStopped
	j.Note = fmt.Sprintf("stopped by crsp: job timer %s", mmss(j.Limit))
	j.Deadline = time.Time{}
	jc := j.JobClock
	e.mu.Unlock()
	return errors.Join(e.audit(ctx, Event{Action: action, Actor: actor, Job: &k, Run: k.Run(), Text: jc.Note}), e.saveJob(ctx, jc))
}

func (e *Engine) saveJob(ctx context.Context, c JobClock) error {
	if e.cfg.Store == nil {
		return nil
	}
	return e.cfg.Store.SaveJob(ctx, c)
}

func (e *Engine) saveRun(ctx context.Context, c RunClock) error {
	if e.cfg.Store == nil {
		return nil
	}
	return e.cfg.Store.SaveRun(ctx, c)
}

// cancelRun cancels the whole run through GitHub. The run is marked acting by
// the caller.
func (e *Engine) cancelRun(ctx context.Context, r *run, actor, action string) error {
	err := e.cfg.GitHub.CancelRun(ctx, r.Key.Repo, r.Key.RunID)
	now := e.cfg.Clock.Now()
	e.mu.Lock()
	r.acting = false
	if err != nil {
		r.State = StateGrace
		r.Deadline = now.Add(Grace)
		rc := r.RunClock
		e.mu.Unlock()
		return errors.Join(e.audit(ctx, Event{Action: ActRunCancelFailed, Actor: ActorSystem, Run: r.Key,
			Text: "Crsp could not cancel the run; it tries again in 30 s."}), e.saveRun(ctx, rc), err)
	}
	r.State = StateStopped
	r.Note = fmt.Sprintf("cancelled by crsp: run timer %s", mmss(r.Limit))
	r.Deadline = time.Time{}
	rc := r.RunClock
	e.mu.Unlock()
	return errors.Join(e.audit(ctx, Event{Action: action, Actor: actor, Run: r.Key, Text: rc.Note}), e.saveRun(ctx, rc))
}

// StopNow stops a job in its grace at once. The audit row is written first, and
// the job is not touched when it cannot be (scheduling R5.102).
func (e *Engine) StopNow(ctx context.Context, k JobKey, actor string) error {
	e.mu.Lock()
	j, ok := e.jobs[k]
	switch {
	case !ok:
		e.mu.Unlock()
		return ErrUnknown
	case j.State != StateGrace || j.acting:
		e.mu.Unlock()
		return ErrNotInGrace
	case e.coveredLocked(j):
		e.mu.Unlock()
		return ErrCovered
	}
	j.acting = true
	e.mu.Unlock()
	if err := e.audit(ctx, Event{Action: ActJobStopNow, Actor: actor, Job: &k, Run: k.Run(), Text: "Stop now was used."}); err != nil {
		e.mu.Lock()
		j.acting = false
		e.mu.Unlock()
		return err
	}
	// The stop row names who stopped it, the row above says how.
	return e.stopJob(ctx, j, actor, ActJobStop)
}

// CancelNow cancels a run in its grace at once.
func (e *Engine) CancelNow(ctx context.Context, k RunKey, actor string) error {
	e.mu.Lock()
	r, ok := e.runs[k]
	switch {
	case !ok:
		e.mu.Unlock()
		return ErrUnknown
	case r.State != StateGrace || r.acting:
		e.mu.Unlock()
		return ErrNotInGrace
	}
	r.acting = true
	e.mu.Unlock()
	if err := e.audit(ctx, Event{Action: ActRunCancelNow, Actor: actor, Run: k, Text: "Cancel now was used."}); err != nil {
		e.mu.Lock()
		r.acting = false
		e.mu.Unlock()
		return err
	}
	return e.cancelRun(ctx, r, actor, ActRunCancel)
}

// Extend adds 10 minutes to a job in its grace, once per job. The timer runs on
// from now, so the job gets the full 10 minutes.
func (e *Engine) Extend(ctx context.Context, k JobKey, actor string) error {
	now := e.cfg.Clock.Now()
	e.mu.Lock()
	j, ok := e.jobs[k]
	switch {
	case !ok:
		e.mu.Unlock()
		return ErrUnknown
	case j.State != StateGrace || j.acting:
		e.mu.Unlock()
		return ErrNotInGrace
	case j.Extended:
		e.mu.Unlock()
		return ErrExtendedOnce
	}
	j.acting = true
	e.mu.Unlock()
	text := fmt.Sprintf("Extend 10 min was used at %s; the job timer now ends at %s.", mmss(now.Sub(j.StartedAt)), mmss(now.Sub(j.StartedAt)+ExtendBy))
	if err := e.audit(ctx, Event{Action: ActJobExtend, Actor: actor, Job: &k, Run: k.Run(), Text: text}); err != nil {
		e.mu.Lock()
		j.acting = false
		e.mu.Unlock()
		return err
	}
	e.mu.Lock()
	j.acting = false
	j.Limit = now.Sub(j.StartedAt) + ExtendBy
	j.Extended = true
	j.State = StateRunning
	j.Deadline = time.Time{}
	jc := j.JobClock
	e.mu.Unlock()
	return e.saveJob(ctx, jc)
}

// ExtendRun adds 10 minutes to a run in its grace, once per run.
func (e *Engine) ExtendRun(ctx context.Context, k RunKey, actor string) error {
	now := e.cfg.Clock.Now()
	e.mu.Lock()
	r, ok := e.runs[k]
	switch {
	case !ok:
		e.mu.Unlock()
		return ErrUnknown
	case r.State != StateGrace || r.acting:
		e.mu.Unlock()
		return ErrNotInGrace
	case r.Extended:
		e.mu.Unlock()
		return ErrExtendedOnce
	}
	r.acting = true
	e.mu.Unlock()
	text := fmt.Sprintf("Extend 10 min was used at %s; the run timer now ends at %s.", mmss(now.Sub(r.StartedAt)), mmss(now.Sub(r.StartedAt)+ExtendBy))
	if err := e.audit(ctx, Event{Action: ActRunExtend, Actor: actor, Run: k, Text: text}); err != nil {
		e.mu.Lock()
		r.acting = false
		e.mu.Unlock()
		return err
	}
	e.mu.Lock()
	r.acting = false
	r.Limit = now.Sub(r.StartedAt) + ExtendBy
	r.Extended = true
	r.State = StateRunning
	r.Deadline = time.Time{}
	rc := r.RunClock
	e.mu.Unlock()
	return e.saveRun(ctx, rc)
}

// Watch ticks every interval until ctx ends.
func (e *Engine) Watch(ctx context.Context, interval time.Duration, onErr func(error)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := e.Tick(ctx); err != nil && onErr != nil {
				onErr(err)
			}
		}
	}
}

// Job returns the state of one job's timer, from memory or, for a job that has
// ended, from the store.
func (e *Engine) Job(ctx context.Context, k JobKey) (JobView, bool) {
	now := e.cfg.Clock.Now()
	e.mu.Lock()
	if j, ok := e.jobs[k]; ok {
		v := e.jobViewLocked(j, now)
		e.mu.Unlock()
		return v, true
	}
	e.mu.Unlock()
	if e.cfg.Store != nil {
		if c, ok, err := e.cfg.Store.LoadJob(ctx, k); err == nil && ok {
			return jobView(c, now, false, false, nil, true), true
		}
	}
	return JobView{}, false
}

// jobViewLocked is the view of a job held in memory; the caller holds e.mu.
func (e *Engine) jobViewLocked(j *job, now time.Time) JobView {
	covered := e.coveredLocked(j)
	var rv *RunView
	if r, ok := e.runs[j.Key.Run()]; ok && covered {
		v := runView(r.RunClock, now, e.cfg.Settings().Enforce)
		rv = &v
	}
	return jobView(j.JobClock, now, covered, j.acting, rv, e.cfg.Settings().Enforce)
}

// JobsByID is JobByID for many jobs at once, from memory only and under one lock. The jobs that are open are all in
// memory (the engine loads them at start), so a list of running jobs needs no store read; an id that is not held
// is left out of the answer.
func (e *Engine) JobsByID(ids []int64) map[int64]JobView {
	want := make(map[int64]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	out := make(map[int64]JobView, len(ids))
	now := e.cfg.Clock.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	for k, j := range e.jobs {
		if want[k.JobID] {
			out[k.JobID] = e.jobViewLocked(j, now)
		}
	}
	return out
}

// Run returns the state of one run's timer.
func (e *Engine) Run(ctx context.Context, k RunKey) (RunView, bool) {
	now := e.cfg.Clock.Now()
	e.mu.Lock()
	if r, ok := e.runs[k]; ok {
		v := runView(r.RunClock, now, e.cfg.Settings().Enforce)
		v.CanCancelNow = v.CanCancelNow && !r.acting
		e.mu.Unlock()
		return v, true
	}
	e.mu.Unlock()
	if e.cfg.Store != nil {
		if c, ok, err := e.cfg.Store.LoadRun(ctx, k); err == nil && ok {
			return runView(c, now, true), true
		}
	}
	return RunView{}, false
}

func jobView(c JobClock, now time.Time, covered, acting bool, rv *RunView, enforce bool) JobView {
	v := JobView{Key: c.Key, State: c.State, StartedAt: c.StartedAt, Limit: c.Limit, Note: c.Note, Extended: c.Extended, CoveredByRun: covered}
	switch c.State {
	case StateRunning:
		v.Elapsed = now.Sub(c.StartedAt)
		v.Text = fmt.Sprintf("Job timer %s of %s", mmss(v.Elapsed), mmss(c.Limit))
		if observedNote(c.Note) {
			v.Text += "; " + c.Note
		}
	case StateGrace:
		v.Elapsed = now.Sub(c.StartedAt)
		v.Deadline = c.Deadline
		v.Text = jobText("Job", c.Limit, c.Deadline.Sub(now), jobVerb(enforce))
		if covered && rv != nil {
			v.Text = rv.Text
		}
		v.CanStopNow = !covered && !acting
		v.CanExtend = !c.Extended && !acting
	case StateStopped:
		v.Elapsed = c.JobTimer
		v.Text = c.Note
	default:
		v.Elapsed = c.JobTimer
		v.Text = fmt.Sprintf("Job ended after %s", mmss(c.JobTimer))
	}
	return v
}

func runView(c RunClock, now time.Time, enforce bool) RunView {
	v := RunView{Key: c.Key, State: c.State, StartedAt: c.StartedAt, Limit: c.Limit, Note: c.Note, Extended: c.Extended}
	switch c.State {
	case StateRunning:
		v.Elapsed = now.Sub(c.StartedAt)
		v.Text = fmt.Sprintf("Run timer %s of %s", mmss(v.Elapsed), mmss(c.Limit))
		if observedNote(c.Note) {
			v.Text += "; " + c.Note
		}
	case StateGrace:
		v.Elapsed = now.Sub(c.StartedAt)
		v.Deadline = c.Deadline
		v.Text = jobText("Run", c.Limit, c.Deadline.Sub(now), runVerb(enforce))
		v.CanCancelNow = true
		v.CanExtend = !c.Extended
	case StateStopped:
		v.Elapsed = c.Elapsed
		v.Text = c.Note
	default:
		v.Elapsed = c.Elapsed
		v.Text = fmt.Sprintf("Run ended after %s", mmss(c.Elapsed))
	}
	return v
}

// SweepQueued alerts once for each job that waited longer than the queued
// timer. OPS80's queue code calls it with the jobs that wait.
func (e *Engine) SweepQueued(ctx context.Context, items []QueuedItem) error {
	if e.cfg.Queued == nil {
		return nil
	}
	now := e.cfg.Clock.Now()
	limit := e.cfg.Settings().Queued()
	var errs []error
	for _, it := range items {
		waited := now.Sub(it.QueuedAt)
		e.mu.Lock()
		done := e.queuedDone[it.Key.JobID]
		e.mu.Unlock()
		if waited < limit || done {
			continue
		}
		if err := e.cfg.Queued.QueuedTooLong(ctx, it, waited); err != nil {
			errs = append(errs, err)
			continue
		}
		e.mu.Lock()
		e.queuedDone[it.Key.JobID] = true
		e.mu.Unlock()
		k := it.Key
		errs = append(errs, e.audit(ctx, Event{Action: ActQueuedAlert, Actor: ActorSystem, Job: &k, Run: k.Run(),
			Text: fmt.Sprintf("The job has waited %s in GitHub's queue; GitHub cancels it at 24 h.", mmss(waited))}))
	}
	return errors.Join(errs...)
}

// SweepStartup acts once for each runner that did not start its job within the
// start-up timer. OPS80's assignment code calls it.
func (e *Engine) SweepStartup(ctx context.Context, items []StartupItem) error {
	if e.cfg.Startup == nil {
		return nil
	}
	now := e.cfg.Clock.Now()
	limit := e.cfg.Settings().Startup()
	var errs []error
	for _, it := range items {
		waited := now.Sub(it.AssignedAt)
		e.mu.Lock()
		done := e.startupDone[it.Key.JobID]
		e.mu.Unlock()
		if waited < limit || done {
			continue
		}
		if err := e.cfg.Startup.StartupTooLong(ctx, it, waited); err != nil {
			errs = append(errs, err)
			continue
		}
		e.mu.Lock()
		e.startupDone[it.Key.JobID] = true
		e.mu.Unlock()
		k := it.Key
		errs = append(errs, e.audit(ctx, Event{Action: ActStartupExpired, Actor: ActorSystem, Job: &k, Run: k.Run(),
			Text: fmt.Sprintf("The runner did not start the job in %s; it is removed and the job is admitted again.", mmss(limit))}))
	}
	return errors.Join(errs...)
}

// JobByID is Job for a caller that has only GitHub's job id.
func (e *Engine) JobByID(ctx context.Context, id int64) (JobView, bool) {
	e.mu.Lock()
	for k := range e.jobs {
		if k.JobID == id {
			e.mu.Unlock()
			return e.Job(ctx, k)
		}
	}
	e.mu.Unlock()
	return e.Job(ctx, JobKey{JobID: id})
}

// RunByID is Run for a caller that has only GitHub's run id and the attempt
// (0 means the newest open one, or attempt 1 for a stored one).
func (e *Engine) RunByID(ctx context.Context, runID int64, attempt int) (RunView, bool) {
	e.mu.Lock()
	for k := range e.runs {
		if k.RunID == runID && (attempt == 0 || k.Attempt == attempt) {
			e.mu.Unlock()
			return e.Run(ctx, k)
		}
	}
	e.mu.Unlock()
	if attempt == 0 {
		attempt = 1
	}
	return e.Run(ctx, RunKey{RunID: runID, Attempt: attempt})
}
