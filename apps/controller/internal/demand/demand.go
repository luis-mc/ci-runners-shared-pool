// Package demand starts one ephemeral container runner for each queued job
// (github-integration R5.37, runners R5.25 to R5.27, R5.31). It reads job
// changes from a github.Demand source (the REST poller), matches the runs-on
// labels of a queued job to a machine whose allow list names the container
// backend, and asks that machine's agent to start one JIT runner. It keeps to
// the slot budget of the machine (scheduling R5.1) and never starts a second
// runner for the same job. It is the fallback source: a scale set or a
// webhook feeds the same loop later.
package demand

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// RunnerPrefix starts the name of every runner the loop makes. The rest is the
// job id, so a runner is easy to tie to the job it was made for.
const RunnerPrefix = "crsp-job-"

// Actor is the name the loop writes in the audit trail.
const Actor = "demand"

// Repo is what the loop needs to know about a repository with the pool on.
type Repo struct {
	Scope runners.Scope
	// InstallationID is the GitHub App installation that reads the jobs of
	// the repository (used by the job re-check, runners R5.73).
	InstallationID int64
	AllowFork      bool
	// ExtraPackages are the pool's extra packages (runners R5.51).
	ExtraPackages []string
}

// Host is a machine that may start container runners: online, active, Linux,
// with the container backend present, usable and in the machine's allow list.
// Slots is the machine's job slot budget (CRSP50): at most that many ephemeral
// runners exist on it at once.
type Host struct {
	MachineID string
	Arch      string // x64 or arm64
	Slots     int
}

// Env is the controller's view the loop reads.
type Env interface {
	// Repo returns the repository named owner/name when its pool is on.
	Repo(ctx context.Context, fullName string) (Repo, bool, error)
	// Hosts lists the machines that may start container runners now.
	Hosts(ctx context.Context) ([]Host, error)
}

// Runners is the part of the runners service the loop uses.
type Runners interface {
	StartJobRunner(ctx context.Context, j runners.JobRunner, actor string) (runners.Record, error)
	FinishJobRunner(ctx context.Context, runnerID, actor string) error
	EphemeralOnMachine(ctx context.Context, machineID string) (int, error)
	// EphemeralCeiling is how many ephemeral runners may run at once on the
	// machine: the slot records an ephemeral pool request left (runners R5.92).
	// Zero means none start there.
	EphemeralCeiling(ctx context.Context, machineID string) (int, error)
	// RemoveIdleRunner removes a runner that never got a job, after asking
	// GitHub that it is not busy. It returns runners.ErrRunnerBusy when it is.
	RemoveIdleRunner(ctx context.Context, runnerID, actor string) error
	// ClearFailedStarts removes the failed records of the tries for one job.
	ClearFailedStarts(ctx context.Context, jobKey string) error
}

// DefaultIdleAfter is how long a runner may wait with no job before the loop
// removes it and frees its slot (runners R5.26).
const DefaultIdleAfter = 15 * time.Minute

// idleRetry is the wait before the loop tries again to remove an idle runner
// whose removal failed.
const idleRetry = time.Minute

// Loop is the demand loop.
type Loop struct {
	Env      Env
	Runners  Runners
	PoolName string
	Logger   *slog.Logger
	// Every is how often waiting jobs are tried again (default 5 s). Now is a
	// seam for tests.
	Every time.Duration
	Now   func() time.Time
	// IdleAfter is the time a runner may have no job (default 15 minutes).
	IdleAfter time.Duration
	// ReadJob reads one job from GitHub. Before a runner starts for a queued
	// job whose last event is older than RecheckAfter, the loop reads the job
	// again and starts nothing when it is no longer queued (runners R5.73).
	// Nil means no re-check; the state the events gave is trusted.
	ReadJob func(ctx context.Context, installationID int64, owner, repo string, jobID int64) (github.Job, error)
	// RecheckAfter is how old an event may be before the API read (default 5 s).
	RecheckAfter time.Duration
	// ScaleJIT, when set, gives the JIT source of a job that came from a scale
	// set (github.Job.ScaleSetID is not zero). Without it such a job gets the
	// repository-level config like any other.
	ScaleJIT func(j github.Job, runnerName string) func(ctx context.Context) (github.JITConfig, error)

	mu      sync.Mutex
	seen    map[int64]github.Job // open jobs by id
	seenAt  map[int64]time.Time  // when the last event of an open job came
	pending map[int64]*waiting
	started map[int64]startedRunner // job id -> runner made for it
	gaveUp  map[int64]bool          // jobs left after maxFails tries; a new queued event does not start them again (OPS89)
}

type waiting struct {
	fails   int
	nextTry time.Time
	why     string
}

type startedRunner struct {
	id, name string
	at       time.Time // when the runner was started
	took     bool      // a job ran on it, so the job's end cleans it up
	retryAt  time.Time // an idle removal that failed waits until then
}

