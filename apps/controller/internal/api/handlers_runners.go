package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/repos"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// RunnersService is what the runner operations need (runners R5.1 to R5.7). It
// is declared here so the API depends on an interface, not on the database
// (STACKSPEC 3.2); *runners.Service implements it.
type RunnersService interface {
	ListView(ctx context.Context, f runners.Filter) ([]runners.View, error)
	GetView(ctx context.Context, id string) (runners.View, error)
	PlanAdd(ctx context.Context, req runners.AddRequest, actor string) (runners.Plan, error)
	PlanReplacement(ctx context.Context, runnerID, actor string) (runners.Plan, error)
	GetPlan(ctx context.Context, id string) (runners.Plan, error)
	StopPlan(ctx context.Context, planID, actor string) (runners.Plan, error)
	StartApply(ctx context.Context, planID string, approved []string, actor string) (runners.Plan, error)
	BeginRemove(ctx context.Context, id string, confirm bool, actor string) (runners.Record, func(context.Context) error, error)
	DeleteOfflineRegistration(ctx context.Context, id string, confirm bool, actor string) (runners.Record, error)
	StartRemove(ctx context.Context, finish func(context.Context) error)
	UpdateSettings(ctx context.Context, runnerID string, keepDependencies *bool, actor string) (runners.View, error)
	RequestClearCache(ctx context.Context, runnerID, actor string) (runners.View, error)
	ReregisterRunner(ctx context.Context, runnerID, actor string) (runners.View, error)
	StartRunner(ctx context.Context, runnerID, actor string) (runners.View, error)
	RemoveStrayRegistration(ctx context.Context, in runners.StrayRegistration, prefix string, confirm bool, actor string) error
	RemoveRecordWithoutRegistration(ctx context.Context, id string, confirm bool, actor string) (runners.Record, error)
}

var _ RunnersService = (*runners.Service)(nil)

type actorKey struct{}

// ContextWithActor carries the signed-in user's login to the handlers. The
// auth middleware sets it.
func ContextWithActor(ctx context.Context, login string) context.Context {
	return context.WithValue(ctx, actorKey{}, login)
}

func actorFrom(ctx context.Context) string {
	if v, ok := ctx.Value(actorKey{}).(string); ok && v != "" {
		return v
	}
	return "unknown"
}

// runnerFail maps a service error to the error envelope.
func runnerFail(err error) (int, gen.ErrorEnvelope) {
	var re *runners.RefusalError
	var nc *runners.NeedsConfirmation
	var pub *repos.PublicOffError
	switch {
	case errors.As(err, &pub):
		return http.StatusConflict, gen.ErrorEnvelope{Error: gen.ErrorBody{Code: CodePublicRepositoriesOff, Message: pub.Error()}}
	case errors.As(err, &nc):
		details := make([]gen.ErrorDetail, 0, len(nc.Items))
		for _, it := range nc.Items {
			details = append(details, gen.ErrorDetail{Kind: it.Kind, Text: it.Text})
		}
		return http.StatusConflict, gen.ErrorEnvelope{Error: gen.ErrorBody{
			Code: CodeNeedsConfirmation, Message: "this removal needs a yes; it names what goes", Details: &details}}
	case errors.As(err, &re):
		return http.StatusConflict, gen.ErrorEnvelope{Error: gen.ErrorBody{Code: CodeConflict, Message: re.Reason}}
	case errors.Is(err, runners.ErrNotFound):
		return http.StatusNotFound, gen.ErrorEnvelope{Error: gen.ErrorBody{Code: CodeNotFound, Message: "no such runner or plan"}}
	}
	// The user sees only "internal error"; the log keeps the real one.
	slog.Error("runner request failed", "error", err.Error())
	return http.StatusInternalServerError, gen.ErrorEnvelope{Error: gen.ErrorBody{Code: CodeInternal, Message: "internal error"}}
}

func toGenScope(s runners.Scope) gen.RunnerScope {
	out := gen.RunnerScope{Kind: gen.RunnerScopeKind(s.Kind)}
	if s.RepositoryID != 0 {
		id := s.RepositoryID
		out.RepositoryId = &id
	}
	if s.Organization != "" {
		o := s.Organization
		out.Organization = &o
	}
	if s.Group != "" {
		g := s.Group
		out.RunnerGroup = &g
	}
	return out
}

func fromGenScope(s gen.RunnerScope) runners.Scope {
	out := runners.Scope{Kind: string(s.Kind)}
	if s.RepositoryId != nil {
		out.RepositoryID = *s.RepositoryId
	}
	if s.Organization != nil {
		out.Organization = *s.Organization
	}
	if s.RunnerGroup != nil {
		out.Group = *s.RunnerGroup
	}
	return out
}

