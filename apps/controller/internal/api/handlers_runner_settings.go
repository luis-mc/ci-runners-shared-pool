package api

import (
	"context"
	"net/http"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
)

// UpdateRunnerSettings implements PATCH /api/v1/runners/{runnerId}/settings (runners R5.90).
// Only a persistent runner has settings; an ephemeral one is refused with the reason.
func (s *server) UpdateRunnerSettings(ctx context.Context, req gen.UpdateRunnerSettingsRequestObject) (gen.UpdateRunnerSettingsResponseObject, error) {
	if s.opts.Runners == nil {
		return gen.UpdateRunnerSettingsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	if req.Body == nil {
		return gen.UpdateRunnerSettingsdefaultJSONResponse{StatusCode: http.StatusBadRequest,
			Body: gen.ErrorEnvelope{Error: gen.ErrorBody{Code: CodeBadRequest, Message: "the request is not valid"}}}, nil
	}
	v, err := s.opts.Runners.UpdateSettings(ctx, req.RunnerId, req.Body.KeepDependencies, actorFrom(ctx))
	if err != nil {
		code, body := runnerFail(err)
		return gen.UpdateRunnerSettingsdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.UpdateRunnerSettings200JSONResponse(toGenRunner(v)), nil
}

// ReregisterRunner implements POST /api/v1/runners/{runnerId}/reregister (runners R5.153): the
// Register again button of a runner whose GitHub registration is gone.
func (s *server) ReregisterRunner(ctx context.Context, req gen.ReregisterRunnerRequestObject) (gen.ReregisterRunnerResponseObject, error) {
	if s.opts.Runners == nil {
		return gen.ReregisterRunnerdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	v, err := s.opts.Runners.ReregisterRunner(ctx, req.RunnerId, actorFrom(ctx))
	if err != nil {
		code, body := runnerFail(err)
		return gen.ReregisterRunnerdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.ReregisterRunner200JSONResponse(toGenRunner(v)), nil
}

// StartRunner implements POST /api/v1/runners/{runnerId}/start (runners R5.157): the
// Start button of a runner whose service is stopped.
func (s *server) StartRunner(ctx context.Context, req gen.StartRunnerRequestObject) (gen.StartRunnerResponseObject, error) {
	if s.opts.Runners == nil {
		return gen.StartRunnerdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	v, err := s.opts.Runners.StartRunner(ctx, req.RunnerId, actorFrom(ctx))
	if err != nil {
		code, body := runnerFail(err)
		return gen.StartRunnerdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.StartRunner200JSONResponse(toGenRunner(v)), nil
}

// ClearRunnerCache implements POST /api/v1/runners/{runnerId}/clear-cache (runners R5.90).
// The answer is 202: an idle runner clears now, a runner with a job clears when the job ends.
func (s *server) ClearRunnerCache(ctx context.Context, req gen.ClearRunnerCacheRequestObject) (gen.ClearRunnerCacheResponseObject, error) {
	if s.opts.Runners == nil {
		return gen.ClearRunnerCachedefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	v, err := s.opts.Runners.RequestClearCache(ctx, req.RunnerId, actorFrom(ctx))
	if err != nil {
		code, body := runnerFail(err)
		return gen.ClearRunnerCachedefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.ClearRunnerCache202JSONResponse(toGenRunner(v)), nil
}