// maxFails is how many failed starts a job gets before the loop leaves it.
const maxFails = 5

func (l *Loop) init() {
	if l.seen == nil {
		l.seen = map[int64]github.Job{}
		l.seenAt = map[int64]time.Time{}
		l.pending = map[int64]*waiting{}
		l.started = map[int64]startedRunner{}
		l.gaveUp = map[int64]bool{}
	}
	if l.IdleAfter <= 0 {
		l.IdleAfter = DefaultIdleAfter
	}
	if l.RecheckAfter <= 0 {
		l.RecheckAfter = 5 * time.Second
	}
	if l.Every <= 0 {
		l.Every = 5 * time.Second
	}
	if l.Now == nil {
		l.Now = time.Now
	}
	if l.Logger == nil {
		l.Logger = slog.Default()
	}
}

// Run reads jobs until the channel closes or ctx ends. Each change updates the
// loop's view; every change and every tick then starts runners for the jobs
// that wait.
func (l *Loop) Run(ctx context.Context, jobs <-chan github.Job) {
	l.mu.Lock()
	l.init()
	every := l.Every
	l.mu.Unlock()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case j, ok := <-jobs:
			if !ok {
				return
			}
			l.Observe(ctx, j)
			l.Dispatch(ctx)
		case <-t.C:
			l.Dispatch(ctx)
			l.SweepIdle(ctx)
		}
	}
}

// Observe records one job change. A queued job waits for a runner; a job that
// started or ended stops waiting; the runner made for an ended job is cleaned
// up.
func (l *Loop) Observe(ctx context.Context, j github.Job) {
	l.mu.Lock()
	l.init()
	// A webhook knows less than the poller: it never wipes the event and the
	// fork flag the poller read (CRSP82).
	if old, ok := l.seen[j.ID]; ok && j.Partial && !old.Partial {
		j.Event, j.FromFork, j.Partial = old.Event, old.FromFork, false
	}
	switch j.Status {
	case "queued":
		l.seen[j.ID] = j
		l.seenAt[j.ID] = l.Now()
		if _, done := l.started[j.ID]; !done && l.pending[j.ID] == nil && !l.gaveUp[j.ID] {
			l.pending[j.ID] = &waiting{}
		}
	case "in_progress":
		l.seen[j.ID] = j
		l.seenAt[j.ID] = l.Now()
		delete(l.pending, j.ID)
		// The runner of this job has a job now: it is not idle.
		if strings.HasPrefix(j.RunnerName, RunnerPrefix) {
			for id, st := range l.started {
				if st.name == j.RunnerName {
					st.took = true
					l.started[id] = st
				}
			}
		}
	default: // completed
		delete(l.seen, j.ID)
		delete(l.seenAt, j.ID)
		delete(l.pending, j.ID)
		delete(l.gaveUp, j.ID)
	}
	var finish []startedRunner
	if j.Status == "completed" && strings.HasPrefix(j.RunnerName, RunnerPrefix) {
		// The runner of this job is spent. A runner takes whichever queued
		// job it gets, so a job it was made for may still be queued: it waits
		// again.
		for id, st := range l.started {
			if st.name != j.RunnerName {
				continue
			}
			finish = append(finish, st)
			delete(l.started, id)
			if q, ok := l.seen[id]; ok && q.Status == "queued" {
				l.pending[id] = &waiting{}
			}
		}
	}
	if j.Status == "completed" {
		delete(l.started, j.ID)
	}
	l.mu.Unlock()
	if j.Status == "completed" {
		// The job ended: the failed rows of its tries go (OPS89).
		if err := l.Runners.ClearFailedStarts(ctx, fmt.Sprintf("%s%d-", RunnerPrefix, j.ID)); err != nil {
			l.Logger.Warn("clear the failed starts of an ended job", "job", j.ID, "err", err)
		}
	}
	for _, st := range finish {
		if st.id == "" {
			continue
		}
		if err := l.Runners.FinishJobRunner(ctx, st.id, Actor); err != nil {
			l.Logger.Warn("clean up the runner of a finished job", "runner", st.name, "err", err)
		}
	}
}

