package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/repos"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/settings"
)

// Handlers of the setting Public repositories (github-integration R5.54 to
// R5.58). The setting itself lives in internal/settings; switching it off
// drains the public repositories through internal/repos.

// CodePublicRepositoriesOff is the error code of a refusal to serve a public
// repository while the setting is off (R5.55). The status is 409.
const CodePublicRepositoriesOff = "public_repositories_off"

func (s *server) toGenPublic(ctx context.Context, v settings.PublicRepositories) (gen.PublicRepositories, error) {
	out := gen.PublicRepositories{Enabled: v.Enabled}
	if !v.ChangedAt.IsZero() {
		at := v.ChangedAt
		out.ChangedAt = &at
	}
	if v.ChangedBy != "" {
		by := v.ChangedBy
		out.ChangedBy = &by
	}
	if a := s.ghArea(); a != nil && a.Repos != nil {
		n, err := a.Repos.PublicOnPool(ctx)
		if err != nil {
			return out, err
		}
		out.PublicOnPool = n
	}
	return out, nil
}

// GetPublicRepositories implements GET /api/v1/settings/public-repositories.
func (s *server) GetPublicRepositories(ctx context.Context, _ gen.GetPublicRepositoriesRequestObject) (gen.GetPublicRepositoriesResponseObject, error) {
	fail := func(f opsFailure) (gen.GetPublicRepositoriesResponseObject, error) {
		return gen.GetPublicRepositoriesdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Settings == nil {
		return fail(unavailable())
	}
	v, err := s.opts.Settings.GetPublicRepositories(ctx)
	if err != nil {
		return fail(s.opsError(err))
	}
	out, err := s.toGenPublic(ctx, v)
	if err != nil {
		return fail(s.opsError(err))
	}
	return gen.GetPublicRepositories200JSONResponse(out), nil
}

// SetPublicRepositories implements PUT /api/v1/settings/public-repositories.
// Switching off stops every public repository whose pool is on; the answer says
// how many. Switching off again repeats that stop, so a run that failed half
// way is finished by asking once more.
func (s *server) SetPublicRepositories(ctx context.Context, req gen.SetPublicRepositoriesRequestObject) (gen.SetPublicRepositoriesResponseObject, error) {
	fail := func(f opsFailure) (gen.SetPublicRepositoriesResponseObject, error) {
		return gen.SetPublicRepositoriesdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Settings == nil {
		return fail(unavailable())
	}
	if req.Body == nil {
		return fail(envelope(http.StatusBadRequest, CodeBadRequest, "the request is not valid"))
	}
	enabled := req.Body.Enabled
	v, _, err := s.opts.Settings.SetPublicRepositories(ctx, publicActor(ctx, s.principal(ctx)), enabled)
	if err != nil {
		return fail(s.opsError(err))
	}
	stopped := 0
	if !enabled {
		if a := s.ghArea(); a != nil && a.Repos != nil {
			p := s.principal(ctx)
			if p.Login == "" {
				p = repos.System
			}
			stopped, err = a.Repos.StopPublicOnPool(ctx, p, "the owner turned public repositories off")
			if err != nil {
				return fail(envelope(http.StatusInternalServerError, CodeInternal, err.Error()))
			}
		}
	}
	base, err := s.toGenPublic(ctx, v)
	if err != nil {
		return fail(s.opsError(err))
	}
	return gen.SetPublicRepositories200JSONResponse{
		Enabled: base.Enabled, ChangedAt: base.ChangedAt, ChangedBy: base.ChangedBy, PublicOnPool: base.PublicOnPool, Stopped: stopped,
	}, nil
}

// publicActor names who changes the setting in the audit log: the signed-in
// principal when there is one, else the actor the request carries.
func publicActor(ctx context.Context, p auth.Principal) audit.Actor {
	if p.Login == "" {
		return audit.ActorFrom(ctx)
	}
	return audit.Actor{Login: p.Login, ID: strconv.FormatInt(p.UserID, 10), Source: p.Source}
}
