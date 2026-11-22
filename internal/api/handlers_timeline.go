package api

import (
	"context"
	"net/http"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/gen"
)

func init() { otherAreaAuth["getTimeline"] = authUser }

// GetTimeline implements GET /api/v1/timeline (LOOK58). Stub until the timeline service lands.
func (s *server) GetTimeline(_ context.Context, _ gen.GetTimelineRequestObject) (gen.GetTimelineResponseObject, error) {
	return gen.GetTimelinedefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
}
