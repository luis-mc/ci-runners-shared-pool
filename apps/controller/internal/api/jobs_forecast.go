package api

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/forecast"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

// forecaster reads the samples a forecast needs. It lives for one request and remembers what it read, so the jobs
// of a page that share a workflow or a repository read those samples once; nothing is kept between requests.
//
// It also keeps the open jobs and the queue simulation of the request, so a page of many runs reads and forecasts the
// open jobs once, and prefetch loads the samples of a whole page in a few queries (CRSP290, audit F1, F2, F11).
type forecaster struct {
	r    jobsdata.Reader
	now  time.Time
	memo map[string][]jobsdata.Sample

	open      []jobsdata.Job
	openRead  bool
	queue     map[int64]queueInfo
	queueRead bool
}

func newForecaster(r jobsdata.Reader, now time.Time) *forecaster {
	return &forecaster{r: r, now: now, memo: map[string][]jobsdata.Sample{}}
}

func sampleKey(q jobsdata.SampleQuery) string {
	return fmt.Sprintf("%d|%s|%s|%s|%s", q.RepositoryID, q.JobKey, q.Workflow, q.JobType, q.StepName)
}

// openJobs is every queued and running job, read once per request.
func (f *forecaster) openJobs(ctx context.Context) ([]jobsdata.Job, error) {
	if f.openRead {
		return f.open, nil
	}
	open, err := f.r.OpenJobs(ctx)
	if err != nil {
		return nil, err
	}
	f.open, f.openRead = open, true
	return open, nil
}

// needsSamples says whether forJob reads samples for j.
func needsSamples(j jobsdata.Job) bool { return j.State == jobsdata.Queued || j.Forecast == nil }

// prefetch reads, for all the jobs at once, the samples forJob will ask for: the job-key level for every job, then
// the workflow level for the jobs whose key has too few, then the repository level. Each level is one batch read
// (the reader answers many questions in one query). A reader without batch reads is left to forJob, one question at
// a time. Nothing here changes an answer: forJob finds the same samples in the memo that it would have read.
func (f *forecaster) prefetch(ctx context.Context, lists ...[]jobsdata.Job) error {
	br, ok := f.r.(jobsdata.BatchReader)
	if !ok {
		return nil
	}
	var jobs []jobsdata.Job
	for _, l := range lists {
		for _, j := range l {
			if needsSamples(j) {
				jobs = append(jobs, j)
			}
		}
	}
	load := func(qs []jobsdata.SampleQuery) error {
		var todo []jobsdata.SampleQuery
		seen := map[string]bool{}
		for _, q := range qs {
			q.Since, q.Limit = f.now.Add(-forecast.MaxAge), forecast.Window
			k := sampleKey(q)
			if _, have := f.memo[k]; have || seen[k] {
				continue
			}
			seen[k] = true
			todo = append(todo, q)
		}
		if len(todo) == 0 {
			return nil
		}
		got, err := br.SamplesMany(ctx, todo)
		if err != nil {
			return err
		}
		for i, q := range todo {
			f.memo[sampleKey(q)] = got[i]
		}
		return nil
	}
	short := func(q jobsdata.SampleQuery) bool { return len(f.memo[sampleKey(q)]) < forecast.MinSamples }
	var l1, l2, l3 []jobsdata.SampleQuery
	for _, j := range jobs {
		l1 = append(l1, jobsdata.SampleQuery{RepositoryID: j.RepositoryID, JobKey: j.JobKey})
	}
	if err := load(l1); err != nil {
		return err
	}
	var rest []jobsdata.Job
	for _, j := range jobs {
		if q := (jobsdata.SampleQuery{RepositoryID: j.RepositoryID, JobKey: j.JobKey}); short(withLimits(q, f.now)) {
			rest = append(rest, j)
			if j.Workflow != "" {
				l2 = append(l2, jobsdata.SampleQuery{RepositoryID: j.RepositoryID, Workflow: j.Workflow, JobType: j.JobType})
			}
		}
	}
	if err := load(l2); err != nil {
		return err
	}
	for _, j := range rest {
		if j.Workflow != "" {
			if q := (jobsdata.SampleQuery{RepositoryID: j.RepositoryID, Workflow: j.Workflow, JobType: j.JobType}); !short(withLimits(q, f.now)) {
				continue
			}
		}
		l3 = append(l3, jobsdata.SampleQuery{RepositoryID: j.RepositoryID})
	}
	return load(l3)
}

// prepare reads ahead what forecasting jobs (and placing the queued ones) will need: the open jobs when one of jobs
// is queued, then the samples of all of them in a few batch reads.
func (f *forecaster) prepare(ctx context.Context, jobs ...[]jobsdata.Job) error {
	lists := jobs
	for _, l := range jobs {
		if slices.ContainsFunc(l, func(j jobsdata.Job) bool { return j.State == jobsdata.Queued }) {
			open, err := f.openJobs(ctx)
			if err != nil {
				return err
			}
			lists = append(lists[:len(lists):len(lists)], open)
			break
		}
	}
	return f.prefetch(ctx, lists...)
}

func withLimits(q jobsdata.SampleQuery, now time.Time) jobsdata.SampleQuery {
	q.Since, q.Limit = now.Add(-forecast.MaxAge), forecast.Window
	return q
}

func (f *forecaster) samples(ctx context.Context, q jobsdata.SampleQuery) ([]jobsdata.Sample, error) {
	q.Since, q.Limit = f.now.Add(-forecast.MaxAge), forecast.Window
	key := sampleKey(q)
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
	if fc.queueRead {
		return fc.queue, nil
	}
	open, err := fc.openJobs(ctx)
	if err != nil {
		return nil, err
	}
	if err := fc.prefetch(ctx, open); err != nil {
		return nil, err
	}
	fcs, err := fc.forJobs(ctx, open)
	if err != nil {
		return nil, err
	}
	fc.queue, fc.queueRead = queueETAs(open, fcs, s.slotCounts(ctx), fc.now), true
	return fc.queue, nil
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
