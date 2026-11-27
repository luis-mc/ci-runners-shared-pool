package demand

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// DefaultQuiet is how long the demand source may see no demand for a
// repository that has queued jobs for the pool (config `demand_quiet_minutes`).
const DefaultQuiet = 10 * time.Minute

// Quiet is one repository whose queued jobs the demand source does not see.
type Quiet struct {
	Repository string
	// Queued is how many queued jobs GitHub lists that resolve to this pool.
	Queued int
	// Since is when the quiet started: the last demand the source saw, the
	// watchdog's start, or the restart, whichever is latest.
	Since time.Time
	// Restarted is true once the watchdog restarted the demand loop for this
	// episode; RestartedAt is when. It restarts once per episode.
	Restarted   bool
	RestartedAt time.Time
}

// DemandSeen is the part of the demand source the watchdog reads.
type DemandSeen interface {
	// LastDemand is when the source last saw a queued job of the repository
	// (owner/repo); the zero time when it has not.
	LastDemand(fullName string) time.Time
}

// Watchdog finds a demand source that has gone quiet while GitHub lists queued
// jobs for the pool, restarts it once, and keeps the state for the doctor
// check `demand.quiet` (github-integration R5.37).
//
// It asks GitHub for the queued jobs itself, with the poller's own targets, so
// a poller that is stuck cannot hide its own silence. It matches the labels of
// each queued job the way the demand loop does, so a job for another pool or a
// project label never counts.
type Watchdog struct {
	Lister  github.OpenJobsLister
	Targets func(ctx context.Context) ([]github.PollTarget, error)
	Source  DemandSeen
	// Restart restarts the demand loop. It is called at most once per quiet episode.
	Restart  func()
	PoolName string
	// Quiet is the allowed silence (default 10 minutes).
	Quiet  time.Duration
	Now    func() time.Time
	Logger *slog.Logger

	mu       sync.Mutex
	started  time.Time
	episodes map[string]*Quiet // lower-case owner/repo
}

func (w *Watchdog) init() {
	if w.Now == nil {
		w.Now = time.Now
	}
	if w.Logger == nil {
		w.Logger = slog.Default()
	}
	if w.Quiet <= 0 {
		w.Quiet = DefaultQuiet
	}
	if w.episodes == nil {
		w.episodes = map[string]*Quiet{}
		w.started = w.Now()
	}
}

// Tick makes one round: list the queued jobs of every target, compare with
// what the source saw, restart on a first detection. A repository that GitHub
// cannot list this round keeps its state.
func (w *Watchdog) Tick(ctx context.Context) error {
	w.mu.Lock()
	w.init()
	w.mu.Unlock()
	targets, err := w.Targets(ctx)
	if err != nil {
		return err
	}
	bg := github.Background(ctx)
	var firstErr error
	var restart bool
	listed := map[string]bool{}
	for _, t := range targets {
		full := t.Owner + "/" + t.Repo
		key := strings.ToLower(full)
		jobs, err := w.Lister.ListOpenJobs(bg, t.InstallationID, t.Owner, t.Repo)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			listed[key] = true // unknown: keep what we had
			continue
		}
		listed[key] = true
		queued := 0
		for _, j := range jobs {
			if j.Status == "queued" && forPool(j.Labels, w.PoolName) {
				queued++
			}
		}
		w.mu.Lock()
		now := w.Now()
		ep := w.episodes[key]
		if queued == 0 {
			delete(w.episodes, key)
			w.mu.Unlock()
			continue
		}
		last := w.Source.LastDemand(full)
		if ep != nil && last.After(ep.RestartedAt) {
			delete(w.episodes, key) // the restarted source sees demand again
			ep = nil
		}
		since := w.started
		if last.After(since) {
			since = last
		}
		if ep != nil && ep.RestartedAt.After(since) {
			since = ep.RestartedAt
		}
		if now.Sub(since) < w.Quiet {
			if ep != nil {
				ep.Queued = queued // restarted, waiting for the next interval
			}
			w.mu.Unlock()
			continue
		}
		if ep == nil {
			ep = &Quiet{Repository: full, Since: since, Restarted: true, RestartedAt: now}
			w.episodes[key] = ep
			restart = true
			w.Logger.Warn("demand source is quiet while jobs are queued; restarting the demand loop once",
				"repository", full, "queued", queued, "quiet_since", since)
		}
		ep.Queued = queued
		w.mu.Unlock()
	}
	// A repository no longer a target has no episode.
	w.mu.Lock()
	for key := range w.episodes {
		if !listed[key] {
			delete(w.episodes, key)
		}
	}
	w.mu.Unlock()
	if restart && w.Restart != nil {
		w.Restart()
	}
	return firstErr
}

// QuietRepos lists the repositories that are quiet now, sorted by name.
func (w *Watchdog) QuietRepos() []Quiet {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]Quiet, 0, len(w.episodes))
	for _, e := range w.episodes {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Repository < out[j].Repository })
	return out
}

// Run ticks every `every` until ctx ends.
func (w *Watchdog) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := w.Tick(ctx); err != nil && ctx.Err() == nil {
			w.Logger.Warn("demand watchdog round", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// forPool is true when a job's labels resolve to a container runner of this
// pool on some architecture.
func forPool(labels []string, pool string) bool {
	for _, arch := range []string{"x64", "arm64"} {
		if _, why := Match(labels, arch, pool); why == "" {
			return true
		}
	}
	return false
}
