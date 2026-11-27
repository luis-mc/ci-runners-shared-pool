// Code below is the CRSP17 contract stub set: one method per operation of
// packages/contract/openapi.yaml that is not built yet. Each answers 501 with
// the error envelope, so the routes exist and the route-coverage tests pass.
// A feature branch replaces a stub with its handler and deletes it here.

package api

import (
	"context"
	"net/http"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
)

// notImplemented is the envelope every stub answers with.
func notImplemented() gen.ErrorEnvelope {
	return gen.ErrorEnvelope{Error: gen.ErrorBody{Code: CodeNotImplemented, Message: "this operation is not built yet"}}
}

// CreateConsoleLink is not built yet.
func (s *server) CreateConsoleLink(_ context.Context, _ gen.CreateConsoleLinkRequestObject) (gen.CreateConsoleLinkResponseObject, error) {
	return gen.CreateConsoleLinkdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
}
