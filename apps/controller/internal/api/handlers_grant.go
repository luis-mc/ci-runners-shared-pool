package api

import (
	"context"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/repos"
)

// grantBody maps the service's grant view to the API shape.
func grantBody(v repos.GrantView) gen.GithubGrant {
	steps := make([]gen.GithubGrantStep, 0, len(v.Steps))
	for _, st := range v.Steps {
		perms := st.Permissions
		if perms == nil {
			perms = []string{}
		}
		step := gen.GithubGrantStep{Id: gen.GithubGrantStepId(st.ID), Done: st.Done, Permissions: perms}
		if st.URL != "" {
			u := st.URL
			step.Url = &u
		}
		steps = append(steps, step)
	}
	missing := v.Missing
	if missing == nil {
		missing = []string{}
	}
	out := gen.GithubGrant{Ready: v.Ready, Steps: steps, Missing: missing}
	if v.Next != "" {
		n := gen.GithubGrantNext(v.Next)
		out.Next = &n
	}
	if v.InstallationID != 0 {
		id := v.InstallationID
		out.InstallationId = &id
	}
	return out
}

// GetGithubGrant implements GET /api/v1/github/grant (R5.86).
func (s *server) GetGithubGrant(ctx context.Context, req gen.GetGithubGrantRequestObject) (gen.GetGithubGrantResponseObject, error) {
	fail := func(err error) (gen.GetGithubGrantResponseObject, error) {
		code, body := failure(err)
		return gen.GetGithubGrantdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	var needs []string
	list := ""
	if req.Params.Permissions != nil {
		list = *req.Params.Permissions
	}
	for _, n := range strings.Split(list, ",") {
		if n = strings.TrimSpace(n); n != "" {
			needs = append(needs, n)
		}
	}
	if len(needs) == 0 {
		return fail(auth.Invalid("name at least one permission"))
	}
	in := repos.GrantInput{Needs: needs}
	if req.Params.InstallationId != nil {
		in.InstallationID = *req.Params.InstallationId
	}
	if req.Params.RepositoryId != nil {
		in.RepositoryID = *req.Params.RepositoryId
	}
	v, err := s.ghArea().Repos.Grant(ctx, s.principal(ctx), in)
	if err != nil {
		return fail(err)
	}
	return gen.GetGithubGrant200JSONResponse(grantBody(v)), nil
}
