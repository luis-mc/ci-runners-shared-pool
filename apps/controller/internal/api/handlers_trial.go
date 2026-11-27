package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/trial"
)

// TrialService is what the trial routes need; *trial.Manager implements it.
type TrialService interface {
	Get(ctx context.Context, repo string) (trial.State, error)
	Report(ctx context.Context, repo string) (trial.Report, error)
	Set(ctx context.Context, repo string, mode trial.Mode, o trial.Options) (trial.State, error)
}

func trialSide(s trial.SideStats) gen.TrialSide {
	return gen.TrialSide{Runs: s.Runs, Succeeded: s.Succeeded, AvgWaitSec: s.AvgWaitSec, AvgRunSec: s.AvgRunSec}
}

func toTrial(st trial.State, rep trial.Report) gen.Trial {
	out := gen.Trial{Repository: st.Repo, Mode: gen.TrialMode(st.Mode), Variables: nonNil(rep.Variables), Total: rep.Total,
		Hosted: trialSide(rep.Hosted), Crsp: trialSide(rep.Crsp), Jobs: []gen.TrialJob{}}
	if !st.Since.IsZero() {
		t := st.Since
		out.Since = &t
	}
	if st.Percent > 0 {
		p := st.Percent
		out.Percent = &p
	}
	if len(st.Jobs) > 0 {
		j := st.Jobs
		out.CanaryJobs = &j
	}
	if st.ShadowWorkflow != "" {
		w := st.ShadowWorkflow
		out.ShadowWorkflow = &w
	}
	if len(st.ShadowJobs) > 0 {
		j := st.ShadowJobs
		out.ShadowJobs = &j
	}
	for _, j := range rep.Jobs {
		out.Jobs = append(out.Jobs, gen.TrialJob{Job: j.Job, Hosted: trialSide(j.Hosted), Crsp: trialSide(j.Crsp), WouldTake: j.WouldTake, Agree: j.Agree})
	}
	return out
}

// trialErr maps a refusal of the trial to the error envelope.
func trialErr(err error) error {
	if errors.Is(err, trial.ErrPublic) || errors.Is(err, trial.ErrNoJobs) || errors.Is(err, trial.ErrBadPercent) || errors.Is(err, trial.ErrTwoSteps) {
		return auth.Invalid("%s", err.Error())
	}
	return err
}

func (s *server) trialOf(ctx context.Context, id int64) (string, error) {
	v, err := s.ghArea().Repos.Get(ctx, s.principal(ctx), id)
	if err != nil {
		return "", err
	}
	return v.FullName, nil
}

// GetRepositoryTrial implements GET /api/v1/repositories/{repositoryId}/trial
// (github-integration R5.66). Read only.
func (s *server) GetRepositoryTrial(ctx context.Context, req gen.GetRepositoryTrialRequestObject) (gen.GetRepositoryTrialResponseObject, error) {
	fail := func(err error) (gen.GetRepositoryTrialResponseObject, error) {
		code, body := failure(err)
		return gen.GetRepositoryTrialdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	if s.opts.Trial == nil || s.opts.GitHub == nil {
		return gen.GetRepositoryTrialdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	repo, err := s.trialOf(ctx, req.RepositoryId)
	if err != nil {
		return fail(err)
	}
	st, err := s.opts.Trial.Get(ctx, repo)
	if err != nil {
		return fail(err)
	}
	rep, err := s.opts.Trial.Report(ctx, repo)
	if err != nil {
		return fail(err)
	}
	return gen.GetRepositoryTrial200JSONResponse(toTrial(st, rep)), nil
}

// SetRepositoryTrial implements PUT /api/v1/repositories/{repositoryId}/trial
// (github-integration R5.66).
func (s *server) SetRepositoryTrial(ctx context.Context, req gen.SetRepositoryTrialRequestObject) (gen.SetRepositoryTrialResponseObject, error) {
	fail := func(err error) (gen.SetRepositoryTrialResponseObject, error) {
		code, body := failure(trialErr(err))
		return gen.SetRepositoryTrialdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	if s.opts.Trial == nil || s.opts.GitHub == nil {
		return gen.SetRepositoryTrialdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	if req.Body == nil {
		return fail(auth.Invalid("the request is empty"))
	}
	mode, err := trial.ParseMode(string(req.Body.Mode))
	if err != nil {
		return fail(auth.Invalid("%s", err.Error()))
	}
	repo, err := s.trialOf(ctx, req.RepositoryId)
	if err != nil {
		return fail(err)
	}
	if req.Body.Confirmed == nil || !*req.Body.Confirmed {
		// Ask first (operations R5.42): nothing changes without a yes.
		cur, err := s.opts.Trial.Get(ctx, repo)
		if err != nil {
			return fail(err)
		}
		return gen.SetRepositoryTrialdefaultJSONResponse{StatusCode: http.StatusConflict, Body: gen.ErrorEnvelope{Error: gen.ErrorBody{
			Code: CodeNeedsConfirmation, Message: trialAsk(repo, cur.Mode, mode)}}}, nil
	}
	o := trial.Options{By: s.principal(ctx).Login}
	b := req.Body
	if b.Percent != nil {
		o.Percent = *b.Percent
	}
	if b.Jobs != nil {
		o.Jobs = *b.Jobs
	}
	if b.ShadowWorkflow != nil {
		o.ShadowWorkflow = *b.ShadowWorkflow
	}
	if b.ShadowJobs != nil {
		o.ShadowJobs = *b.ShadowJobs
	}
	st, err := s.opts.Trial.Set(ctx, repo, mode, o)
	if err != nil {
		return fail(err)
	}
	rep, err := s.opts.Trial.Report(ctx, repo)
	if err != nil {
		return fail(err)
	}
	return gen.SetRepositoryTrial200JSONResponse(toTrial(st, rep)), nil
}

// trialAsk says in plain words what a mode change does, for the question.
func trialAsk(repo string, from, to trial.Mode) string {
	what := map[trial.Mode]string{
		trial.Off:     "stops the trial and puts every earlier variable back",
		trial.Observe: "only watches: it logs what crsp would do for each queued job and starts nothing",
		trial.Shadow:  "copies the marked workflow to crsp while the real jobs stay on GitHub",
		trial.Canary:  "runs the chosen jobs on crsp and writes the canary variables; the other jobs stay on GitHub",
		trial.Live:    "puts every job of this repository on the pool and removes the canary variables",
	}[to]
	return fmt.Sprintf("%s goes from %s to %s, which %s. Send confirmed to do it.", repo, from, to, what)
}
