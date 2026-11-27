package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/envsets"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/regcreds"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

// envFail maps an environment set or registry error to the error envelope.
// The text never holds a value: the errors name variables and hosts only.
func envFail(err error) (int, gen.ErrorEnvelope) {
	e := func(status int, code, msg string) (int, gen.ErrorEnvelope) {
		return status, gen.ErrorEnvelope{Error: gen.ErrorBody{Code: code, Message: msg}}
	}
	switch {
	case errors.Is(err, envsets.ErrInvalid), errors.Is(err, regcreds.ErrInvalid):
		return e(http.StatusBadRequest, CodeBadRequest, err.Error())
	case errors.Is(err, runners.ErrNotFound), errors.Is(err, envsets.ErrNotFound), errors.Is(err, regcreds.ErrNotFound):
		return e(http.StatusNotFound, CodeNotFound, "not found")
	case errors.Is(err, secrets.ErrUnavailable):
		return e(http.StatusServiceUnavailable, CodeUnavailable, "the secret store is not available")
	}
	return e(http.StatusInternalServerError, CodeInternal, "internal error")
}

func genEnvSet(s envsets.Set) gen.EnvSet {
	out := gen.EnvSet{Name: s.Name, Vars: []gen.EnvVar{}}
	for _, v := range s.Vars {
		g := gen.EnvVar{Name: v.Name, Sensitive: v.Sensitive}
		if !v.Sensitive {
			val := v.Value
			g.Value = &val
		}
		out.Vars = append(out.Vars, g)
	}
	return out
}

