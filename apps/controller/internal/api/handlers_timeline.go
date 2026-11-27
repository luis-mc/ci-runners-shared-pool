package api

import (
	"golang.org/x/sync/errgroup"

	"context"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"net/http"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

func init() { otherAreaAuth["getTimeline"] = authUser }

const (
	timelinePage  = 100 // jobs read per store call
	timelineCalls = 50  // at most this many store calls for the finished jobs
)

// GetTimeline implements GET /api/v1/timeline (LOOK58, observability R5.66).
func (s *server) GetTimeline(ctx context.Context, req gen.GetTimelineRequestObject) (gen.GetTimelineResponseObject, error) {
	if s.opts.JobsData == nil || s.opts.Machines == nil {
		return gen.GetTimelinedefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notWired()}, nil
	}
	fail := func(err error) (gen.GetTimelineResponseObject, error) {
		s.opts.Logger.Error("timeline", "err", err)
		return gen.GetTimelinedefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errBody(CodeInternal, "internal error")}, nil
	}
	now := s.now().UTC()
	in := timelineInput{Now: now, Before: req.Params.Before, Hours: timelineHours}
	if req.Params.Hours != nil {
		in.Hours = *req.Params.Hours
	}
	if req.Params.MachineId != nil {
		in.MachineID = *req.Params.MachineId
	}
	// Machines, finished jobs and open jobs are independent reads: run them together.
	var (
		views []machines.View
		err   error
	)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(4)
	g.Go(func() (e error) { views, e = s.opts.Machines.List(gctx); return e })
	g.Go(func() (e error) { in.Finished, in.OlderFinished, e = s.finishedJobs(gctx, in); return e })
	g.Go(func() (e error) { in.Open, e = s.opts.JobsData.OpenJobs(gctx); return e })
	if err = g.Wait(); err != nil {
		return fail(err)
	}
	for _, v := range views {
		name := v.Machine.Name
		if name == "" {
			name = v.Machine.ID
		}
		in.Machines = append(in.Machines, timelineMachine{ID: v.Machine.ID, Name: name, Slots: v.Settings.JobSlots})
	}
	in.Flags = s.flagsFor(ctx, in.Open)
	fc := newForecaster(s.opts.JobsData, now)
	fc.open, fc.openRead = in.Open, true
	if err = fc.prepare(ctx, in.Open); err != nil {
		return fail(err)
	}
	if in.Forecasts, err = fc.forJobs(ctx, in.Open); err != nil {
		return fail(err)
	}
	return gen.GetTimeline200JSONResponse(buildTimeline(in)), nil
}

// finishedJobs reads the done jobs the view needs. A reader that can read a time window (jobsdata.TimelineReader) gives
// only the jobs that ended inside the window being shown, and says whether older ones exist within 24 hours; the
// window is hours long, ends at the "before" mark or now, and never reaches past the 24-hour horizon. Other readers
// are read the long way: every done job of 48 hours, a page at a time.
func (s *server) finishedJobs(ctx context.Context, in timelineInput) (jobs []jobsdata.Job, older bool, err error) {
	if tr, ok := s.opts.JobsData.(jobsdata.TimelineReader); ok {
		floor := in.Now.Add(-timelineHorizon)
		upper := in.Now
		if in.Before != nil && in.Before.Before(upper) {
			upper = in.Before.UTC()
		}
		hours := min(max(in.Hours, 1), 24)
		lower := upper.Add(-time.Duration(hours) * time.Hour)
		if lower.Before(floor) {
			lower = floor
		}
		g, gctx := errgroup.WithContext(ctx)
		g.Go(func() (e error) {
			// A little past the window's end, so a job that ended at the mark itself is not cut.
			jobs, e = tr.FinishedBetween(gctx, lower, upper.Add(time.Second), timelinePage*timelineCalls)
			return e
		})
		g.Go(func() (e error) {
			if !lower.After(floor) {
				return nil
			}
			older, e = tr.AnyFinishedBetween(gctx, floor, lower, in.MachineID)
			return e
		})
		return jobs, older, g.Wait()
	}
	var out []jobsdata.Job
	f := jobsdata.JobFilter{State: jobsdata.Done, From: in.Now.Add(-2 * timelineHorizon), Limit: timelinePage}
	for range timelineCalls {
		rows, _, err := s.opts.JobsData.ListJobs(ctx, f)
		if err != nil {
			return nil, false, err
		}
		page := rows[:min(len(rows), timelinePage)]
		out = append(out, page...)
		if len(rows) <= timelinePage {
			break
		}
		f.After = page[len(page)-1].ID
	}
	return out, false, nil
}
