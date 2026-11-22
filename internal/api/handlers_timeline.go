package api

import (
	"context"
	"net/http"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsdata"
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
	views, err := s.opts.Machines.List(ctx)
	if err != nil {
		return fail(err)
	}
	for _, v := range views {
		name := v.Machine.Name
		if name == "" {
			name = v.Machine.ID
		}
		in.Machines = append(in.Machines, timelineMachine{ID: v.Machine.ID, Name: name, Slots: v.Settings.JobSlots})
	}
	if in.Finished, err = s.finishedJobs(ctx, now); err != nil {
		return fail(err)
	}
	if in.Open, err = s.opts.JobsData.OpenJobs(ctx); err != nil {
		return fail(err)
	}
	if in.Forecasts, err = newForecaster(s.opts.JobsData, now).forJobs(ctx, in.Open); err != nil {
		return fail(err)
	}
	return gen.GetTimeline200JSONResponse(buildTimeline(in)), nil
}

// finishedJobs reads the done jobs that may have ended in the last 24 hours, newest first by id. The store filters on
// created_at, so jobs created up to 24 hours before the window are read too, and the builder keeps those that ended inside it.
func (s *server) finishedJobs(ctx context.Context, now time.Time) ([]jobsdata.Job, error) {
	var out []jobsdata.Job
	f := jobsdata.JobFilter{State: jobsdata.Done, From: now.Add(-2 * timelineHorizon), Limit: timelinePage}
	for range timelineCalls {
		rows, _, err := s.opts.JobsData.ListJobs(ctx, f)
		if err != nil {
			return nil, err
		}
		page := rows[:min(len(rows), timelinePage)]
		out = append(out, page...)
		if len(rows) <= timelinePage {
			break
		}
		f.After = page[len(page)-1].ID
	}
	return out, nil
}
