package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/share"
)

// Pass settings (R5.41).
const (
	PassEvery    = 5 * time.Minute  // how often Pass runs
	StaleAfter   = 10 * time.Minute // a queued or running row with no update this long is stale
	MaxRunsPass  = 100              // run attempts read in one pass
	WatchEvery   = 60 * time.Second // steps of a running job, while its page is open (R5.40)
	GoneReason   = "GitHub no longer has this job"
	RunGoneWhy   = "GitHub no longer has this run"
	eventsSource = "events"
)

// JobRef is what the steps reads need to know about a stored job.
type JobRef struct {
	Repository  string // owner/repo
	State       string // queued, running, done
	StepsSource string // "", "events" or "rest"
	HasSteps    bool
}

// JobStore is the part of the store reconcile needs beyond the contract Store. CIX8's Store lists stale runs
// and writes one job, but names no job ids of a run and has no steps-only write: these two reads and one write
// are the gap, answered here by a local interface that internal/store implements.
type JobStore interface {
	// OpenJobs lists the ids of the queued and running jobs of one run attempt.
	OpenJobs(ctx context.Context, repositoryID, runID int64, attempt int) ([]int64, error)
	// JobRef reads one job; found is false for a job crsp does not hold.
	JobRef(ctx context.Context, jobID int64) (ref JobRef, found bool, err error)
	// ApplySteps stores the steps of a job with steps_source "rest" and checked_at.
	ApplySteps(ctx context.Context, jobID int64, steps []RemoteStep, checkedAt time.Time) error
}

// Config is what a Reconciler needs.
type Config struct {
	GitHub   GitHub
	Store    Store
	Jobs     JobStore
	Classify Classify
	Budget   share.Budget
	// Installation gives the installation id of a repository; ok false skips its jobs.
	Installation func(ctx context.Context, repositoryID int64) (id int64, ok bool)
	Now          func() time.Time
	Log          *slog.Logger
}

type reconciler struct {
	cfg Config

	mu      sync.Mutex
	watched map[int64]time.Time // last steps read of a running job
	done    map[int64]struct{}  // jobs whose completion read was made
}

// New makes the Reconciler of R5.40 and R5.41.
func New(cfg Config) Reconciler {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &reconciler{cfg: cfg, watched: map[int64]time.Time{}, done: map[int64]struct{}{}}
}

func splitRepo(full string) (owner, repo string, ok bool) {
	owner, repo, ok = strings.Cut(full, "/")
	return owner, repo, ok && owner != "" && repo != ""
}

// Pass re-reads stale runs once, oldest first, up to MaxRunsPass, one call per run (R5.41). It stops early when
// the insights share is spent; the next pass goes on.
func (r *reconciler) Pass(ctx context.Context, now time.Time) (int, error) {
	runs, err := r.cfg.Store.StaleRuns(ctx, now.Add(-StaleAfter), now.Add(-PassEvery), MaxRunsPass)
	if err != nil {
		return 0, fmt.Errorf("reconcile: list stale runs: %w", err)
	}
	checked := 0
	for _, run := range runs {
		if err := ctx.Err(); err != nil {
			return checked, err
		}
		inst, ok := r.cfg.Installation(ctx, run.RepositoryID)
		owner, repo, valid := splitRepo(run.Repository)
		if !ok || !valid {
			continue
		}
		if ok, _ := r.cfg.Budget.Take(ctx, inst, share.PriorityReconcile, 1); !ok {
			break
		}
		if err := r.reconcileRun(ctx, now, inst, owner, repo, run); err != nil {
			if ctx.Err() != nil {
				return checked, ctx.Err()
			}
			r.cfg.Log.Warn("reconcile: run not read", "repository", run.Repository, "run", run.RunID, "err", err)
			continue
		}
		checked++
	}
	return checked, nil
}

func (r *reconciler) reconcileRun(ctx context.Context, now time.Time, inst int64, owner, repo string, run StaleRun) error {
	open, err := r.cfg.Jobs.OpenJobs(ctx, run.RepositoryID, run.RunID, run.RunAttempt)
	if err != nil {
		return err
	}
	remote, err := r.cfg.GitHub.RunJobs(ctx, inst, owner, repo, run.RunID, run.RunAttempt)
	if errors.Is(err, ErrGone) {
		for _, id := range open {
			if err := r.cfg.Store.MarkLost(ctx, id, RunGoneWhy, now); err != nil {
				return err
			}
		}
		return nil
	}
	if err != nil {
		return err
	}
	seen := make(map[int64]bool, len(remote))
	for _, j := range remote {
		seen[j.ID] = true
		origin, err := r.cfg.Classify.Origin(ctx, j.RunnerName, j.RunnerGroupName)
		if err != nil {
			return err
		}
		if err := r.cfg.Store.Apply(ctx, run.RepositoryID, j, origin, now); err != nil {
			return err
		}
	}
	for _, id := range open {
		if !seen[id] {
			if err := r.cfg.Store.MarkLost(ctx, id, GoneReason, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// JobCompleted reads the steps of a completed job whose steps did not come from webhooks, once (R5.40).
func (r *reconciler) JobCompleted(ctx context.Context, repositoryID, jobID int64) error {
	r.mu.Lock()
	_, was := r.done[jobID]
	r.mu.Unlock()
	if was {
		return nil
	}
	if err := r.readSteps(ctx, repositoryID, jobID, false); err != nil {
		return err
	}
	r.mu.Lock()
	r.done[jobID] = struct{}{}
	delete(r.watched, jobID)
	r.mu.Unlock()
	return nil
}

// Watch reads a running job's steps at most every WatchEvery while its Job page is open (R5.40).
func (r *reconciler) Watch(ctx context.Context, repositoryID, jobID int64) error {
	now := r.cfg.Now()
	r.mu.Lock()
	last, ok := r.watched[jobID]
	if ok && now.Sub(last) < WatchEvery {
		r.mu.Unlock()
		return nil
	}
	r.watched[jobID] = now
	r.mu.Unlock()
	return r.readSteps(ctx, repositoryID, jobID, true)
}

// readSteps does one REST read for the steps of a job. A job with steps from events is left alone.
func (r *reconciler) readSteps(ctx context.Context, repositoryID, jobID int64, running bool) error {
	ref, found, err := r.cfg.Jobs.JobRef(ctx, jobID)
	if err != nil || !found {
		return err
	}
	if ref.StepsSource == eventsSource || (ref.StepsSource == "" && ref.HasSteps) {
		return nil
	}
	if running && ref.State != "running" {
		return nil
	}
	inst, ok := r.cfg.Installation(ctx, repositoryID)
	owner, repo, valid := splitRepo(ref.Repository)
	if !ok || !valid {
		return nil
	}
	if ok, _ := r.cfg.Budget.Take(ctx, inst, share.PrioritySteps, 1); !ok {
		return nil // over the share: the screens say it waits (R6.1)
	}
	j, err := r.cfg.GitHub.Job(ctx, inst, owner, repo, jobID)
	if errors.Is(err, ErrGone) {
		return nil // steps GitHub no longer keeps stay as they are; the stale pass handles open rows
	}
	if err != nil {
		return err
	}
	return r.cfg.Jobs.ApplySteps(ctx, jobID, j.Steps, r.cfg.Now())
}
