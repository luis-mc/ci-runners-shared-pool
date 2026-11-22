package api

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/internal/repos"
)

// syncMaxAge is how old a user's repository listing may get before a list
// call refreshes it from GitHub (R6.11).
const syncMaxAge = 5 * time.Minute

// syncState is what a list call knows of its refresh: the age of the data,
// whether it is the last copy, and the error that made it so.
type syncState struct {
	at    time.Time
	stale bool
	cause error
}

func staleness(st syncState) gen.Staleness {
	s := gen.Staleness{Stale: st.stale}
	if !st.at.IsZero() {
		at := st.at
		s.AsOf = &at
	}
	if st.stale && st.cause != nil {
		reason, status, msg := staleCause(st.cause)
		s.Reason = &reason
		if status != 0 {
			s.Status = &status
		}
		if msg != "" {
			s.Message = &msg
		}
	}
	return s
}

// staleCause names why a refresh failed: the sign-in is gone, a permission is
// missing, GitHub could not be reached, or GitHub answered with an error (its
// real status and words are kept, UX-28).
func staleCause(err error) (gen.StalenessReason, int, string) {
	var api *github.APIError
	status, msg := 0, ""
	if errors.As(err, &api) {
		status, msg = api.Status, api.Message
	}
	switch {
	case errors.Is(err, auth.ErrGitHubSessionExpired):
		return gen.SessionExpired, 0, "sign in with GitHub again"
	case errors.Is(err, github.ErrUnavailable) && status == 0:
		return gen.Unreachable, 0, err.Error()
	case errors.Is(err, github.ErrForbidden) && status == http.StatusForbidden:
		return gen.PermissionMissing, status, msg
	}
	if msg == "" {
		msg = err.Error()
	}
	return gen.GithubError, status, msg
}

// refresh syncs the user's listing when it is old or never made. A failure
// is not an error: the page shows the last known state, says how old it is
// and why it could not be refreshed (section 8).
func (a *GitHubArea) refresh(ctx context.Context, p auth.Principal) syncState {
	last := a.Repos.LastSync(p.UserID)
	if !last.IsZero() && time.Since(last) < syncMaxAge {
		return syncState{at: last}
	}
	if err := a.Repos.Sync(ctx, p); err != nil {
		if !errors.Is(err, auth.ErrGitHubSessionExpired) {
			a.log().Warn("repository sync failed", "err", err)
		}
		return syncState{at: last, stale: true, cause: err}
	}
	return syncState{at: a.Repos.LastSync(p.UserID)}
}

