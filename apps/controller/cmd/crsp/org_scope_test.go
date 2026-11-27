package main

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/repos"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

func TestScopeResolverFollowsOrganizationPool(t *testing.T) { // github-integration R5.21
	ctx := context.Background()
	st := repos.NewMemStore()
	_ = st.UpsertInstallation(ctx, repos.Installation{ID: 20, AccountLogin: "acme", AccountType: "Organization"})
	_ = st.UpsertInstallation(ctx, repos.Installation{ID: 10, AccountLogin: "alice", AccountType: "User"})
	_ = st.UpsertRepository(ctx, repos.Repository{ID: 200, InstallationID: 20, FullName: "acme/api", Owner: "acme", Name: "api"})
	_ = st.UpsertRepository(ctx, repos.Repository{ID: 100, InstallationID: 10, FullName: "alice/web", Owner: "alice", Name: "web"})
	_ = st.SetRepositoryPool(ctx, 200, true, "x", time.Now())
	_ = st.SetRepositoryPool(ctx, 100, true, "x", time.Now())
	res := scopeResolver{repos: st}

	// Mode off: repository level.
	sc, on, err := res.OrgScope(ctx, runners.Scope{Kind: "repository", RepositoryID: 200})
	if err != nil || on || sc.Kind != "repository" {
		t.Fatalf("%+v %v %v", sc, on, err)
	}
	_ = st.SetOrgPool(ctx, repos.OrgPool{InstallationID: 20, Enabled: true, GroupID: 9, GroupName: "crsp"})
	sc, on, err = res.OrgScope(ctx, runners.Scope{Kind: "repository", RepositoryID: 200})
	if err != nil || !on || sc.Kind != "organization" || sc.Organization != "acme" || sc.Group != "crsp" || sc.RepositoryID != 200 {
		t.Fatalf("%+v %v %v", sc, on, err)
	}
	tg, err := res.Resolve(ctx, sc)
	if err != nil || tg.Scope.Repo != "" || tg.Scope.Owner != "acme" || tg.Scope.GroupID != 9 || tg.InstallationID != 20 || tg.URL != "https://github.com/acme" {
		t.Fatalf("%+v %v", tg, err)
	}
	// A personal account stays at repository level, whatever is stored.
	_ = st.SetOrgPool(ctx, repos.OrgPool{InstallationID: 10, Enabled: true, GroupID: 9, GroupName: "crsp"})
	if _, on, err := res.OrgScope(ctx, runners.Scope{Kind: "repository", RepositoryID: 100}); err != nil || on {
		t.Fatalf("personal account: %v %v", on, err)
	}
	tg, err = res.Resolve(ctx, runners.Scope{Kind: "repository", RepositoryID: 100})
	if err != nil || tg.Scope.Repo != "web" || tg.Scope.Owner != "alice" {
		t.Fatalf("%+v %v", tg, err)
	}
}
