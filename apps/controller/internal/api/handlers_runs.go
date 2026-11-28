package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

// The runs routes of the Jobs section: listRuns, getRun (OPS34 D3, D4, D6).

// runView is a run with what the screens need beyond its rows.
type runView struct {
	Jobs         []gen.Job
	Edges        [][]string
	CriticalPath []string
	Eta          *gen.Eta
	Graph        gen.RunGraph
}

// buildRun forecasts the jobs of a run, places the queued ones, draws the needs graph and finds the critical path.
func (s *server) buildRun(ctx context.Context, fc *forecaster, d jobsdata.RunDetail) (runView, error) {
	if err := fc.prepare(ctx, d.Jobs); err != nil {
		return runView{}, err
	}
	fcs, err := fc.forJobs(ctx, d.Jobs)
	if err != nil {
		return runView{}, err
	}
	qi, err := s.queueFor(ctx, fc, d.Jobs)
	if err != nil {
		return runView{}, err
	}
	edges, needs, graph := graphEdges(d)
	v := runView{Edges: edges, Graph: graph, Jobs: make([]gen.Job, 0, len(d.Jobs))}
	for _, j := range d.Jobs {
		q, hasQ := qi[j.ID]
		item := withRun(jobWithForecast(j, fcs[j.ID], q, hasQ), d.Run, j.Worktree)
		n := needs[j.Name]
		if n == nil {
			n = []string{}
		}
		item.Needs = &n
		v.Jobs = append(v.Jobs, item)
	}
	v.CriticalPath, v.Eta = criticalPath(d, runNodes(d, needs, fcs, qi, fc.now))
	return v, nil
}

func toRunSummary(r jobsdata.RunRow, now func() int64) gen.RunSummary {
	out := gen.RunSummary{
		Id: r.RunID, Attempt: r.Attempt, Repository: r.Repository, RepositoryId: ptr(r.RepositoryID), Workflow: r.Workflow,
		Branch: r.Branch, HeadSha: r.HeadSHA, PullRequest: ptr(r.PullRequest), TaskId: ptr(r.TaskID), Worktree: ptr(r.Worktree),
		Trigger: r.Event, HtmlUrl: r.HTMLURL, State: gen.RunSummaryState(r.State), Result: jobResult(string(r.Conclusion)),
		CreatedAt: r.CreatedAt, CompletedAt: tptr(r.CompletedAt), JobsTotal: r.JobsTotal, JobsDone: r.JobsDone, JobsFailed: r.JobsFailed,
	}
	out.OverallState = gen.RunSummaryOverallState(jobsdata.OverallOf(r.State, followUpsOf(r)))
	out.FollowUps, out.FollowUpsOpen = ptr(r.FollowUps), ptr(r.FollowUpsOpen)
	sn := toStartedNext(r.StartedNext)
	out.StartedNext = &sn
	if r.ParentRunID != 0 {
		out.StartedBy = ptr(r.ParentRunID)
	}
	if r.TotalMS > 0 {
		out.TotalSeconds = ptr(int(r.TotalMS / 1000))
	} else if r.State != jobsdata.Done && !r.CreatedAt.IsZero() {
		if n := now() - r.CreatedAt.Unix(); n > 0 {
			out.TotalSeconds = ptr(int(n))
		}
	}
	return out
}

// followUpsOf gives OverallOf the follow-ups of a row; a row from a test or a reader that only counts has stubs.
func followUpsOf(r jobsdata.RunRow) []jobsdata.FollowUp {
	if len(r.StartedNext) > 0 {
		return r.StartedNext
	}
	out := make([]jobsdata.FollowUp, 0, r.FollowUps)
	for i := 0; i < r.FollowUps; i++ {
		st := jobsdata.Done
		if i < r.FollowUpsOpen {
			st = jobsdata.Running
		}
		out = append(out, jobsdata.FollowUp{State: st})
	}
	return out
}

func toStartedNext(list []jobsdata.FollowUp) []gen.RunFollowUp {
	out := make([]gen.RunFollowUp, 0, len(list))
	for _, f := range list {
		out = append(out, gen.RunFollowUp{WorkflowName: f.WorkflowName, RunId: f.RunID, Attempt: ptr(f.Attempt),
			State: gen.RunFollowUpState(f.State), Result: jobResult(string(f.Result)), StartedAt: tptr(f.StartedAt),
			CompletedAt: tptr(f.CompletedAt), Link: f.HTMLURL})
	}
	return out
}