// SweepIdle removes each runner that has had no job for IdleAfter, so its slot
// is free again (runners R5.26). GitHub is asked that the runner is not busy
// first; a busy one is left to its job. The job the runner was made for waits
// again when it is still queued.
func (l *Loop) SweepIdle(ctx context.Context) {
	l.mu.Lock()
	l.init()
	now := l.Now()
	var idle []startedRunner
	for _, st := range l.started {
		if !st.took && !st.at.IsZero() && now.Sub(st.at) >= l.IdleAfter && !now.Before(st.retryAt) {
			idle = append(idle, st)
		}
	}
	l.mu.Unlock()
	for _, st := range idle {
		err := l.Runners.RemoveIdleRunner(ctx, st.id, Actor)
		l.mu.Lock()
		switch {
		case errors.Is(err, runners.ErrRunnerBusy):
			l.markTook(st.name)
		case err != nil:
			l.markRetry(st.name, now.Add(idleRetry))
			l.Logger.Warn("remove an idle runner", "runner", st.name, "err", err)
		default:
			for id, s := range l.started {
				if s.name != st.name {
					continue
				}
				delete(l.started, id)
				if q, ok := l.seen[id]; ok && q.Status == "queued" {
					l.pending[id] = &waiting{}
				}
			}
			l.Logger.Info("idle runner removed", "runner", st.name, "idle_after", l.IdleAfter.String())
		}
		l.mu.Unlock()
	}
}

func (l *Loop) markTook(name string) {
	for id, s := range l.started {
		if s.name == name {
			s.took = true
			l.started[id] = s
		}
	}
}

func (l *Loop) markRetry(name string, at time.Time) {
	for id, s := range l.started {
		if s.name == name {
			s.retryAt = at
			l.started[id] = s
		}
	}
}

// Waiting is the number of queued jobs the loop has not started a runner for.
func (l *Loop) Waiting() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.pending)
}

// Dispatch starts a runner for each waiting job that a machine can take now.
// Older jobs go first. A job no machine can take stays waiting without noise.
func (l *Loop) Dispatch(ctx context.Context) {
	l.mu.Lock()
	l.init()
	ids := make([]int64, 0, len(l.pending))
	for id := range l.pending {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool {
		ja, jb := l.seen[ids[a]], l.seen[ids[b]]
		if !ja.CreatedAt.Equal(jb.CreatedAt) {
			return ja.CreatedAt.Before(jb.CreatedAt)
		}
		return ids[a] < ids[b]
	})
	l.mu.Unlock()
	if len(ids) == 0 {
		return
	}
	hosts, err := l.Env.Hosts(ctx)
	if err != nil {
		l.Logger.Warn("list machines for queued jobs", "err", err)
		return
	}
	if len(hosts) == 0 {
		return
	}
	used := map[string]int{}
	limit := map[string]int{}
	for _, h := range hosts {
		n, err := l.Runners.EphemeralOnMachine(ctx, h.MachineID)
		if err != nil {
			l.Logger.Warn("count ephemeral runners", "machine", h.MachineID, "err", err)
			return
		}
		used[h.MachineID] = n
		c, err := l.Runners.EphemeralCeiling(ctx, h.MachineID)
		if err != nil {
			l.Logger.Warn("read the ephemeral ceiling", "machine", h.MachineID, "err", err)
			return
		}
		// The ceiling and the job slots both bound the runners at once (R5.92).
		limit[h.MachineID] = min(c, h.Slots)
		if c == 0 {
			limit[h.MachineID] = -1
		}
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		l.mu.Lock()
		j, w := l.seen[id], l.pending[id]
		hold := w == nil || l.Now().Before(w.nextTry)
		l.mu.Unlock()
		if hold {
			continue
		}
		l.try(ctx, j, w, hosts, used, limit)
	}
}

func (l *Loop) try(ctx context.Context, j github.Job, w *waiting, hosts []Host, used map[string]int, limit map[string]int) {
	repo, on, err := l.Env.Repo(ctx, j.Repository)
	if err != nil {
		l.Logger.Warn("read repository", "repository", j.Repository, "err", err)
		return
	}
	if !on {
		return
	}
	if j.Partial {
		l.note(j, w, "waiting to learn whether this job comes from a fork")
		return
	}
	if j.FromFork && !repo.AllowFork {
		l.note(j, w, "a fork pull request; the policy does not allow it")
		return
	}
	var labelWhy string
	for _, h := range hosts {
		labels, why := Match(j.Labels, h.Arch, l.PoolName)
		if why != "" {
			labelWhy = why
			continue
		}
		if limit[h.MachineID] < 0 {
			l.note(j, w, "no ephemeral runners are set up on the machine; add some in Runners")
			continue
		}
		if h.Slots < 1 || used[h.MachineID] >= limit[h.MachineID] {
			l.note(j, w, "every ephemeral runner of the machine is in use")
			continue
		}
		if !l.stillQueued(ctx, j, repo) {
			return
		}
		// One name for each try: GitHub may still hold the registration of
		// an earlier try and answers 409 to the same name (OPS89).
		key := fmt.Sprintf("%s%d-", RunnerPrefix, j.ID)
		name := fmt.Sprintf("%s%d", key, w.fails+1)
		jr := runners.JobRunner{Name: name, MachineID: h.MachineID, Arch: h.Arch,
			Backend: "docker", JobKey: key, Labels: labels, Scope: repo.Scope, ExtraPackages: repo.ExtraPackages}
		if j.ScaleSetID != 0 && l.ScaleJIT != nil {
			jr.JIT = l.ScaleJIT(j, name)
		}
		rec, err := l.Runners.StartJobRunner(ctx, jr, Actor)
		if err != nil {
			l.fail(j, w, err)
			return
		}
		used[h.MachineID]++
		l.mu.Lock()
		delete(l.pending, j.ID)
		l.started[j.ID] = startedRunner{id: rec.ID, name: name, at: l.Now()}
		l.mu.Unlock()
		l.Logger.Info("runner started for a queued job", "job", j.ID, "repository", j.Repository, "machine", h.MachineID, "runner", name)
		return
	}
	if labelWhy != "" {
		l.note(j, w, labelWhy)
	}
}

