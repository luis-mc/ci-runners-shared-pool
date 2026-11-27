package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
)

// DoctorService is what the doctor routes need. internal/doctor.Service
// implements it.
type DoctorService interface {
	// Run starts a run, or joins one of the same scope that is going (R5.8).
	Run(ctx context.Context, scope *gen.DoctorScope, trigger, startedBy string) (gen.DoctorRun, error)
	GetRun(ctx context.Context, id string) (gen.DoctorRun, error)
	Findings(ctx context.Context, severity, group string) (gen.DoctorFindingList, error)
	// IsNotFound says whether err means the run does not exist.
	IsNotFound(err error) bool
}

func errEnvelope(code, msg string) gen.ErrorEnvelope {
	return gen.ErrorEnvelope{Error: gen.ErrorBody{Code: code, Message: msg}}
}

// RunDoctor implements POST /api/v1/doctor/runs (doctor R5.1, R5.8). The auth
// layer has checked the admin role (R6.4).
func (s *server) RunDoctor(ctx context.Context, req gen.RunDoctorRequestObject) (gen.RunDoctorResponseObject, error) {
	if s.opts.Doctor == nil {
		return gen.RunDoctordefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	var scope *gen.DoctorScope
	if req.Body != nil {
		scope = req.Body
	}
	run, err := s.opts.Doctor.Run(ctx, scope, "web", "api")
	if err != nil {
		s.opts.Logger.Error("doctor run", "err", err)
		return gen.RunDoctordefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errEnvelope(CodeInternal, "the doctor could not start")}, nil
	}
	return gen.RunDoctor202JSONResponse(run), nil
}

// GetDoctorRun implements GET /api/v1/doctor/runs/{runId}.
func (s *server) GetDoctorRun(ctx context.Context, req gen.GetDoctorRunRequestObject) (gen.GetDoctorRunResponseObject, error) {
	if s.opts.Doctor == nil {
		return gen.GetDoctorRundefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	run, err := s.opts.Doctor.GetRun(ctx, string(req.RunId))
	switch {
	case err == nil:
		return gen.GetDoctorRun200JSONResponse(run), nil
	case s.opts.Doctor.IsNotFound(err):
		return gen.GetDoctorRundefaultJSONResponse{StatusCode: http.StatusNotFound, Body: errEnvelope(CodeNotFound, "no such doctor run")}, nil
	}
	s.opts.Logger.Error("doctor run read", "err", err)
	return gen.GetDoctorRundefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errEnvelope(CodeInternal, "the doctor run could not be read")}, nil
}

// ListDoctorFindings implements GET /api/v1/doctor/findings.
func (s *server) ListDoctorFindings(ctx context.Context, req gen.ListDoctorFindingsRequestObject) (gen.ListDoctorFindingsResponseObject, error) {
	if s.opts.Doctor == nil {
		return gen.ListDoctorFindingsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	var sev, group string
	if req.Params.Severity != nil {
		sev = string(*req.Params.Severity)
	}
	if req.Params.Group != nil {
		group = *req.Params.Group
	}
	list, err := s.opts.Doctor.Findings(ctx, sev, group)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		s.opts.Logger.Error("doctor findings", "err", err)
		return gen.ListDoctorFindingsdefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errEnvelope(CodeInternal, "the findings could not be read")}, nil
	}
	return gen.ListDoctorFindings200JSONResponse(list), nil
}
