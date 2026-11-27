package api

import (
	"context"
	"net/http"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
)

// BuildsService reads the image build records (registry R5.69, R5.70, IMG1).
// *builds.Recorder implements it.
type BuildsService interface {
	// List gives the builds, the running ones first; machineID narrows to one machine.
	List(ctx context.Context, machineID string, now time.Time) ([]gen.ImageBuild, error)
	// Get gives one build with its log tail; false when there is none.
	Get(ctx context.Context, id string, now time.Time) (gen.ImageBuild, bool, error)
}

func (s *server) buildsNow() time.Time {
	if s.opts.Now != nil {
		return s.opts.Now()
	}
	return time.Now()
}

// ListImageBuilds implements GET /api/v1/registry/builds.
func (s *server) ListImageBuilds(ctx context.Context, req gen.ListImageBuildsRequestObject) (gen.ListImageBuildsResponseObject, error) {
	if s.opts.Builds == nil {
		return gen.ListImageBuildsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	machine := ""
	if req.Params.MachineId != nil {
		machine = *req.Params.MachineId
	}
	items, err := s.opts.Builds.List(ctx, machine, s.buildsNow())
	if err != nil {
		return nil, err
	}
	return gen.ListImageBuilds200JSONResponse(gen.ImageBuildList{Items: items}), nil
}

// GetImageBuild implements GET /api/v1/registry/builds/{buildId}.
func (s *server) GetImageBuild(ctx context.Context, req gen.GetImageBuildRequestObject) (gen.GetImageBuildResponseObject, error) {
	if s.opts.Builds == nil {
		return gen.GetImageBuilddefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	b, ok, err := s.opts.Builds.Get(ctx, req.BuildId, s.buildsNow())
	if err != nil {
		return nil, err
	}
	if !ok {
		return gen.GetImageBuilddefaultJSONResponse{StatusCode: http.StatusNotFound, Body: errEnvelope(CodeNotFound, "no such build")}, nil
	}
	return gen.GetImageBuild200JSONResponse(b), nil
}
