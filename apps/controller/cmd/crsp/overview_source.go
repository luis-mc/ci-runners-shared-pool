package main

import (
	"context"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/history"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/overview"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// poolOverviewSource is the live view's source over the store: the machines with their load, the queued and running
// jobs, and the minutes saved today (observability R5.1, R5.15; audit R3-7).
type poolOverviewSource struct {
	DB         *store.Store
	MachineSvc interface {
		List(ctx context.Context) ([]machines.View, error)
	}
	// Timers gives the job timers once they are wired (they are built after the live view); it may be nil.
	Timers func() api.TimerService
	// open shares one read of the open jobs between the queued and the running question of a snapshot.
	open singleflight.Group
}

type openJobs struct{ queued, running []gen.Job }

func (p *poolOverviewSource) openJobs(ctx context.Context) (openJobs, error) {
	v, err, _ := p.open.Do("open", func() (any, error) {
		var timers api.TimerService
		if p.Timers != nil {
			timers = p.Timers()
		}
		q, r, err := api.OverviewJobs(context.WithoutCancel(ctx), p.DB.Jobs(), timers, time.Now())
		return openJobs{q, r}, err
	})
	if err != nil {
		return openJobs{}, err
	}
	return v.(openJobs), nil
}

// Machines lists the machines as the contract shows them.
func (p *poolOverviewSource) Machines(ctx context.Context) ([]gen.Machine, error) {
	if p.MachineSvc == nil {
		return nil, nil
	}
	vs, err := p.MachineSvc.List(ctx)
	if err != nil {
		return nil, err
	}
	return api.OverviewMachines(vs, time.Now()), nil
}

// QueuedJobs lists the queued jobs in queue order.
func (p *poolOverviewSource) QueuedJobs(ctx context.Context) ([]gen.Job, error) {
	o, err := p.openJobs(ctx)
	return o.queued, err
}

// RunningJobs lists the jobs that run now.
func (p *poolOverviewSource) RunningJobs(ctx context.Context) ([]gen.Job, error) {
	o, err := p.openJobs(ctx)
	return o.running, err
}

// MinutesSavedSince sums, per OS, the finished pool jobs since the given time, each rounded up to a whole minute.
func (p *poolOverviewSource) MinutesSavedSince(ctx context.Context, since time.Time) ([]overview.OSMinutes, error) {
	jobs, err := history.DBSource{DB: p.DB}.FinishedPoolJobs(ctx, since, time.Now().Add(time.Minute))
	if err != nil {
		return nil, err
	}
	by := map[string]int{}
	for _, j := range jobs {
		by[j.OS] += history.RoundUpMinutes(j.Run) * max(j.Count, 1)
	}
	out := make([]overview.OSMinutes, 0, len(by))
	for os, m := range by {
		out = append(out, overview.OSMinutes{OS: os, Minutes: m})
	}
	return out, nil
}