// stillQueued is the last check before a runner starts: it never starts one
// for a job that already finished, was cancelled or was taken (runners R5.73).
// The webhook or poll state is trusted while it is fresh; when the last event
// of the job is older than RecheckAfter, one API read settles it. A job that
// is not queued any more stops waiting. A read that fails starts nothing and
// the job is tried again on the next tick.
func (l *Loop) stillQueued(ctx context.Context, j github.Job, repo Repo) bool {
	if l.ReadJob == nil {
		return true
	}
	l.mu.Lock()
	at := l.seenAt[j.ID]
	l.mu.Unlock()
	if !at.IsZero() && l.Now().Sub(at) < l.RecheckAfter {
		return true
	}
	owner, name, ok := strings.Cut(j.Repository, "/")
	if !ok {
		return false
	}
	cur, err := l.ReadJob(ctx, repo.InstallationID, owner, name, j.ID)
	if err != nil {
		l.Logger.Warn("re-read a queued job before starting a runner", "job", j.ID, "repository", j.Repository, "err", err)
		return false
	}
	if cur.Status == "queued" {
		l.mu.Lock()
		l.seenAt[j.ID] = l.Now()
		l.mu.Unlock()
		return true
	}
	l.mu.Lock()
	delete(l.pending, j.ID)
	delete(l.seen, j.ID)
	delete(l.seenAt, j.ID)
	l.mu.Unlock()
	l.Logger.Info("queued job is no longer queued; no runner started", "job", j.ID, "repository", j.Repository, "status", cur.Status, "conclusion", cur.Conclusion)
	return false
}

// note keeps the reason a job waits and logs it once.
func (l *Loop) note(j github.Job, w *waiting, why string) {
	l.mu.Lock()
	changed := w.why != why
	w.why = why
	l.mu.Unlock()
	if changed {
		l.Logger.Info("queued job waits", "job", j.ID, "repository", j.Repository, "why", why)
	}
}

// fail backs off a job whose runner did not start, and leaves it after
// maxFails tries. The error text comes from the agent and holds no secret.
func (l *Loop) fail(j github.Job, w *waiting, err error) {
	l.mu.Lock()
	w.fails++
	gone := w.fails >= maxFails
	w.nextTry = l.Now().Add(time.Duration(1<<min(w.fails, 6)) * 5 * time.Second)
	if gone {
		delete(l.pending, j.ID)
		l.gaveUp[j.ID] = true
	}
	l.mu.Unlock()
	l.Logger.Warn("start a runner for a queued job", "job", j.ID, "repository", j.Repository, "try", w.fails, "gave_up", gone, "err", err)
}

// Match checks the runs-on labels of a job against what a container runner on
// a machine of the given architecture can carry. It returns the runner labels,
// or the reason the job does not match. The runner carries the job's labels
// and its architecture; every label must be generic and one a container can
// honour: self-hosted and linux are required, kvm and gpu are not offered.
func Match(jobLabels []string, arch, pool string) ([]string, string) {
	have := map[string]bool{}
	for _, raw := range jobLabels {
		l := strings.ToLower(strings.TrimSpace(raw))
		if l != "" {
			have[l] = true
		}
	}
	if !have["self-hosted"] || !have["linux"] {
		return nil, "the job does not ask for a self-hosted Linux runner"
	}
	other := "arm64"
	if arch == "arm64" {
		other = "x64"
	}
	if have[other] {
		return nil, "the job asks for " + other + " and the machine is " + arch
	}
	set := map[string]bool{arch: true}
	for l := range have {
		switch {
		case l == "self-hosted", l == "linux", l == "x64", l == "arm64", l == "docker", l == runners.EphemeralLabel:
			set[l] = true
		case pool != "" && l == strings.ToLower(pool):
			set[pool] = true
		default:
			return nil, "label " + l + " is not offered by a container runner"
		}
	}
	out := make([]string, 0, len(set))
	for l := range set {
		out = append(out, l)
	}
	sort.Strings(out)
	return out, ""
}
