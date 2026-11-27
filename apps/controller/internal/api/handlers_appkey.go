package api

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
)

// ReplaceAppPrivateKey implements PUT /api/v1/app/private-key (github-integration
// R5.7). The key arrives once, goes to the auth service and is not returned,
// logged or put in an error.
func (s *server) ReplaceAppPrivateKey(ctx context.Context, req gen.ReplaceAppPrivateKeyRequestObject) (gen.ReplaceAppPrivateKeyResponseObject, error) {
	fail := func(err error) (gen.ReplaceAppPrivateKeyResponseObject, error) {
		code, body := failure(err)
		return gen.ReplaceAppPrivateKeydefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	a := s.ghArea()
	if a == nil || a.Auth == nil {
		return gen.ReplaceAppPrivateKeydefaultJSONResponse{StatusCode: 501, Body: notImplemented()}, nil
	}
	if req.Body == nil {
		return fail(auth.Invalid("send the key file"))
	}
	if err := a.Auth.RotateAppKey(ctx, s.principal(ctx), []byte(req.Body.Pem)); err != nil {
		return fail(err)
	}
	return gen.ReplaceAppPrivateKey204Response{}, nil
}
