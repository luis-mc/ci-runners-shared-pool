package api

import (
	"context"
	"net/http"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/poolname"
)

// PoolNameService is what the pool name routes need (runners R5.141 to R5.145).
// *poolname.Service implements it.
type PoolNameService interface {
	Info(ctx context.Context) poolname.Info
	SetFirst(ctx context.Context, name, actor string) (poolname.Info, error)
	Plan(ctx context.Context, name, actor string) (poolname.Job, error)
	Get(ctx context.Context, id string) (poolname.Job, error)
	Apply(ctx context.Context, id, actor string) (poolname.Job, error)
	Stop(ctx context.Context, id, actor string) (poolname.Job, error)
}

func genPoolRename(j poolname.Job) gen.PoolRename {
	out := gen.PoolRename{Id: j.ID, Old: j.Old, New: j.New, State: gen.PoolRenameState(j.State), Steps: []gen.PoolRenameStep{},
		Counts: gen.PoolRenameCounts{Runners: j.Counts.Runners, Registered: j.Counts.Registered, Ephemeral: j.Counts.Ephemeral,
			Repositories: j.Counts.Repositories, ScaleSet: j.Counts.ScaleSet},
		CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt}
	if j.CreatedBy != "" {
		by := j.CreatedBy
		out.CreatedBy = &by
	}
	for _, st := range j.Steps {
		g := gen.PoolRenameStep{Id: st.ID, Kind: gen.PoolRenameStepKind(st.Kind), Text: st.Text, State: gen.PoolRenameStepState(st.State)}
		if st.Error != "" {
			e := st.Error
			g.Error = &e
		}
		out.Steps = append(out.Steps, g)
	}
	return out
}

func genPoolName(in poolname.Info) gen.PoolName {
	out := gen.PoolName{Name: in.Name, Source: gen.PoolNameSource(in.Source), EnvOverride: in.EnvOverride}
	if in.Pending != nil {
		p := genPoolRename(*in.Pending)
		out.Pending = &p
	}
	return out
}

func (s *server) poolOff() bool { return s.opts.PoolName == nil }

func poolNotImplemented() gen.ErrorEnvelope { return notImplemented() }

// GetPoolName implements GET /api/v1/pool/name.
func (s *server) GetPoolName(ctx context.Context, _ gen.GetPoolNameRequestObject) (gen.GetPoolNameResponseObject, error) {
	if s.poolOff() {
		return gen.GetPoolNamedefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: poolNotImplemented()}, nil
	}
	return gen.GetPoolName200JSONResponse(genPoolName(s.opts.PoolName.Info(ctx))), nil
}

// SetPoolName implements PUT /api/v1/pool/name.
func (s *server) SetPoolName(ctx context.Context, r gen.SetPoolNameRequestObject) (gen.SetPoolNameResponseObject, error) {
	fail := func(err error) (gen.SetPoolNameResponseObject, error) {
		code, body := failure(err)
		return gen.SetPoolNamedefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	if s.poolOff() {
		return gen.SetPoolNamedefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: poolNotImplemented()}, nil
	}
	if r.Body == nil {
		return fail(auth.Invalid("Give the pool a name."))
	}
	in, err := s.opts.PoolName.SetFirst(ctx, r.Body.Name, s.principal(ctx).Login)
	if err != nil {
		return fail(err)
	}
	return gen.SetPoolName200JSONResponse(genPoolName(in)), nil
}

// PlanPoolRename implements POST /api/v1/pool/renames.
func (s *server) PlanPoolRename(ctx context.Context, r gen.PlanPoolRenameRequestObject) (gen.PlanPoolRenameResponseObject, error) {
	fail := func(err error) (gen.PlanPoolRenameResponseObject, error) {
		code, body := failure(err)
		return gen.PlanPoolRenamedefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	if s.poolOff() {
		return gen.PlanPoolRenamedefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: poolNotImplemented()}, nil
	}
	if r.Body == nil {
		return fail(auth.Invalid("Give the pool a name."))
	}
	j, err := s.opts.PoolName.Plan(ctx, r.Body.Name, s.principal(ctx).Login)
	if err != nil {
		return fail(err)
	}
	return gen.PlanPoolRename201JSONResponse(genPoolRename(j)), nil
}

// GetPoolRename implements GET /api/v1/pool/renames/{renameId}.
func (s *server) GetPoolRename(ctx context.Context, r gen.GetPoolRenameRequestObject) (gen.GetPoolRenameResponseObject, error) {
	if s.poolOff() {
		return gen.GetPoolRenamedefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: poolNotImplemented()}, nil
	}
	j, err := s.opts.PoolName.Get(ctx, r.RenameId)
	if err != nil {
		code, body := failure(err)
		return gen.GetPoolRenamedefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.GetPoolRename200JSONResponse(genPoolRename(j)), nil
}

// ApplyPoolRename implements POST /api/v1/pool/renames/{renameId}/apply.
func (s *server) ApplyPoolRename(ctx context.Context, r gen.ApplyPoolRenameRequestObject) (gen.ApplyPoolRenameResponseObject, error) {
	if s.poolOff() {
		return gen.ApplyPoolRenamedefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: poolNotImplemented()}, nil
	}
	j, err := s.opts.PoolName.Apply(ctx, r.RenameId, s.principal(ctx).Login)
	if err != nil {
		code, body := failure(err)
		return gen.ApplyPoolRenamedefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.ApplyPoolRename202JSONResponse(genPoolRename(j)), nil
}

// StopPoolRename implements POST /api/v1/pool/renames/{renameId}/stop.
func (s *server) StopPoolRename(ctx context.Context, r gen.StopPoolRenameRequestObject) (gen.StopPoolRenameResponseObject, error) {
	if s.poolOff() {
		return gen.StopPoolRenamedefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: poolNotImplemented()}, nil
	}
	j, err := s.opts.PoolName.Stop(ctx, r.RenameId, s.principal(ctx).Login)
	if err != nil {
		code, body := failure(err)
		return gen.StopPoolRenamedefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.StopPoolRename202JSONResponse(genPoolRename(j)), nil
}