// toGenRunner names the isolation of every runner and marks a persistent one as
// keeping state (runners R5.7, UX-61).
func toGenRunner(v runners.View) gen.Runner {
	r := v.Record
	out := gen.Runner{
		Id: r.ID, Name: r.Name, MachineId: r.MachineID, MachineName: v.MachineName, Os: gen.OS(r.OS), Arch: gen.Arch(r.Arch),
		Kind: gen.RunnerKind(r.Kind), Backend: gen.BackendName(r.Backend), Isolation: gen.Isolation(r.Isolation),
		Labels: append([]string{}, r.Labels...), Scope: toGenScope(r.Scope), State: gen.RunnerState(v.State),
		Managed: r.Managed, KeepsState: r.KeepsState(),
	}
	if r.Labels == nil {
		out.Labels = []string{}
	}
	if r.SizeClass != "" {
		c := gen.RunnerSizeClass(r.SizeClass)
		out.SizeClass = &c
	}
	if r.KeepsState() {
		keep, size := r.KeepDependencies, r.DependencyCacheBytes
		out.KeepDependencies, out.DependencyCacheBytes = &keep, &size
	}
	if r.UncleanReason != "" {
		why := r.UncleanReason
		out.UncleanReason = &why
	}
	if v.Reason != "" {
		why := v.Reason
		out.Reason = &why
	}
	if r.Scope.Kind == runners.ScopePool {
		n := v.Registrations
		out.Registrations = &n
		if v.Serving != "" {
			sv := v.Serving
			out.Serving = &sv
		}
	}
	if v.CanReregister {
		yes := true
		out.CanReregister = &yes
	}
	if v.CanStart {
		yes := true
		out.CanStart = &yes
	}
	if r.RunnerVersion != "" {
		ver := r.RunnerVersion
		out.RunnerVersion = &ver
	}
	if r.GitHubRunnerID != 0 {
		id := r.GitHubRunnerID
		out.GithubRunnerId = &id
	}
	if r.State == runners.Failed && r.FailedReason != "" {
		why := r.FailedReason
		out.FailedReason = &why
	}
	if !r.CreatedAt.IsZero() {
		t := r.CreatedAt
		out.CreatedAt = &t
	}
	return out
}

func toGenPlan(p runners.Plan) gen.Plan {
	out := gen.Plan{Id: p.ID, Area: gen.PlanArea(p.Area), State: gen.PlanState(p.State), CreatedAt: p.CreatedAt, CreatedBy: p.CreatedBy,
		Steps: make([]gen.PlanStep, 0, len(p.Steps))}
	for _, s := range p.Steps {
		st := gen.PlanStep{Id: s.ID, Action: gen.PlanStepAction(s.Action), Text: s.Text, NeedsYes: s.NeedsYes, State: gen.PlanStepState(s.State)}
		if s.Target != "" {
			t := s.Target
			st.Target = &t
		}
		if s.WaitsFor != "" {
			w := s.WaitsFor
			st.WaitsFor = &w
		}
		if s.Error != "" {
			e := s.Error
			st.Error = &e
		}
		out.Steps = append(out.Steps, st)
	}
	return out
}

