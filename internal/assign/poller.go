package assign

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
)

// The poller is the safety net for a lost webhook (runners R5.115): at start and
// every 30 to 60 s it lists the queued and running jobs of every repository and
// feeds the controller. One poller serves all repositories; its reads are
// conditional (ETag), so a round that finds nothing new costs no rate limit.

// Poll intervals.
const (
	MinEvery = 30 * time.Second
	MaxEvery = 60 * time.Second
)

// Source reads the open jobs of the repositories with the pool on.
type Source interface {
	Targets(ctx context.Context) ([]github.PollTarget, error)
	// OpenJobs lists the queued and in-progress jobs of one repository, as
	// github.InstallationClient.ListOpenJobs does (ETag-conditional).
	OpenJobs(ctx context.Context, t github.PollTarget) ([]github.Job, error)
}

// Poller feeds a Controller.
type Poller struct {
	C   *Controller
	Src Source
	// Every is the time between rounds, kept within MinEvery and MaxEvery.
	Every time.Duration
	// After waits d or until ctx ends (a seam for tests); nil uses a timer.
	After func(ctx context.Context, d time.Duration) error
	// OnError sees every error of a round.
	OnError func(error)
}

func (p *Poller) every() time.Duration {
	d := p.Every
	if d < MinEvery {
		d = MinEvery
	}
	if d > MaxEvery {
		d = MaxEvery
	}
	return d
}

// Run polls at start and then every interval until ctx ends.
func (p *Poller) Run(ctx context.Context) {
	wait := p.After
	if wait == nil {
		wait = func(ctx context.Context, d time.Duration) error {
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
	for {
		if err := p.Round(ctx); err != nil && p.OnError != nil {
			p.OnError(err)
		}
		if wait(ctx, p.every()) != nil {
			return
		}
	}
}

// Round makes one pass over every repository. A repository whose list fails is
// skipped and its leases are left as they are: a job is called vanished only on
// a complete listing. A rate limit ends the round.
func (p *Poller) Round(ctx context.Context) error {
	ts, err := p.Src.Targets(ctx)
	if err != nil {
		return err
	}
	var firstErr error
	for _, t := range ts {
		jobs, err := p.Src.OpenJobs(ctx, t)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if errors.Is(err, github.ErrRateLimited) {
				break
			}
			continue
		}
		if err := p.apply(ctx, t, jobs); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := p.C.Sweep(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (p *Poller) apply(ctx context.Context, t github.PollTarget, jobs []github.Job) error {
	full := t.Owner + "/" + t.Repo
	listed := map[int64]github.Job{}
	var firstErr error
	keep := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, j := range jobs {
		listed[j.ID] = j
		switch j.Status {
		case "queued":
			keep(p.C.Queued(ctx, j))
		case "in_progress":
			l, ok, err := p.C.Store.Get(ctx, j.ID)
			keep(err)
			if err == nil && (!ok || l.State != Running) {
				keep(p.C.InProgress(ctx, j))
			}
		}
	}
	open, err := p.C.Store.List(ctx, Waiting, Starting, Running)
	if err != nil {
		return err
	}
	for _, l := range open {
		if !strings.EqualFold(l.Repository, full) {
			continue
		}
		if _, ok := listed[l.JobID]; !ok {
			keep(p.C.Vanished(ctx, l.JobID))
		}
	}
	return firstErr
}