// ListRuns implements GET /api/v1/runs: newest first, keyset-paged on the store's sort key.
func (s *server) ListRuns(ctx context.Context, req gen.ListRunsRequestObject) (gen.ListRunsResponseObject, error) {
	fail := func(status int, code, msg string) (gen.ListRunsResponseObject, error) {
		return gen.ListRunsdefaultJSONResponse{StatusCode: status, Body: errBody(code, msg)}, nil
	}
	if s.opts.JobsData == nil {
		return fail(http.StatusNotImplemented, CodeNotImplemented, "this operation is not built yet")
	}
	p := req.Params
	limit, ok := pageLimit(p.Limit)
	if !ok {
		return fail(http.StatusBadRequest, CodeBadRequest, "limit must be 1 to 100")
	}
	after, ok := cursorID(p.Cursor, "r")
	if !ok {
		return fail(http.StatusBadRequest, CodeBadRequest, "the cursor is not valid")
	}
	q := jobQuery{State: nil, RepositoryID: p.RepositoryId, Branch: p.Branch, From: p.From, To: p.To}
	if p.State != nil {
		q.State = ptrOf(string(*p.State))
	}
	if p.Result != nil {
		q.Result = ptrOf(string(*p.Result))
	}
	jf, msg := q.jobFilter()
	if msg != "" {
		return fail(http.StatusBadRequest, CodeBadRequest, msg)
	}
	f := jobsdata.RunFilter{State: jf.State, Result: jf.Result, RepositoryID: jf.RepositoryID, Branch: jf.Branch, From: jf.From, To: jf.To, After: after, Limit: limit}
	rows, total, err := s.opts.JobsData.ListRuns(ctx, f)
	if err != nil {
		s.opts.Logger.Error("list runs", "err", err)
		return fail(http.StatusInternalServerError, CodeInternal, "internal error")
	}
	page := gen.RunPage{Items: []gen.RunSummary{}, Stale: gen.Staleness{}, Total: total}
	if len(rows) > limit {
		rows = rows[:limit]
		page.NextCursor = ptr("r" + strconv.FormatInt(rows[len(rows)-1].SortKey, 10))
	}
	etas, err := s.openRunEtas(ctx, newForecaster(s.opts.JobsData, s.now()), rows)
	if err != nil {
		s.opts.Logger.Error("list runs", "err", err)
		return fail(http.StatusInternalServerError, CodeInternal, "internal error")
	}
	for _, r := range rows {
		item := toRunSummary(r, func() int64 { return s.now().Unix() })
		item.Eta = etas[r.SortKey]
		page.Items = append(page.Items, item)
	}
	return gen.ListRuns200JSONResponse(page), nil
}

// openRunEtas gives the ETA of each open run of the page, by sort key. The jobs and graphs of all the open runs are
// read together when the reader can (RunsWithJobs), and the samples of all their jobs and of the open jobs in a few
// batch reads, so the page costs a handful of queries however many runs are open.
func (s *server) openRunEtas(ctx context.Context, fc *forecaster, rows []jobsdata.RunRow) (map[int64]*gen.Eta, error) {
	var open []jobsdata.RunRow
	for _, r := range rows {
		if r.State != jobsdata.Done {
			open = append(open, r)
		}
	}
	out := map[int64]*gen.Eta{}
	if len(open) == 0 {
		return out, nil
	}
	var details []jobsdata.RunDetail
	if br, ok := s.opts.JobsData.(jobsdata.BatchReader); ok {
		var err error
		if details, err = br.RunsWithJobs(ctx, open); err != nil {
			return nil, err
		}
	} else {
		for _, r := range open {
			d, ok, err := s.opts.JobsData.GetRun(ctx, r.RunID, r.Attempt)
			if err != nil {
				return nil, err
			}
			if ok {
				details = append(details, d)
			}
		}
	}
	lists := make([][]jobsdata.Job, len(details))
	for i, d := range details {
		lists[i] = d.Jobs
	}
	if err := fc.prepare(ctx, lists...); err != nil {
		return nil, err
	}
	for _, d := range details {
		v, err := s.buildRun(ctx, fc, d)
		if err != nil {
			return nil, err
		}
		out[d.SortKey] = v.Eta
	}
	return out, nil
}

// GetRun implements GET /api/v1/runs/{runId}. Without attempt the newest attempt is read (attempt 0 to the store).
func (s *server) GetRun(ctx context.Context, req gen.GetRunRequestObject) (gen.GetRunResponseObject, error) {
	fail := func(status int, code, msg string) (gen.GetRunResponseObject, error) {
		return gen.GetRundefaultJSONResponse{StatusCode: status, Body: errBody(code, msg)}, nil
	}
	if s.opts.JobsData == nil {
		return fail(http.StatusNotImplemented, CodeNotImplemented, "this operation is not built yet")
	}
	attempt := 0
	if req.Params.Attempt != nil {
		attempt = *req.Params.Attempt
	}
	d, ok, err := s.opts.JobsData.GetRun(ctx, req.RunId, attempt)
	if err != nil {
		s.opts.Logger.Error("get run", "err", err)
		return fail(http.StatusInternalServerError, CodeInternal, "internal error")
	}
	if !ok {
		return fail(http.StatusNotFound, CodeNotFound, "no such run")
	}
	v, err := s.buildRun(ctx, newForecaster(s.opts.JobsData, s.now()), d)
	if err != nil {
		s.opts.Logger.Error("get run", "err", err)
		return fail(http.StatusInternalServerError, CodeInternal, "internal error")
	}
	sum := toRunSummary(d.RunRow, func() int64 { return s.now().Unix() })
	attempts := d.Attempts
	if attempts == nil {
		attempts = []int{d.Attempt}
	}
	return gen.GetRun200JSONResponse(gen.RunItem{
		Id: sum.Id, Attempt: sum.Attempt, Attempts: attempts, Repository: sum.Repository, RepositoryId: sum.RepositoryId, Workflow: sum.Workflow,
		Branch: sum.Branch, HeadSha: sum.HeadSha, PullRequest: sum.PullRequest, TaskId: sum.TaskId, Worktree: sum.Worktree, Trigger: sum.Trigger,
		HtmlUrl: sum.HtmlUrl, State: gen.RunItemState(sum.State), Result: sum.Result, CreatedAt: sum.CreatedAt, CompletedAt: sum.CompletedAt,
		OverallState: gen.RunItemOverallState(sum.OverallState), FollowUps: sum.FollowUps, FollowUpsOpen: sum.FollowUpsOpen,
		StartedBy: sum.StartedBy, StartedNext: toStartedNext(d.StartedNext),
		Jobs: v.Jobs, Edges: v.Edges, CriticalPath: v.CriticalPath, Eta: v.Eta, Graph: v.Graph,
	}), nil
}
