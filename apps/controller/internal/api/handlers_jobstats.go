package api

import (
	"context"
	"net/http"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/forecast"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

// The groups, stats, summaries and forecast routes of the Jobs section (OPS34 D2, D4, D9, D10).

func toGroupRow(g jobsdata.GroupRow) gen.JobGroupRow {
	trend := make([]float32, len(g.Trend))
	for i, v := range g.Trend {
		trend[i] = fl32(v)
	}
	return gen.JobGroupRow{Name: g.Name, Filter: g.Filter, Running: g.Running, Queued: g.Queued, Done: g.Done, Failed: g.Failed,
		WaitP50: fl32(g.WaitP50), WaitP90: fl32(g.WaitP90), RunP50: fl32(g.RunP50), RunP90: fl32(g.RunP90), Trend: trend, Light: g.Light}
}

func toJobStats(st jobsdata.Stats) gen.JobStats {
	out := gen.JobStats{Days: []gen.DayStat{}, Hours: []gen.HourStat{}, ForecastPoints: []gen.ForecastPoint{}}
	for _, d := range st.Days {
		out.Days = append(out.Days, gen.DayStat{Day: d.Day, Jobs: d.Jobs, Failed: d.Failed, WaitP50: fl32(d.WaitP50), WaitP90: fl32(d.WaitP90),
			RunP50: fl32(d.RunP50), RunP90: fl32(d.RunP90)})
	}
	for _, h := range st.Hours {
		out.Hours = append(out.Hours, gen.HourStat{Weekday: h.Weekday, Hour: h.Hour, Count: h.Count})
	}
	for _, p := range st.ForecastPoints {
		fp := gen.ForecastPoint{JobId: p.JobID, ForecastSeconds: fl32(p.ForecastS), ActualSeconds: fl32(p.ActualS), Lo: fl32(p.LoS), Hi: fl32(p.HiS), Basis: p.Basis}
		if note := forecast.BasisText(forecast.Forecast{Basis: forecast.Basis(p.Basis)}); note != "" {
			fp.Note = &note
		}
		out.ForecastPoints = append(out.ForecastPoints, fp)
	}
	return out
}

func toJobSummary(d jobsdata.DailySummary) gen.JobSummary {
	return gen.JobSummary{Day: d.Day, Repository: d.Repository, JobType: d.JobType, Jobs: d.Jobs, Failed: d.Failed, WaitP50Ms: d.WaitP50MS,
		WaitP90Ms: d.WaitP90MS, RunP50Ms: d.RunP50MS, RunP90Ms: d.RunP90MS}
}

// ListJobGroups implements GET /api/v1/jobs/groups.
func (s *server) ListJobGroups(ctx context.Context, req gen.ListJobGroupsRequestObject) (gen.ListJobGroupsResponseObject, error) {
	fail := func(status int, code, msg string) (gen.ListJobGroupsResponseObject, error) {
		return gen.ListJobGroupsdefaultJSONResponse{StatusCode: status, Body: errBody(code, msg)}, nil
	}
	if s.opts.JobsData == nil {
		return fail(http.StatusNotImplemented, CodeNotImplemented, "this operation is not built yet")
	}
	by := jobsdata.GroupBy(ptrString(req.Params.By))
	switch by {
	case jobsdata.ByRepository, jobsdata.ByMachine, jobsdata.ByRunner, jobsdata.ByRunnerType, jobsdata.ByJobType:
	default:
		return fail(http.StatusBadRequest, CodeBadRequest, "by must be repository, machine, runner, runner-type or job-type")
	}
	if etag, ok := s.notModified(ctx, s.now()); ok {
		return notModifiedResponse{etag}, nil
	}
	rows, err := s.opts.JobsData.Groups(ctx, by)
	if err != nil {
		s.opts.Logger.Error("list job groups", "err", err)
		return fail(http.StatusInternalServerError, CodeInternal, "internal error")
	}
	out := gen.JobGroupList{Items: []gen.JobGroupRow{}}
	for _, r := range rows {
		out.Items = append(out.Items, toGroupRow(r))
	}
	return gen.ListJobGroups200JSONResponse(out), nil
}

// GetJobStats implements GET /api/v1/jobs/stats.
func (s *server) GetJobStats(ctx context.Context, req gen.GetJobStatsRequestObject) (gen.GetJobStatsResponseObject, error) {
	fail := func(status int, code, msg string) (gen.GetJobStatsResponseObject, error) {
		return gen.GetJobStatsdefaultJSONResponse{StatusCode: status, Body: errBody(code, msg)}, nil
	}
	if s.opts.JobsData == nil {
		return fail(http.StatusNotImplemented, CodeNotImplemented, "this operation is not built yet")
	}
	p := req.Params
	days := 14
	if p.Days != nil {
		days = int(*p.Days)
	}
	if days != 7 && days != 14 && days != 30 {
		return fail(http.StatusBadRequest, CodeBadRequest, "days must be 7, 14 or 30")
	}
	q := jobQuery{RepositoryID: p.RepositoryId, MachineID: p.MachineId, Backend: p.Backend, Runner: p.Runner, JobType: p.JobType, JobKey: p.JobIdent,
		Branch: p.Branch, RunID: p.RunId}
	if p.State != nil {
		q.State = ptrOf(string(*p.State))
	}
	if p.Result != nil {
		q.Result = ptrOf(string(*p.Result))
	}
	if p.RunnerKind != nil {
		q.RunnerKind = ptrOf(string(*p.RunnerKind))
	}
	f, msg := q.jobFilter()
	if msg != "" {
		return fail(http.StatusBadRequest, CodeBadRequest, msg)
	}
	if etag, ok := s.notModified(ctx, s.now()); ok {
		return notModifiedResponse{etag}, nil
	}
	st, err := s.opts.JobsData.Stats(ctx, days, f)
	if err != nil {
		s.opts.Logger.Error("job stats", "err", err)
		return fail(http.StatusInternalServerError, CodeInternal, "internal error")
	}
	return gen.GetJobStats200JSONResponse(toJobStats(st)), nil
}

// ListJobSummaries implements GET /api/v1/jobs/summaries.
func (s *server) ListJobSummaries(ctx context.Context, req gen.ListJobSummariesRequestObject) (gen.ListJobSummariesResponseObject, error) {
	fail := func(status int, code, msg string) (gen.ListJobSummariesResponseObject, error) {
		return gen.ListJobSummariesdefaultJSONResponse{StatusCode: status, Body: errBody(code, msg)}, nil
	}
	if s.opts.JobsData == nil {
		return fail(http.StatusNotImplemented, CodeNotImplemented, "this operation is not built yet")
	}
	rows, err := s.opts.JobsData.Summaries(ctx, timeOf(req.Params.From), timeOf(req.Params.To))
	if err != nil {
		s.opts.Logger.Error("job summaries", "err", err)
		return fail(http.StatusInternalServerError, CodeInternal, "internal error")
	}
	out := gen.JobSummaryList{Items: []gen.JobSummary{}}
	for _, r := range rows {
		out.Items = append(out.Items, toJobSummary(r))
	}
	return gen.ListJobSummaries200JSONResponse(out), nil
}

// GetForecast implements GET /api/v1/forecast: the forecast of a job key from its samples, its workflow's or its
// repository's, with the steps of its newest finished job and its last 30 runs.
func (s *server) GetForecast(ctx context.Context, req gen.GetForecastRequestObject) (gen.GetForecastResponseObject, error) {
	fail := func(status int, code, msg string) (gen.GetForecastResponseObject, error) {
		return gen.GetForecastdefaultJSONResponse{StatusCode: status, Body: errBody(code, msg)}, nil
	}
	if s.opts.JobsData == nil {
		return fail(http.StatusNotImplemented, CodeNotImplemented, "this operation is not built yet")
	}
	var key string
	var repo int64
	if req.Params.JobIdent != nil {
		key = *req.Params.JobIdent
	}
	if req.Params.RepositoryId != nil {
		repo = *req.Params.RepositoryId
	}
	if key == "" || repo < 1 {
		return fail(http.StatusBadRequest, CodeBadRequest, "job_ident and repository_id are required")
	}
	out, err := s.forecastAnswer(ctx, repo, key)
	if err != nil {
		s.opts.Logger.Error("forecast", "err", err)
		return fail(http.StatusInternalServerError, CodeInternal, "internal error")
	}
	return gen.GetForecast200JSONResponse(out), nil
}

func (s *server) forecastAnswer(ctx context.Context, repo int64, key string) (gen.ForecastAnswer, error) {
	r := s.opts.JobsData
	fc := newForecaster(r, s.now())
	// The newest job of the key gives the workflow and job type for the fall-back levels and the step names.
	newest, _, err := r.ListJobs(ctx, jobsdata.JobFilter{RepositoryID: repo, JobKey: key, State: jobsdata.Done, Limit: 1})
	if err != nil {
		return gen.ForecastAnswer{}, err
	}
	probe := jobsdata.Job{RepositoryID: repo, JobKey: key, State: jobsdata.Queued}
	steps := []gen.StepForecast{}
	if len(newest) > 0 {
		probe.Workflow, probe.JobType = newest[0].Workflow, newest[0].JobType
		d, ok, err := r.GetJob(ctx, newest[0].ID)
		if err != nil {
			return gen.ForecastAnswer{}, err
		}
		for _, st := range d.Steps {
			if !ok {
				break
			}
			sf, has, err := fc.forStep(ctx, repo, key, st.Name)
			if err != nil {
				return gen.ForecastAnswer{}, err
			}
			if has {
				steps = append(steps, gen.StepForecast{Name: st.Name, Forecast: toForecast(sf)})
			}
		}
	}
	jf, err := fc.forJob(ctx, probe)
	if err != nil {
		return gen.ForecastAnswer{}, err
	}
	hist, err := r.History(ctx, repo, key, historyLen)
	if err != nil {
		return gen.ForecastAnswer{}, err
	}
	return gen.ForecastAnswer{JobIdent: key, Forecast: toForecast(jf), Steps: steps, History: toHistory(hist)}, nil
}

func ptrString[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}
