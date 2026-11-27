package api

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/repos"
)

func toOrgPool(v repos.OrgPoolView) gen.OrgPool {
	out := gen.OrgPool{InstallationId: v.InstallationID, Organization: v.Organization, IsOrganization: v.IsOrganization,
		Eligible: v.Eligible, Enabled: v.Enabled, GroupName: v.GroupName, Repositories: nonNil(v.Repositories)}
	if v.Reason != "" {
		r := v.Reason
		out.Reason = &r
	}
	if v.GroupID != 0 {
		g := v.GroupID
		out.GroupId = &g
	}
	if !v.ChangedAt.IsZero() {
		at := v.ChangedAt
		out.ChangedAt = &at
	}
	if v.ChangedBy != "" {
		by := v.ChangedBy
		out.ChangedBy = &by
	}
	return out
}

// ListOrgPools implements GET /api/v1/github/org-pools (R5.21).
func (s *server) ListOrgPools(ctx context.Context, _ gen.ListOrgPoolsRequestObject) (gen.ListOrgPoolsResponseObject, error) {
	vs, err := s.ghArea().Repos.OrgPools(ctx, s.principal(ctx))
	if err != nil {
		code, body := failure(err)
		return gen.ListOrgPoolsdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	items := make([]gen.OrgPool, 0, len(vs))
	for _, v := range vs {
		items = append(items, toOrgPool(v))
	}
	return gen.ListOrgPools200JSONResponse{Items: items}, nil
}

// SetOrgPool implements PUT /api/v1/github/installations/{installationId}/org-pool (R5.21).
func (s *server) SetOrgPool(ctx context.Context, req gen.SetOrgPoolRequestObject) (gen.SetOrgPoolResponseObject, error) {
	fail := func(err error) (gen.SetOrgPoolResponseObject, error) {
		code, body := failure(err)
		return gen.SetOrgPooldefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	if req.Body == nil {
		return fail(auth.Invalid("the request is empty"))
	}
	var (
		v   repos.OrgPoolView
		err error
	)
	if req.Body.Enabled {
		v, err = s.ghArea().Repos.EnableOrgPool(ctx, s.principal(ctx), req.InstallationId)
	} else {
		v, err = s.ghArea().Repos.DisableOrgPool(ctx, s.principal(ctx), req.InstallationId)
	}
	if err != nil {
		return fail(err)
	}
	return gen.SetOrgPool200JSONResponse(toOrgPool(v)), nil
}
