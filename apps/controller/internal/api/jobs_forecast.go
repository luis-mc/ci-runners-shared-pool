package api

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/forecast"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

// forecaster reads the samples a forecast needs. It lives for one request and remembers what it read, so the jobs
// of a page that share a workflow or a repository read those samples once; nothing is kept between requests.
type forecaster struct {
	r    jobsdata.Reader
	now  time.Time
	memo map[string][]jobsdata.Sample
}

func newForecaster(r jobsdata.Reader, now time.Time) *forecaster {
	return &forecaster{r: r, now: now, memo: map[string][]jobsdata.Sample{}}
}

func (f *forecaster) samples(ctx context.Context, q jobsdata.SampleQuery) ([]jobsdata.Sample, error) {
	q.Since, q.Limit = f.now.Add(-forecast.MaxAge), forecast.Window
	key := fmt.Sprintf("%d|%s|%s|%s|%s", q.RepositoryID, q.JobKey, q.Workflow, q.JobType, q.StepName)
	if got, ok := f.memo[key]; ok {
		return got, nil
	}
	got, err := f.r.Samples(ctx, q)
	if err != nil {
		return nil, err
	}
	f.memo[key] = got
	return got, nil
}

// forJob is the forecast of one job. A job that started carries the forecast made then (so the charts compare the
// same number with the actual time); a queued job, or one without a stored forecast, is forecast from the samples
// of its job key, else of its workflow and job type, else of its repository (the next level is read only when the
// level before has too few samples).
func (f *forecaster) forJob(ctx context.Context, j jobsdata.Job) (forecast.Forecast, error) {
	if j.State != jobsdata.Queued && j.Forecast != nil {
		return recordForecast(*j.Forecast), nil
	}
	var c forecast.Chain
	var err error
	if c.JobKey, err = f.samples(ctx, jobsdata.SampleQuery{RepositoryID: j.RepositoryID, JobKey: j.JobKey}); err != nil {
		return forecast.Forecast{}, err
	}
	if len(c.JobKey) < forecast.MinSamples {
		if j.Workflow != "" {
			if c.Workflow, err = f.samples(ctx, jobsdata.SampleQuery{RepositoryID: j.RepositoryID, Workflow: j.Workflow, JobType: j.JobType}); err != nil {
				return forecast.Forecast{}, err
			}
		}
		if len(c.Workflow) < forecast.MinSamples {
			if c.Repository, err = f.samples(ctx, jobsdata.SampleQuery{RepositoryID: j.RepositoryID}); err != nil {
				return forecast.Forecast{}, err
			}
		}
	}
	return forecast.ForChain(c, f.now, j.MachineID), nil
}

// forJobs forecasts each job by id.
func (f *forecaster) forJobs(ctx context.Context, jobs []jobsdata.Job) (map[int64]forecast.Forecast, error) {
	out := make(map[int64]forecast.Forecast, len(jobs))
	for _, j := range jobs {
		fc, err := f.forJob(ctx, j)
		if err != nil {
			return nil, err
		}
		out[j.ID] = fc
	}
	return out, nil
}

// forStep forecasts a step of a job key from step samples (kept 30 days); ok is false without enough of them.
func (f *forecaster) forStep(ctx context.Context, repositoryID int64, jobKey, step string) (forecast.Forecast, bool, error) {
	got, err := f.samples(ctx, jobsdata.SampleQuery{RepositoryID: repositoryID, JobKey: jobKey, StepName: step})
	if err != nil {
		return forecast.Forecast{}, false, err
	}
	sorted := append([]jobsdata.Sample(nil), got...)
	sort.SliceStable(sorted, func(a, b int) bool { return sorted[a].At.Before(sorted[b].At) })
	seconds := make([]float64, len(sorted))
	for i, s := range sorted {
		seconds[i] = s.Seconds
	}
	fc := forecast.FromSeconds(seconds, forecast.BasisJobKey, "")
	return fc, fc.Basis != forecast.BasisNone, nil
}

// slotCounts is the job slots of each machine, "" for the whole pool. The queue simulation shares the machine's
// job slots (Settings.JobSlots) between every job; the light and heavy classes are not told apart.
func (s *server) slotCounts(ctx context.Context) map[string]int {
	out := map[string]int{}
	if s.opts.Machines == nil {
		return out
	}
	views, err := s.opts.Machines.List(ctx)
	if err != nil {
		s.opts.Logger.Warn("job slots for the queue forecast", "err", err)
		return out
	}
	for _, v := range views {
		out[v.Machine.ID] = v.Settings.JobSlots
		out[""] += v.Settings.JobSlots
	}
	return out
}

// queueFor gives the queue place and ETA of every queued job in the system, when jobs holds a queued job.
func (s *server) queueFor(ctx context.Context, fc *forecaster, jobs []jobsdata.Job) (map[int64]queueInfo, error) {
	queued := false
	for _, j := range jobs {
		queued = queued || j.State == jobsdata.Queued
	}
	if !queued {
		return nil, nil
	}
	open, err := s.opts.JobsData.OpenJobs(ctx)
	if err != nil {
		return nil, err
	}
	fcs, err := fc.forJobs(ctx, open)
	if err != nil {
		return nil, err
	}
	return queueETAs(open, fcs, s.slotCounts(ctx), fc.now), nil
}

// item is a job as the contract shows it: its forecast and, when queued, its place and ETA.
func jobWithForecast(j jobsdata.Job, fc forecast.Forecast, q queueInfo, hasQ bool) gen.Job {
	out := toJobItem(j)
	f := toForecast(fc)
	out.Forecast = &f
	if hasQ && j.State == jobsdata.Queued {
		out.QueuePosition = ptr(q.Position)
		if q.Known {
			out.QueueEta = toEta(q.Eta)
		}
	}
	return out
}