func toPolicy(p repos.Policy, visibility string) gen.RepositoryPolicy {
	out := gen.RepositoryPolicy{AllowForkPullRequests: p.AllowForkPRs, Labels: nonNil(p.Labels), RunnerGroups: nonNil(p.RunnerGroups),
		MaxParallelJobs: p.MaxParallelJobs, Priority: p.Priority}
	if p.HostedFallback {
		v := true
		out.HostedFallback = &v
	}
	if p.FallbackCapMinutes > 0 {
		v := p.FallbackCapMinutes
		out.FallbackCapMinutes = &v
	}
	if len(p.HostedLabels) > 0 {
		v := map[string]string{}
		for k, l := range p.HostedLabels {
			v[k] = l
		}
		out.HostedLabels = &v
	}
	kinds := gen.RepositoryPolicyRunnerKinds(p.EffectiveRunnerKinds(visibility))
	out.RunnerKinds = &kinds
	set := p.RunnerKindsSet()
	out.RunnerKindsSet = &set
	if len(p.ExtraPackages) > 0 {
		v := append([]string(nil), p.ExtraPackages...)
		out.ExtraPackages = &v
	}
	if p.BudgetCPUPct > 0 {
		v := p.BudgetCPUPct
		out.BudgetCpuPct = &v
	}
	if p.BudgetMemoryPct > 0 {
		v := p.BudgetMemoryPct
		out.BudgetMemoryPct = &v
	}
	if !p.UpdatedAt.IsZero() {
		v := p.UpdatedAt
		out.UpdatedAt = &v
		if p.UpdatedBy != "" {
			by := p.UpdatedBy
			out.UpdatedBy = &by
		}
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func toRepository(v repos.RepositoryView) gen.Repository {
	out := gen.Repository{Id: v.ID, InstallationId: v.InstallationID, FullName: v.FullName, Name: v.Name, Owner: v.Owner,
		Visibility: gen.RepositoryVisibility(v.Visibility), PoolOn: v.PoolOn, UserIsGithubAdmin: v.UserIsAdmin, UserAdminKnown: v.AdminKnown,
		Policy: toPolicy(v.Policy, v.Visibility), Variables: []gen.RepositoryVariable{}}
	if v.Routing.Where != "" {
		rt := gen.RepositoryRouting{Where: gen.RepositoryRoutingWhere(v.Routing.Where), Why: gen.RepositoryRoutingWhy(v.Routing.Why), UsedMinutes: v.Routing.UsedMinutes}
		if len(v.Routing.OSes) > 0 {
			oses := make([]gen.RepositoryRoutingOses, 0, len(v.Routing.OSes))
			for _, o := range v.Routing.OSes {
				oses = append(oses, gen.RepositoryRoutingOses(o))
			}
			rt.Oses = &oses
		}
		out.Routing = &rt
	}
	if v.Problem != nil {
		pr := gen.RepositoryProblem{Code: gen.RepositoryProblemCode(v.Problem.Code), Text: v.Problem.Text,
			Actions: make([]gen.RepositoryProblemAction, 0, len(v.Problem.Actions))}
		for _, a := range v.Problem.Actions {
			pr.Actions = append(pr.Actions, gen.RepositoryProblemAction{Id: gen.RepositoryProblemActionId(a.ID), Label: a.Label})
		}
		out.Problem = &pr
	}
	out.EphemeralCapacity = v.EphemeralCapacity
	if v.LeftInstallation() {
		why := gen.LeftInstallation
		out.PoolOffReason = &why
	}
	if v.DefaultBranch != "" {
		b := v.DefaultBranch
		out.DefaultBranch = &b
	}
	for _, mv := range v.Variables {
		val, at := mv.Value, mv.WrittenAt
		out.Variables = append(out.Variables, gen.RepositoryVariable{Name: gen.RepositoryVariableName(mv.Name),
			Status: gen.VariableStatus(mv.Status), Value: &val, WrittenAt: &at})
	}
	return out
}

// ListInstallations implements GET /api/v1/github/installations (R5.17, R5.24).
func (s *server) ListInstallations(ctx context.Context, _ gen.ListInstallationsRequestObject) (gen.ListInstallationsResponseObject, error) {
	a, p := s.ghArea(), s.principal(ctx)
	st := a.refresh(ctx, p)
	ins, err := a.Repos.Installations(ctx, p)
	if err != nil {
		code, body := failure(err)
		return gen.ListInstallationsdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	items := make([]gen.Installation, 0, len(ins))
	for _, i := range ins {
		gi := gen.Installation{Id: i.ID, AccountLogin: i.AccountLogin, AccountType: gen.InstallationAccountType(i.AccountType),
			RepositorySelection: gen.InstallationRepositorySelection(i.Selection), Suspended: i.Suspended}
		if len(i.MissingPermissions) > 0 {
			m := i.MissingPermissions
			gi.MissingPermissions = &m
		}
		items = append(items, gi)
	}
	return gen.ListInstallations200JSONResponse{Items: items, Stale: staleness(st)}, nil
}

// GetAppHealth implements GET /api/v1/github/app-health (R5.6).
func (s *server) GetAppHealth(_ context.Context, _ gen.GetAppHealthRequestObject) (gen.GetAppHealthResponseObject, error) {
	var st github.AppState
	if src := s.ghArea().AppHealth; src != nil {
		st = src.State()
	}
	out := gen.AppHealth{Gone: st.Gone}
	if st.Gone {
		why := st.Reason
		out.Reason = &why
	}
	if !st.CheckedAt.IsZero() {
		at := st.CheckedAt
		out.CheckedAt = &at
	}
	return gen.GetAppHealth200JSONResponse(out), nil
}

// GetAppInstallUrl implements GET /api/v1/github/install-url.
func (s *server) GetAppInstallUrl(ctx context.Context, _ gen.GetAppInstallUrlRequestObject) (gen.GetAppInstallUrlResponseObject, error) {
	st, err := s.ghArea().Auth.State(ctx)
	if err != nil || !st.HasApp {
		code, body := failure(auth.ErrNoApp)
		return gen.GetAppInstallUrldefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.GetAppInstallUrl200JSONResponse{Url: st.App.HTMLURL + "/installations/new"}, nil
}

func decodeCursor(c *string) string {
	if c == nil || *c == "" {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(*c)
	if err != nil {
		return ""
	}
	return string(b)
}

// ListRepositories implements GET /api/v1/github/repositories (R5.18, R6.11).
func (s *server) ListRepositories(ctx context.Context, req gen.ListRepositoriesRequestObject) (gen.ListRepositoriesResponseObject, error) {
	a, p := s.ghArea(), s.principal(ctx)
	st := a.refresh(ctx, p)
	f := repos.ListFilter{AfterName: decodeCursor(req.Params.Cursor), Limit: 51}
	limit := 50
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	f.Limit = limit + 1
	if req.Params.InstallationId != nil {
		f.InstallationID = *req.Params.InstallationId
	}
	if req.Params.Pool != nil {
		on := *req.Params.Pool == gen.ListRepositoriesParamsPoolOn
		f.Pool = &on
	}
	if req.Params.Q != nil {
		f.Query = *req.Params.Q
	}
	rows, err := a.Repos.List(ctx, p, f)
	if err != nil {
		code, body := failure(err)
		return gen.ListRepositoriesdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	page := gen.RepositoryPage{Items: []gen.Repository{}, Stale: staleness(st)}
	if len(rows) > limit {
		rows = rows[:limit]
		c := base64.RawURLEncoding.EncodeToString([]byte(rows[len(rows)-1].FullName))
		page.NextCursor = &c
	}
	for _, r := range rows {
		page.Items = append(page.Items, s.withHealth(toRepository(r)))
	}
	return gen.ListRepositories200JSONResponse(page), nil
}

func toPlan(p repos.Plan) gen.Plan {
	out := gen.Plan{Id: p.ID, Area: gen.Repositories, State: gen.PlanState(p.State), CreatedAt: p.CreatedAt,
		CreatedBy: p.CreatedBy, Steps: make([]gen.PlanStep, 0, len(p.Steps))}
	for _, st := range p.Steps {
		ps := gen.PlanStep{Id: st.ID, Action: gen.PlanStepAction(st.Action), NeedsYes: st.NeedsYes,
			State: gen.PlanStepState(st.State), Text: st.Text}
		if st.Target != "" {
			t := st.Target
			ps.Target = &t
		}
		if st.Error != "" {
			e := st.Error
			ps.Error = &e
		}
		out.Steps = append(out.Steps, ps)
	}
	return out
}

// PlanRepositoryChanges implements POST /api/v1/repository-plans (R5.20, R5.29).
func (s *server) PlanRepositoryChanges(ctx context.Context, req gen.PlanRepositoryChangesRequestObject) (gen.PlanRepositoryChangesResponseObject, error) {
	fail := func(err error) (gen.PlanRepositoryChangesResponseObject, error) {
		code, body := failure(err)
		return gen.PlanRepositoryChangesdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	if req.Body == nil {
		return fail(auth.Invalid("the request is empty"))
	}
	var cs []repos.Change
	for _, c := range req.Body.Changes {
		cs = append(cs, repos.Change{RepositoryID: c.RepositoryId, Pool: c.Pool == gen.PlanRepositoryChangesRequestChangesPoolOn})
	}
	plan, err := s.ghArea().Repos.PlanChanges(ctx, s.principal(ctx), cs)
	if err != nil {
		return fail(err)
	}
	return gen.PlanRepositoryChanges201JSONResponse(toPlan(plan)), nil
}

// GetRepositoryPlan implements GET /api/v1/repository-plans/{planId}.
func (s *server) GetRepositoryPlan(ctx context.Context, req gen.GetRepositoryPlanRequestObject) (gen.GetRepositoryPlanResponseObject, error) {
	plan, err := s.ghArea().Repos.GetPlan(ctx, s.principal(ctx), req.PlanId)
	if err != nil {
		code, body := failure(err)
		return gen.GetRepositoryPlandefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.GetRepositoryPlan200JSONResponse(toPlan(plan)), nil
}

// ApplyRepositoryChanges implements POST /api/v1/repository-plans/{planId}/apply.
func (s *server) ApplyRepositoryChanges(ctx context.Context, req gen.ApplyRepositoryChangesRequestObject) (gen.ApplyRepositoryChangesResponseObject, error) {
	fail := func(err error) (gen.ApplyRepositoryChangesResponseObject, error) {
		code, body := failure(err)
		return gen.ApplyRepositoryChangesdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	var approved []string
	if req.Body != nil {
		approved = req.Body.ApprovedSteps
	}
	plan, err := s.ghArea().Repos.ApplyPlan(ctx, s.principal(ctx), req.PlanId, approved)
	if err != nil {
		return fail(err)
	}
	return gen.ApplyRepositoryChanges202JSONResponse(toPlan(plan)), nil
}

// StopRepositoryPlan implements POST /api/v1/repository-plans/{planId}/stop.
func (s *server) StopRepositoryPlan(ctx context.Context, req gen.StopRepositoryPlanRequestObject) (gen.StopRepositoryPlanResponseObject, error) {
	plan, err := s.ghArea().Repos.StopPlan(ctx, s.principal(ctx), req.PlanId)
	if err != nil {
		code, body := failure(err)
		return gen.StopRepositoryPlandefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	out := toPlan(plan)
	s.publishPlan(out)
	return gen.StopRepositoryPlan202JSONResponse(out), nil
}

// GetRepository implements GET /api/v1/repositories/{repositoryId}.
func (s *server) GetRepository(ctx context.Context, req gen.GetRepositoryRequestObject) (gen.GetRepositoryResponseObject, error) {
	v, err := s.ghArea().Repos.Get(ctx, s.principal(ctx), req.RepositoryId)
	if err != nil {
		code, body := failure(err)
		return gen.GetRepositorydefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.GetRepository200JSONResponse(s.withHealth(toRepository(v))), nil
}

// UpdateRepositoryPolicy implements PATCH /api/v1/repositories/{repositoryId}/policy
// (R5.19, R5.25 to R5.27).
func (s *server) UpdateRepositoryPolicy(ctx context.Context, req gen.UpdateRepositoryPolicyRequestObject) (gen.UpdateRepositoryPolicyResponseObject, error) {
	fail := func(err error) (gen.UpdateRepositoryPolicyResponseObject, error) {
		code, body := failure(err)
		return gen.UpdateRepositoryPolicydefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	if req.Body == nil {
		return fail(auth.Invalid("the request is empty"))
	}
	b := req.Body
	u := repos.PolicyUpdate{Priority: b.Priority, MaxParallelJobs: b.MaxParallelJobs, AllowForkPRs: b.AllowForkPullRequests,
		Labels: b.Labels, RunnerGroups: b.RunnerGroups, BudgetMemoryPct: b.BudgetMemoryPct, BudgetCPUPct: b.BudgetCpuPct,
		HostedFallback: b.HostedFallback, FallbackCapMinutes: b.FallbackCapMinutes, HostedLabels: b.HostedLabels}
	if b.RunnerKinds != nil {
		k := string(*b.RunnerKinds)
		u.RunnerKinds = &k
	}
	u.ConfirmForkRisk = b.ConfirmForkRisk != nil && *b.ConfirmForkRisk
	u.ConfirmFallbackCost = b.ConfirmFallbackCost != nil && *b.ConfirmFallbackCost
	pol, err := s.ghArea().Repos.UpdatePolicy(ctx, s.principal(ctx), req.RepositoryId, u)
	if err != nil {
		return fail(err)
	}
	visibility := ""
	if v, err := s.ghArea().Repos.Get(ctx, s.principal(ctx), req.RepositoryId); err == nil {
		visibility = v.Visibility
	}
	return gen.UpdateRepositoryPolicy200JSONResponse(toPolicy(pol, visibility)), nil
}

func toExtraPackages(names []string) gen.ExtraPackages {
	return gen.ExtraPackages{Packages: nonNil(names), Text: strings.Join(names, "\n")}
}

// GetRepositoryExtraPackages implements GET /api/v1/repositories/{repositoryId}/extra-packages
// (runners R5.51).
func (s *server) GetRepositoryExtraPackages(ctx context.Context, req gen.GetRepositoryExtraPackagesRequestObject) (gen.GetRepositoryExtraPackagesResponseObject, error) {
	v, err := s.ghArea().Repos.Get(ctx, s.principal(ctx), req.RepositoryId)
	if err != nil {
		code, body := failure(err)
		return gen.GetRepositoryExtraPackagesdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.GetRepositoryExtraPackages200JSONResponse(toExtraPackages(v.Policy.ExtraPackages)), nil
}

// SetRepositoryExtraPackages implements PUT /api/v1/repositories/{repositoryId}/extra-packages
// (runners R5.51). The list is checked with runners.ParseExtraPackages; a bad
// name answers 422 with its line number and changes nothing.
func (s *server) SetRepositoryExtraPackages(ctx context.Context, req gen.SetRepositoryExtraPackagesRequestObject) (gen.SetRepositoryExtraPackagesResponseObject, error) {
	fail := func(err error) (gen.SetRepositoryExtraPackagesResponseObject, error) {
		code, body := failure(err)
		return gen.SetRepositoryExtraPackagesdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	if req.Body == nil {
		return fail(auth.Invalid("the request is empty"))
	}
	text := req.Body.Text
	pol, err := s.ghArea().Repos.UpdatePolicy(ctx, s.principal(ctx), req.RepositoryId, repos.PolicyUpdate{ExtraPackages: &text})
	if err != nil {
		return fail(err)
	}
	return gen.SetRepositoryExtraPackages200JSONResponse(toExtraPackages(pol.ExtraPackages)), nil
}

// GetRepositoryRunsOn implements GET /api/v1/repositories/{repositoryId}/runs-on (R5.30).
func (s *server) GetRepositoryRunsOn(ctx context.Context, req gen.GetRepositoryRunsOnRequestObject) (gen.GetRepositoryRunsOnResponseObject, error) {
	a := s.ghArea()
	if _, err := a.Repos.Get(ctx, s.principal(ctx), req.RepositoryId); err != nil {
		code, body := failure(err)
		return gen.GetRepositoryRunsOndefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	fs, at, err := a.Repos.FindingsAt(ctx, req.RepositoryId)
	if err != nil {
		code, body := failure(err)
		return gen.GetRepositoryRunsOndefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	out := gen.RunsOnFindingList{Items: make([]gen.RunsOnFinding, 0, len(fs))}
	if !at.IsZero() {
		out.CheckedAt = &at
	}
	for _, f := range fs {
		out.Items = append(out.Items, gen.RunsOnFinding{Path: f.Path, Line: f.Line, Labels: nonNil(f.Labels),
			Kind: gen.RunsOnFindingKind(f.Kind), Text: f.Text})
	}
	return gen.GetRepositoryRunsOn200JSONResponse(out), nil
}

func runsOnPRFailure(err error) (int, gen.Error) {
	if errors.Is(err, repos.ErrRunsOnPRUnavailable) {
		return http.StatusNotImplemented, notImplemented()
	}
	return failure(err)
}

// PreviewRunsOnPullRequests implements GET /api/v1/runs-on-pr (github-integration
// R5.113): the files and the diff, for one repository or every opted-in one.
func (s *server) PreviewRunsOnPullRequests(ctx context.Context, req gen.PreviewRunsOnPullRequestsRequestObject) (gen.PreviewRunsOnPullRequestsResponseObject, error) {
	t := repos.RunsOnPRTarget{All: req.Params.RepositoryId == nil}
	if req.Params.RepositoryId != nil {
		t.RepositoryID = *req.Params.RepositoryId
	}
	pvs, err := s.ghArea().Repos.PreviewRunsOnPR(ctx, s.principal(ctx), t)
	if err != nil {
		code, body := runsOnPRFailure(err)
		return gen.PreviewRunsOnPullRequestsdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	out := gen.RunsOnPullRequestPreviewList{Items: make([]gen.RunsOnPullRequestPreview, 0, len(pvs))}
	for _, v := range pvs {
		files := make([]gen.RunsOnPullRequestFile, 0, len(v.Files))
		for _, f := range v.Files {
			files = append(files, gen.RunsOnPullRequestFile{Path: f.Path, Changes: f.Changes, Diff: f.Diff})
		}
		out.Items = append(out.Items, gen.RunsOnPullRequestPreview{RepositoryId: v.RepositoryID, FullName: v.FullName,
			State: gen.RunsOnPullRequestPreviewState(v.State), Files: files, Diff: v.Diff, Skipped: nonNil(v.Skipped), Message: optStr(v.Message)})
	}
	return gen.PreviewRunsOnPullRequests200JSONResponse(out), nil
}

// OpenRunsOnPullRequests implements POST /api/v1/runs-on-pr (github-integration
// R5.113): only this call opens the pull request; nothing does it on a schedule.
func (s *server) OpenRunsOnPullRequests(ctx context.Context, req gen.OpenRunsOnPullRequestsRequestObject) (gen.OpenRunsOnPullRequestsResponseObject, error) {
	if req.Body == nil {
		code, body := failure(auth.Invalid("the request is empty"))
		return gen.OpenRunsOnPullRequestsdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	t := repos.RunsOnPRTarget{All: req.Body.All != nil && *req.Body.All}
	if req.Body.RepositoryId != nil {
		t.RepositoryID = *req.Body.RepositoryId
	}
	rs, err := s.ghArea().Repos.OpenRunsOnPR(ctx, s.principal(ctx), t)
	if err != nil {
		code, body := runsOnPRFailure(err)
		return gen.OpenRunsOnPullRequestsdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	out := gen.RunsOnPullRequestResultList{Items: make([]gen.RunsOnPullRequestResult, 0, len(rs))}
	for _, r := range rs {
		it := gen.RunsOnPullRequestResult{RepositoryId: r.RepositoryID, FullName: r.FullName,
			State: gen.RunsOnPullRequestResultState(r.State), Message: optStr(r.Message)}
		if r.Number != 0 {
			n, u := r.Number, r.URL
			it.Number, it.Url = &n, &u
		}
		out.Items = append(out.Items, it)
	}
	return gen.OpenRunsOnPullRequests200JSONResponse(out), nil
}

var _ = http.StatusOK
