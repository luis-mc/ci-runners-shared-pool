package github

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// ReconcileEvery is how often the job view is checked against REST, whatever
// the demand source is (github-integration R5.38).
const ReconcileEvery = 5 * time.Minute

// Reconciled wraps a Demand source and reconciles the job view with REST
// every ReconcileEvery (R5.38). A job the source never told about is emitted;
// a job it told about that is gone from GitHub is emitted as completed. A
// missed webhook, a broken scale-set session or a slow poller never leaves a
// job unseen for longer than that. It is quiet when nothing is missing.
type Reconciled struct {
	Source  Demand
	Client  OpenJobsLister
	Targets func(ctx context.Context) ([]PollTarget, error)
	// Every defaults to ReconcileEvery.
	Every time.Duration
	// Tick makes the tick channel and its stop function; nil uses a real ticker.
	Tick func(d time.Duration) (<-chan time.Time, func())
	// OnError sees every failed round.
	OnError func(error)
	// OnFound sees every job the reconcile had to add or close.
	OnFound func(Job)

	mu    sync.Mutex
	known map[int64]Job
}

var _ Demand = (*Reconciled)(nil)

// Jobs implements Demand.
func (r *Reconciled) Jobs(ctx context.Context) (<-chan Job, error) {
	if r.Source == nil || r.Client == nil || r.Targets == nil {
		return nil, errors.New("reconcile: source, client and targets are required")
	}
	src, err := r.Source.Jobs(ctx)
	if err != nil {
		return nil, err
	}
	every := r.Every
	if every <= 0 {
		every = ReconcileEvery
	}
	tick := r.Tick
	if tick == nil {
		tick = func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		}
	}
	r.mu.Lock()
	r.known = map[int64]Job{}
	r.mu.Unlock()
	out := make(chan Job, 64)
	go func() {
		defer close(out)
		c, stop := tick(every)
		defer stop()
		for {
			select {
			case <-ctx.Done():
				return
			case j, ok := <-src:
				if !ok {
					return
				}
				r.remember(j)
				if !emit(ctx, out, j) {
					return
				}
			case <-c:
				if err := r.round(ctx, out); err != nil && r.OnError != nil && ctx.Err() == nil {
					r.OnError(err)
				}
			}
		}
	}()
	return out, nil
}

func (r *Reconciled) remember(j Job) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if j.Status == "completed" {
		delete(r.known, j.ID)
		return
	}
	r.known[j.ID] = j
}

func (r *Reconciled) round(ctx context.Context, out chan<- Job) error {
	targets, err := r.Targets(ctx)
	if err != nil {
		return err
	}
	bg := Background(ctx)
	seen := map[int64]bool{}
	failed := map[string]bool{}
	var firstErr error
	for _, t := range targets {
		jobs, err := r.Client.ListOpenJobs(bg, t.InstallationID, t.Owner, t.Repo)
		if err != nil {
			failed[t.Owner+"/"+t.Repo] = true
			if firstErr == nil {
				firstErr = err
			}
			if errors.Is(err, ErrRateLimited) {
				return err
			}
			continue
		}
		for _, j := range jobs {
			seen[j.ID] = true
			r.mu.Lock()
			old, ok := r.known[j.ID]
			r.mu.Unlock()
			if ok && old.Status == j.Status && old.RunnerName == j.RunnerName {
				continue
			}
			r.remember(j)
			if r.OnFound != nil {
				r.OnFound(j)
			}
			if !emit(ctx, out, j) {
				return ctx.Err()
			}
		}
	}
	r.mu.Lock()
	var gone []Job
	for id, j := range r.known {
		if !seen[id] && !failed[j.Repository] && targetsHave(targets, j.Repository) {
			gone = append(gone, j)
		}
	}
	r.mu.Unlock()
	for _, j := range gone {
		r.remember(Job{ID: j.ID, Status: "completed"})
		j.Status = "completed"
		if r.OnFound != nil {
			r.OnFound(j)
		}
		if !emit(ctx, out, j) {
			return ctx.Err()
		}
	}
	return firstErr
}

// targetsHave is true when the repository (owner/repo) is still listed; a
// repository that left the targets is the installation reconcile's business.
func targetsHave(ts []PollTarget, fullName string) bool {
	for _, t := range ts {
		if strings.EqualFold(t.Owner+"/"+t.Repo, fullName) {
			return true
		}
	}
	return false
}