// GetRunnerEnv implements GET /api/v1/runners/{runnerId}/env.
func (s *server) GetRunnerEnv(ctx context.Context, req gen.GetRunnerEnvRequestObject) (gen.GetRunnerEnvResponseObject, error) {
	if s.opts.EnvSets == nil {
		return gen.GetRunnerEnvdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	vars, err := s.opts.EnvSets.RunnerEnv(ctx, req.RunnerId)
	if err != nil {
		code, body := envFail(err)
		return gen.GetRunnerEnvdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	out := gen.RunnerEnv{RunnerId: req.RunnerId, Vars: []gen.ResolvedEnvVar{}}
	for _, v := range vars {
		out.Vars = append(out.Vars, gen.ResolvedEnvVar{Name: v.Name, Value: v.Value, Sensitive: v.Sensitive, Source: v.Source})
	}
	return gen.GetRunnerEnv200JSONResponse(out), nil
}

// ListEnvSets implements GET /api/v1/env-sets.
func (s *server) ListEnvSets(ctx context.Context, _ gen.ListEnvSetsRequestObject) (gen.ListEnvSetsResponseObject, error) {
	if s.opts.EnvSets == nil {
		return gen.ListEnvSetsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	sets, err := s.opts.EnvSets.List(ctx)
	if err != nil {
		code, body := envFail(err)
		return gen.ListEnvSetsdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	out := gen.EnvSetList{Items: []gen.EnvSet{}}
	for _, st := range sets {
		out.Items = append(out.Items, genEnvSet(st))
	}
	return gen.ListEnvSets200JSONResponse(out), nil
}

// PutEnvSet implements PUT /api/v1/env-sets/{setName}.
func (s *server) PutEnvSet(ctx context.Context, req gen.PutEnvSetRequestObject) (gen.PutEnvSetResponseObject, error) {
	if s.opts.EnvSets == nil {
		return gen.PutEnvSetdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	in := envsets.Set{Name: req.SetName}
	if req.Body != nil {
		for _, v := range req.Body.Vars {
			x := envsets.Var{Name: v.Name}
			if v.Value != nil {
				x.Value = *v.Value
			}
			if v.Sensitive != nil {
				x.Sensitive = *v.Sensitive
			}
			in.Vars = append(in.Vars, x)
		}
	}
	if err := s.opts.EnvSets.Put(ctx, in); err != nil {
		code, body := envFail(err)
		return gen.PutEnvSetdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	saved, err := s.opts.EnvSets.Get(ctx, req.SetName)
	if err != nil {
		code, body := envFail(err)
		return gen.PutEnvSetdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.PutEnvSet200JSONResponse(genEnvSet(saved)), nil
}

// DeleteEnvSet implements DELETE /api/v1/env-sets/{setName}.
func (s *server) DeleteEnvSet(ctx context.Context, req gen.DeleteEnvSetRequestObject) (gen.DeleteEnvSetResponseObject, error) {
	if s.opts.EnvSets == nil {
		return gen.DeleteEnvSetdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	if err := s.opts.EnvSets.Delete(ctx, req.SetName); err != nil {
		code, body := envFail(err)
		return gen.DeleteEnvSetdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.DeleteEnvSet204Response{}, nil
}

func genLogin(l regcreds.RegistryLogin) gen.RegistryLogin {
	return gen.RegistryLogin{Kind: gen.RegistryLoginKind(l.Kind), Host: l.Host, Username: l.Username}
}

func (s *server) registrySettings(ctx context.Context) (gen.RegistrySettings, error) {
	ls, err := s.opts.Registry.Logins(ctx, "")
	if err != nil {
		return gen.RegistrySettings{}, err
	}
	b, err := s.opts.Registry.Bundle(ctx)
	if err != nil {
		return gen.RegistrySettings{}, err
	}
	out := gen.RegistrySettings{Logins: []gen.RegistryLogin{}, CaBundleSet: len(b) > 0}
	for _, l := range ls {
		out.Logins = append(out.Logins, genLogin(l))
	}
	return out, nil
}

// GetRegistrySettings implements GET /api/v1/registry.
func (s *server) GetRegistrySettings(ctx context.Context, _ gen.GetRegistrySettingsRequestObject) (gen.GetRegistrySettingsResponseObject, error) {
	if s.opts.Registry == nil {
		return gen.GetRegistrySettingsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	out, err := s.registrySettings(ctx)
	if err != nil {
		code, body := envFail(err)
		return gen.GetRegistrySettingsdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.GetRegistrySettings200JSONResponse(out), nil
}

// PutRegistryLogin implements PUT /api/v1/registry/logins.
func (s *server) PutRegistryLogin(ctx context.Context, req gen.PutRegistryLoginRequestObject) (gen.PutRegistryLoginResponseObject, error) {
	if s.opts.Registry == nil {
		return gen.PutRegistryLogindefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	if req.Body == nil {
		return gen.PutRegistryLogindefaultJSONResponse{StatusCode: http.StatusBadRequest,
			Body: gen.ErrorEnvelope{Error: gen.ErrorBody{Code: CodeBadRequest, Message: "the request is not valid"}}}, nil
	}
	pw := ""
	if req.Body.Password != nil {
		pw = *req.Body.Password
	}
	l, err := s.opts.Registry.PutLogin(ctx, regcreds.RegistryLogin{Kind: string(req.Body.Kind), Host: req.Body.Host, Username: req.Body.Username}, pw)
	if err != nil {
		code, body := envFail(err)
		return gen.PutRegistryLogindefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.PutRegistryLogin200JSONResponse(genLogin(l)), nil
}

// DeleteRegistryLogin implements DELETE /api/v1/registry/logins.
func (s *server) DeleteRegistryLogin(ctx context.Context, req gen.DeleteRegistryLoginRequestObject) (gen.DeleteRegistryLoginResponseObject, error) {
	if s.opts.Registry == nil {
		return gen.DeleteRegistryLogindefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	if err := s.opts.Registry.DeleteLogin(ctx, string(req.Kind), req.Host); err != nil {
		code, body := envFail(err)
		return gen.DeleteRegistryLogindefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.DeleteRegistryLogin204Response{}, nil
}

// PutRegistryCaBundle implements PUT /api/v1/registry/ca-bundle.
func (s *server) PutRegistryCaBundle(ctx context.Context, req gen.PutRegistryCaBundleRequestObject) (gen.PutRegistryCaBundleResponseObject, error) {
	if s.opts.Registry == nil {
		return gen.PutRegistryCaBundledefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	pem := ""
	if req.Body != nil {
		pem = req.Body.Pem
	}
	if err := s.opts.Registry.PutBundle(ctx, []byte(pem)); err != nil {
		code, body := envFail(err)
		return gen.PutRegistryCaBundledefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	out, err := s.registrySettings(ctx)
	if err != nil {
		code, body := envFail(err)
		return gen.PutRegistryCaBundledefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.PutRegistryCaBundle200JSONResponse(out), nil
}

// DeleteRegistryCaBundle implements DELETE /api/v1/registry/ca-bundle.
func (s *server) DeleteRegistryCaBundle(ctx context.Context, _ gen.DeleteRegistryCaBundleRequestObject) (gen.DeleteRegistryCaBundleResponseObject, error) {
	if s.opts.Registry == nil {
		return gen.DeleteRegistryCaBundledefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	if err := s.opts.Registry.DeleteBundle(ctx); err != nil {
		code, body := envFail(err)
		return gen.DeleteRegistryCaBundledefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.DeleteRegistryCaBundle204Response{}, nil
}
