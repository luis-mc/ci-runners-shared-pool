package api

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/history"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

var commitSHA = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// slowestStepsShown is how many steps the commit view names for its slowest job (R5.13).
const slowestStepsShown = 2

// GetCommitView implements GET /api/v1/commits/{sha} (observability R5.13): every run of one head commit, whether
// all of them are done, and where the time went. It reads stored rows only; the pure work is in internal/history.
func (s *server) GetCommitView(ctx context.Context, req gen.GetCommitViewRequestObject) (gen.GetCommitViewResponseObject, error) {
	fail := func(status int, code, msg string) (gen.GetCommitViewResponseObject, error) {
		return gen.GetCommitViewdefaultJSONResponse{StatusCode: status, Body: errBody(code, msg)}, nil
	}
	cr, ok := s.opts.JobsData.(jobsdata.CommitReader)
	if s.opts.JobsData == nil || !ok {
		return fail(http.StatusNotImplemented, CodeNotImplemented, "this operation is not built yet")
	}
	if !commitSHA.MatchString(req.Sha) {
		return fail(http.StatusBadRequest, CodeBadRequest, "name the commit with 7 to 40 hex characters")
	}
	var repoID int64
	if req.Params.RepositoryId != nil {
		repoID = *req.Params.RepositoryId
	}
	sha := strings.ToLower(req.Sha)
	runs, err := cr.RunsOfCommit(ctx, repoID, sha)
	if err != nil {
		s.opts.Logger.Error("commit view", "err", err)
		return fail(http.StatusInternalServerError, CodeInternal, "internal error")
	}
	if len(runs) == 0 {
		return fail(http.StatusNotFound, CodeNotFound, "no run of this commit is stored")
	}
	sum := history.SummarizeCommit(runs, s.now())
	out := gen.CommitView{
		HeadSha: runs[0].HeadSHA, Repository: ptr(runs[0].Repository), AllRunsDone: sum.AllRunsDone,
		RunsTotal: len(sum.Runs), RunsDone: sum.RunsDone, WallSeconds: sum.WallSeconds, Runs: make([]gen.CommitRun, 0, len(sum.Runs)),
	}
	for _, r := range sum.Runs {
		out.Runs = append(out.Runs, gen.CommitRun{
			Id: r.RunID, Attempt: r.Attempt, Workflow: r.Workflow, State: gen.CommitRunState(r.State), Result: jobResult(string(r.Result)),
			CreatedAt: r.CreatedAt, CompletedAt: tptr(r.CompletedAt), HtmlUrl: ptr(r.HTMLURL), JobsTotal: r.Jobs, JobsDone: r.JobsDone,
		})
	}
	if j := sum.Slowest; j != nil {
		steps := []gen.CommitStep{}
		d, found, err := s.opts.JobsData.GetJob(ctx, j.JobID)
		if err != nil {
			s.opts.Logger.Error("commit view", "err", err)
			return fail(http.StatusInternalServerError, CodeInternal, "internal error")
		}
		if found {
			for _, st := range history.SlowestSteps(d.Steps, slowestStepsShown) {
				steps = append(steps, gen.CommitStep{Name: st.Name, Seconds: st.Seconds})
			}
		}
		out.SlowestJob = &gen.CommitSlowJob{JobId: j.JobID, RunId: j.RunID, Name: j.Name, Workflow: j.Workflow,
			Seconds: j.Seconds, SharePercent: j.SharePercent, SlowestSteps: steps}
	}
	return gen.GetCommitView200JSONResponse(out), nil
}
