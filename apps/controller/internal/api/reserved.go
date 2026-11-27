package api

import (
	"context"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/assign"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/timers"
)

// Reserved jobs and jobs whose timer ran out (LOOK65, scheduling R5.93 and R5.100). The controller keeps both outside
// the job record: a reservation lives on the waiting lease of the assign controller, a timer that ran out lives in the
// timer engine (state grace). This reads them and puts them on the job and on the timeline.

// jobFlags is what the controller knows about open jobs, by GitHub job id.
type jobFlags struct {
	reserved map[int64]time.Time // job -> when it became its machine's reserved job
	grace    map[int64]time.Time // job -> end of the grace after its timer ran out
}

// reservedOf lists the waiting leases that hold a reservation.
func reservedOf(leases []assign.Lease) map[int64]time.Time {
	out := map[int64]time.Time{}
	for _, l := range leases {
		if l.State == assign.Waiting && !l.ReservedSince.IsZero() {
			out[l.JobID] = l.ReservedSince.UTC()
		}
	}
	return out
}

// graceOf is the end of the grace of a job whose timer ran out; ok is false for any other timer state.
func graceOf(v timers.JobView, found bool) (time.Time, bool) {
	if !found || v.State != timers.StateGrace || v.Deadline.IsZero() {
		return time.Time{}, false
	}
	return v.Deadline.UTC(), true
}

// flagsFor reads the reservations and timers of the open jobs in rows. A service that is not wired says nothing.
func (s *server) flagsFor(ctx context.Context, rows []jobsdata.Job) jobFlags {
	var queued bool
	var running []int64
	for _, r := range rows {
		queued = queued || r.State == jobsdata.Queued
		if r.State == jobsdata.Running {
			running = append(running, r.ID)
		}
	}
	return s.flagsOf(ctx, queued, running)
}

// flagsOf reads the reservations (when some job is queued) and the timers of the running jobs.
func (s *server) flagsOf(ctx context.Context, anyQueued bool, running []int64) jobFlags {
	f := jobFlags{reserved: map[int64]time.Time{}, grace: map[int64]time.Time{}}
	if s.opts.Timers != nil {
		for _, id := range running {
			v, ok := s.opts.Timers.Job(ctx, id)
			if d, ok := graceOf(v, ok); ok {
				f.grace[id] = d
			}
		}
	}
	if anyQueued && s.opts.Assign != nil {
		if ls, err := s.opts.Assign.List(ctx, assign.Waiting); err == nil {
			f.reserved = reservedOf(ls)
		} else {
			s.opts.Logger.Error("reserved jobs", "err", err)
		}
	}
	return f
}

// markOverview puts the flags on the jobs of the overview and on its list of reasons.
func (s *server) markOverview(ctx context.Context, o *gen.Overview) {
	var running []int64
	for _, j := range o.Running {
		running = append(running, j.Id)
	}
	queued := o.Queued != nil && len(*o.Queued) > 0
	f := s.flagsOf(ctx, queued, running)
	for i := range o.Running {
		f.markJob(&o.Running[i])
	}
	if o.Queued == nil {
		return
	}
	for i := range *o.Queued {
		f.markJob(&(*o.Queued)[i])
	}
	if o.Waiting != nil {
		for i, w := range *o.Waiting {
			if _, ok := f.reserved[w.JobId]; ok {
				(*o.Waiting)[i].Reason = gen.WaitReasonReserved
				(*o.Waiting)[i].Words = scheduler.WaitWords(scheduler.WaitReserved, "")
			}
		}
	}
}

// markJob puts the reservation or the timer on the job as the contract shows it.
func (f jobFlags) markJob(j *gen.Job) {
	if since, ok := f.reserved[j.Id]; ok && j.State == gen.JobStateQueued {
		r := gen.WaitReasonReserved
		j.WaitReason, j.ReservedSince = &r, tptr(since)
	}
	if d, ok := f.grace[j.Id]; ok && j.State == gen.JobStateRunning {
		j.TimerDeadline = tptr(d)
	}
}
