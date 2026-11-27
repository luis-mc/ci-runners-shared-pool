package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/mirror"
)

// RegistryMirrorService is what the mirror settings routes need (registry
// R5.63 to R5.67). *mirror.Service implements it.
type RegistryMirrorService interface {
	Get(ctx context.Context) (mirror.View, error)
	Put(ctx context.Context, actor audit.Actor, in registry.MirrorSettings) (mirror.View, error)
}

var _ RegistryMirrorService = (*mirror.Service)(nil)

func toGenMirror(v mirror.View) gen.RegistryMirrorSettings {
	p := v.Path
	return gen.RegistryMirrorSettings{
		Enabled: v.Enabled, Upstreams: v.Upstreams, Folder: v.Folder, BudgetBytes: v.BudgetBytes,
		TagRecheckMinutes: int(v.TagRecheck / time.Minute), UsedBytes: v.UsedBytes, Path: &p,
	}
}

// GetRegistryMirrorSettings implements GET /api/v1/registry/mirror.
func (s *server) GetRegistryMirrorSettings(ctx context.Context, _ gen.GetRegistryMirrorSettingsRequestObject) (gen.GetRegistryMirrorSettingsResponseObject, error) {
	if s.opts.RegistryMirror == nil {
		return gen.GetRegistryMirrorSettingsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	v, err := s.opts.RegistryMirror.Get(ctx)
	if err != nil {
		s.opts.Logger.Error("registry mirror settings", "err", err)
		return gen.GetRegistryMirrorSettingsdefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errEnvelope(CodeInternal, "internal error")}, nil
	}
	return gen.GetRegistryMirrorSettings200JSONResponse(toGenMirror(v)), nil
}

// PutRegistryMirrorSettings implements PUT /api/v1/registry/mirror: turn the
// mirror on or off and set its upstreams, folder and budget (admin).
func (s *server) PutRegistryMirrorSettings(ctx context.Context, req gen.PutRegistryMirrorSettingsRequestObject) (gen.PutRegistryMirrorSettingsResponseObject, error) {
	if s.opts.RegistryMirror == nil {
		return gen.PutRegistryMirrorSettingsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	in := req.Body
	if in == nil {
		return gen.PutRegistryMirrorSettingsdefaultJSONResponse{StatusCode: http.StatusBadRequest, Body: errEnvelope(CodeBadRequest, "the request is not valid")}, nil
	}
	m := registry.MirrorSettings{
		Enabled: in.Enabled, Upstreams: in.Upstreams, BudgetBytes: in.BudgetBytes,
		TagRecheck: time.Duration(in.TagRecheckMinutes) * time.Minute,
	}
	if in.Folder != nil {
		m.Folder = *in.Folder
	}
	v, err := s.opts.RegistryMirror.Put(ctx, audit.ActorFrom(ctx), m)
	if err != nil {
		if errors.Is(err, mirror.ErrInvalid) {
			return gen.PutRegistryMirrorSettingsdefaultJSONResponse{StatusCode: http.StatusBadRequest, Body: errEnvelope(CodeBadRequest, err.Error())}, nil
		}
		s.opts.Logger.Error("registry mirror settings", "err", err)
		return gen.PutRegistryMirrorSettingsdefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errEnvelope(CodeInternal, "internal error")}, nil
	}
	return gen.PutRegistryMirrorSettings200JSONResponse(toGenMirror(v)), nil
}