// ListRunners implements GET /api/v1/runners.
func (s *server) ListRunners(ctx context.Context, req gen.ListRunnersRequestObject) (gen.ListRunnersResponseObject, error) {
	if s.opts.Runners == nil {
		return gen.ListRunnersdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	f := runners.Filter{Managed: req.Params.Managed}
	if req.Params.MachineId != nil {
		f.MachineID = *req.Params.MachineId
	}
	if req.Params.Os != nil {
		f.OS = string(*req.Params.Os)
	}
	if req.Params.State != nil {
		f.State = string(*req.Params.State)
	}
	list, err := s.opts.Runners.ListView(ctx, f)
	if err != nil {
		code, body := runnerFail(err)
		return gen.ListRunnersdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	items := make([]gen.Runner, 0, len(list))
	for _, r := range list {
		items = append(items, toGenRunner(r))
	}
	return gen.ListRunners200JSONResponse{Items: items}, nil
}

// GetRunner implements GET /api/v1/runners/{runnerId}.
func (s *server) GetRunner(ctx context.Context, req gen.GetRunnerRequestObject) (gen.GetRunnerResponseObject, error) {
	if s.opts.Runners == nil {
		return gen.GetRunnerdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	r, err := s.opts.Runners.GetView(ctx, req.RunnerId)
	if err != nil {
		code, body := runnerFail(err)
		return gen.GetRunnerdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.GetRunner200JSONResponse(toGenRunner(r)), nil
}

// PlanRunners implements POST /api/v1/runner-plans.
func (s *server) PlanRunners(ctx context.Context, req gen.PlanRunnersRequestObject) (gen.PlanRunnersResponseObject, error) {
	if s.opts.Runners == nil {
		return gen.PlanRunnersdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	b := req.Body
	if b == nil {
		return gen.PlanRunnersdefaultJSONResponse{StatusCode: http.StatusBadRequest,
			Body: gen.ErrorEnvelope{Error: gen.ErrorBody{Code: CodeBadRequest, Message: "the request is not valid"}}}, nil
	}
	ar := runners.AddRequest{OS: string(b.Os), Kind: string(b.Kind), MachineID: b.MachineId, Count: b.Count, Scope: runners.Scope{Kind: runners.ScopePool}}
	if b.Scope != nil && b.Scope.Kind != gen.RunnerScopeKind(runners.ScopePool) { // no scope: pool runners
		ar.Scope = fromGenScope(*b.Scope)
	}
	if b.Labels != nil {
		ar.Labels = *b.Labels
	}
	ar.KeepDependencies = b.KeepDependencies
	p, err := s.opts.Runners.PlanAdd(ctx, ar, actorFrom(ctx))
	if err != nil {
		code, body := runnerFail(err)
		return gen.PlanRunnersdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.PlanRunners201JSONResponse(toGenPlan(p)), nil
}

// GetRunnerPlan implements GET /api/v1/runner-plans/{planId}.
func (s *server) GetRunnerPlan(ctx context.Context, req gen.GetRunnerPlanRequestObject) (gen.GetRunnerPlanResponseObject, error) {
	if s.opts.Runners == nil {
		return gen.GetRunnerPlandefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	p, err := s.opts.Runners.GetPlan(ctx, req.PlanId)
	if err != nil {
		code, body := runnerFail(err)
		return gen.GetRunnerPlandefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.GetRunnerPlan200JSONResponse(toGenPlan(p)), nil
}

// CreateRunners implements POST /api/v1/runner-plans/{planId}/apply. A step
// that needs a yes runs only when named in approved_steps.
func (s *server) CreateRunners(ctx context.Context, req gen.CreateRunnersRequestObject) (gen.CreateRunnersResponseObject, error) {
	if s.opts.Runners == nil {
		return gen.CreateRunnersdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	if req.Body == nil {
		return gen.CreateRunnersdefaultJSONResponse{StatusCode: http.StatusBadRequest,
			Body: gen.ErrorEnvelope{Error: gen.ErrorBody{Code: CodeBadRequest, Message: "the request is not valid"}}}, nil
	}
	p, err := s.opts.Runners.StartApply(ctx, req.PlanId, req.Body.ApprovedSteps, actorFrom(ctx))
	if err != nil {
		code, body := runnerFail(err)
		return gen.CreateRunnersdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.CreateRunners202JSONResponse(toGenPlan(p)), nil
}

// PlanRunnerReplacement implements POST /api/v1/runners/{runnerId}/replace.
func (s *server) PlanRunnerReplacement(ctx context.Context, req gen.PlanRunnerReplacementRequestObject) (gen.PlanRunnerReplacementResponseObject, error) {
	if s.opts.Runners == nil {
		return gen.PlanRunnerReplacementdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	p, err := s.opts.Runners.PlanReplacement(ctx, req.RunnerId, actorFrom(ctx))
	if err != nil {
		code, body := runnerFail(err)
		return gen.PlanRunnerReplacementdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.PlanRunnerReplacement201JSONResponse(toGenPlan(p)), nil
}

// DeleteRunner implements DELETE /api/v1/runners/{runnerId}. Without
// confirm=true it answers 409 needs_confirmation naming what goes and the job it
// waits for (runners R5.4). With it the runner drains, then is removed with a
// remove token in the background.
func (s *server) DeleteRunner(ctx context.Context, req gen.DeleteRunnerRequestObject) (gen.DeleteRunnerResponseObject, error) {
	if s.opts.Runners == nil {
		return gen.DeleteRunnerdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	confirm := req.Params.Confirm != nil && *req.Params.Confirm
	r, finish, err := s.opts.Runners.BeginRemove(ctx, req.RunnerId, confirm, actorFrom(ctx))
	if err != nil {
		code, body := runnerFail(err)
		return gen.DeleteRunnerdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	s.opts.Runners.StartRemove(ctx, finish)
	// The runner is draining now; the machine's name comes with the next list.
	return gen.DeleteRunner202JSONResponse(toGenRunner(runners.View{Record: r, State: r.State, Reason: r.Reason, MachineName: r.MachineID})), nil
}

// DeleteRunnerRegistration implements DELETE /api/v1/runners/{runnerId}/registration
// (github-integration R5.33): the yes path of the offline runner cleanup.
func (s *server) DeleteRunnerRegistration(ctx context.Context, req gen.DeleteRunnerRegistrationRequestObject) (gen.DeleteRunnerRegistrationResponseObject, error) {
	if s.opts.Runners == nil {
		return gen.DeleteRunnerRegistrationdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	confirm := req.Params.Confirm != nil && *req.Params.Confirm
	r, err := s.opts.Runners.DeleteOfflineRegistration(ctx, req.RunnerId, confirm, actorFrom(ctx))
	if err != nil {
		code, body := runnerFail(err)
		return gen.DeleteRunnerRegistrationdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.DeleteRunnerRegistration200JSONResponse(toGenRunner(runners.View{Record: r, State: r.State, Reason: r.Reason})), nil
}

// StopRunnerPlan implements POST /api/v1/runner-plans/{planId}/stop.
func (s *server) StopRunnerPlan(ctx context.Context, req gen.StopRunnerPlanRequestObject) (gen.StopRunnerPlanResponseObject, error) {
	if s.opts.Runners == nil {
		return gen.StopRunnerPlandefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	p, err := s.opts.Runners.StopPlan(ctx, req.PlanId, actorFrom(ctx))
	if err != nil {
		code, body := runnerFail(err)
		return gen.StopRunnerPlandefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	out := toGenPlan(p)
	s.publishPlan(out)
	return gen.StopRunnerPlan202JSONResponse(out), nil
}

// RunnerEventJSON is the `runner` field of a runner.state live event: the
// record as the console reads it. A removed record is sent in state removing,
// and the console reads the list again (OPS19).
func RunnerEventJSON(r runners.Record, gone bool) []byte {
	st := r.State
	if gone {
		st = runners.Removing
	}
	b, err := json.Marshal(toGenRunner(runners.View{Record: r, State: st, Reason: r.Reason, MachineName: r.MachineID}))
	if err != nil {
		return nil
	}
	return b
}

// RemoveRegistration implements POST /api/v1/doctor/registration-fixes (doctor
// R5.50, runners R5.94): the Remove button of a half-registered runner. The
// service reads GitHub and the records again before it removes anything.
func (s *server) RemoveRegistration(ctx context.Context, req gen.RemoveRegistrationRequestObject) (gen.RemoveRegistrationResponseObject, error) {
	if s.opts.Runners == nil {
		return gen.RemoveRegistrationdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	if req.Body == nil {
		return gen.RemoveRegistrationdefaultJSONResponse{StatusCode: http.StatusBadRequest, Body: gen.ErrorEnvelope{Error: gen.ErrorBody{Code: CodeBadRequest, Message: "a body is needed"}}}, nil
	}
	confirm := req.Params.Confirm != nil && *req.Params.Confirm
	a := req.Body.Args
	var name string
	var err error
	switch req.Body.Action {
	case gen.RemoveRegistrationRequestActionRemoveGithubRunner:
		in := runners.StrayRegistration{Name: deref(a.Name)}
		if a.InstallationId != nil {
			in.InstallationID = *a.InstallationId
		}
		if a.GithubRunnerId != nil {
			in.GitHubID = *a.GithubRunnerId
		}
		in.Scope = github.RunnerScope{Owner: deref(a.Owner), Repo: deref(a.Repo), Organization: deref(a.Organization)}
		name = in.Name
		err = s.opts.Runners.RemoveStrayRegistration(ctx, in, doctor.DefaultRunnerPrefix, confirm, actorFrom(ctx))
	case gen.RemoveRegistrationRequestActionRemoveRunnerRecord:
		var r runners.Record
		r, err = s.opts.Runners.RemoveRecordWithoutRegistration(ctx, deref(a.RunnerId), confirm, actorFrom(ctx))
		name = r.Name
	default:
		return gen.RemoveRegistrationdefaultJSONResponse{StatusCode: http.StatusBadRequest, Body: gen.ErrorEnvelope{Error: gen.ErrorBody{Code: CodeBadRequest, Message: "unknown action"}}}, nil
	}
	if err != nil {
		code, body := runnerFail(err)
		return gen.RemoveRegistrationdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.RemoveRegistration200JSONResponse(gen.RemoveRegistrationResult{Removed: true, Name: name}), nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
