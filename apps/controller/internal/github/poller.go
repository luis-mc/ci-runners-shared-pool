package github

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// PollTarget is one enabled repository to poll.
type PollTarget struct {
	InstallationID int64
	Owner, Repo    string
}

// OpenJobsLister is the one call the poller needs.
type OpenJobsLister interface {
	ListOpenJobs(ctx context.Context, installationID int64, owner, repo string) ([]Job, error)
}

// Poller is the REST polling Demand source (R5.37). It lists open jobs of
// each enabled repository, emits a Job when one is new or changed, and emits
// a completed Job when one it knew about is gone. When nothing changes it
// slows down, and a rate limit makes it wait until the reset (R6.6).
type Poller struct {
	Client  OpenJobsLister
	Targets func(ctx context.Context) ([]PollTarget, error)
	// Interval is the base interval (default 30 s). MaxInterval caps the idle
	// back-off (default 5 minutes).
	Interval    time.Duration
	MaxInterval time.Duration
	// Sleep is a seam for tests.
	Sleep func(ctx context.Context, d time.Duration) error
	// OnError sees every failed round.
	OnError func(error)
	// Now is a seam for tests.
	Now func() time.Time
	// OnQueued sees every queued job a round lists (webhook health, RES14).
	OnQueued func(fullName string, jobID int64)

	known map[int64]Job

	mu         sync.Mutex
	lastDemand map[string]time.Time // lower-case owner/repo -> last round that listed a queued job
}

// LastDemand is the time the poller last listed a queued job of the
// repository (owner/repo), or the zero time when it has not. The quiet-demand
// watchdog reads it (doctor `demand.quiet`).
func (p *Poller) LastDemand(fullName string) time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastDemand[strings.ToLower(fullName)]
}

func (p *Poller) sawDemand(fullName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lastDemand == nil {
		p.lastDemand = map[string]time.Time{}
	}
	p.lastDemand[strings.ToLower(fullName)] = p.Now()
}

var _ Demand = (*Poller)(nil)

// Jobs implements Demand.
func (p *Poller) Jobs(ctx context.Context) (<-chan Job, error) {
	if p.Client == nil || p.Targets == nil {
		return nil, errors.New("poller: client and targets are required")
	}
	if p.Interval <= 0 {
		p.Interval = 30 * time.Second
	}
	if p.MaxInterval <= 0 {
		p.MaxInterval = 5 * time.Minute
	}
	if p.Sleep == nil {
		p.Sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	if p.Now == nil {
		p.Now = time.Now
	}
	p.known = map[int64]Job{}
	out := make(chan Job, 64)
	go func() {
		defer close(out)
		wait := p.Interval
		for {
			changed, rlWait, err := p.round(ctx, out)
			switch {
			case err != nil && p.OnError != nil:
				p.OnError(err)
			}
			switch {
			case rlWait > 0:
				wait = rlWait
			case changed:
				wait = p.Interval
			default:
				wait = min(wait*2, p.MaxInterval)
			}
			if err := p.Sleep(ctx, wait); err != nil {
				return
			}
		}
	}()
	return out, nil
}

func (p *Poller) round(ctx context.Context, out chan<- Job) (changed bool, rlWait time.Duration, err error) {
	targets, err := p.Targets(ctx)
	if err != nil {
		return false, 0, err
	}
	bg := Background(ctx)
	seen := map[int64]bool{}
	failed := map[string]bool{}
	for _, t := range targets {
		jobs, err := p.Client.ListOpenJobs(bg, t.InstallationID, t.Owner, t.Repo)
		if err != nil {
			failed[t.Owner+"/"+t.Repo] = true
			var rl *RateLimitError
			if errors.As(err, &rl) {
				d := rl.ResetAt.Sub(p.Now())
				if d < time.Second {
					d = time.Second
				}
				return changed, d, err
			}
			continue
		}
		for _, j := range jobs {
			seen[j.ID] = true
			if j.Status == "queued" {
				p.sawDemand(t.Owner + "/" + t.Repo)
				if p.OnQueued != nil {
					p.OnQueued(t.Owner+"/"+t.Repo, j.ID)
				}
			}
			if old, ok := p.known[j.ID]; ok && old.Status == j.Status && old.RunnerName == j.RunnerName {
				continue
			}
			p.known[j.ID] = j
			changed = true
			if !emit(ctx, out, j) {
				return changed, 0, ctx.Err()
			}
		}
	}
	for id, j := range p.known {
		if seen[id] || failed[j.Repository] {
			continue
		}
		delete(p.known, id)
		j.Status = "completed"
		changed = true
		if !emit(ctx, out, j) {
			return changed, 0, ctx.Err()
		}
	}
	return changed, 0, nil
}

func emit(ctx context.Context, out chan<- Job, j Job) bool {
	select {
	case out <- j:
		return true
	case <-ctx.Done():
		return false
	}
}
