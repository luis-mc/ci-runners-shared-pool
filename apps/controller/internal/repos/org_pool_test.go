package repos

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// orgWorld adds the organization acme (installation 20) to the personal world:
// alice owns it, bob is a member, carol is another owner. Alice is signed in and has synced; repositories 200 and 201 are
// on, 202 is not.
func (e *env) orgWorld() {
	e.t.Helper()
	e.world()
	e.fake.AddUser(3, "carol")
	e.fake.AddInstall(20, "acme", "Organization", 1, 2)
	e.fake.AddRepo(200, 20, "acme", "api", true, 1)
	e.fake.AddRepo(201, 20, "acme", "web", true, 1)
	e.fake.AddRepo(202, 20, "acme", "docs", true, 1)
	e.fake.AddOrgMember("acme", "alice", "admin")
	e.fake.AddOrgMember("acme", "bob", "member")
	e.fake.AddOrgMember("acme", "carol", "admin")
	if err := e.svc.Sync(context.Background(), adminP); err != nil {
		e.t.Fatal(err)
	}
	e.turnOn(200, 201)
}

var bobAdmin = auth.Principal{UserID: 2, Login: "bob", Role: auth.RoleAdmin, Source: "web"}

func groupRepos(t *testing.T, e *env, name string) []int64 {
	t.Helper()
	g, ok := e.fake.GroupByName("acme", name)
	if !ok {
		t.Fatalf("no runner group %q", name)
	}
	if g.Visibility != "selected" {
		t.Fatalf("group %q has visibility %q, want selected", name, g.Visibility)
	}
	sort.Slice(g.Repos, func(i, j int) bool { return g.Repos[i] < g.Repos[j] })
	return g.Repos
}

func TestOrgOwnerCanTurnPoolOnForOrganization(t *testing.T) { // R5.21
	e := newEnv(t)
	e.orgWorld()
	v, err := e.svc.EnableOrgPool(context.Background(), adminP, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Enabled || v.GroupID == 0 || v.GroupName != "crsp" || !v.IsOrganization || !v.Eligible {
		t.Fatalf("%+v", v)
	}
	if got := groupRepos(t, e, "crsp"); !reflect.DeepEqual(got, []int64{200, 201}) {
		t.Fatalf("group repositories %v", got)
	}
	if !reflect.DeepEqual(v.Repositories, []string{"acme/api", "acme/web"}) {
		t.Fatalf("view repositories %v", v.Repositories)
	}
	if !e.audit.has("org_pool.on") {
		t.Fatal("the change is in the audit log")
	}
	// Registration is at organization level, in that group, for a repository
	// that is on, and stays at repository level for one that is off.
	op, ok, err := OrgRunnerScope(context.Background(), e.store, 200)
	if err != nil || !ok || op.Organization != "acme" || op.GroupName != "crsp" || op.GroupID != v.GroupID {
		t.Fatalf("%+v %v %v", op, ok, err)
	}
	if _, ok, _ := OrgRunnerScope(context.Background(), e.store, 202); ok {
		t.Fatal("a repository whose pool is off registers nothing at organization level")
	}
}

func TestOrgNonOwnerRefused(t *testing.T) { // R5.21
	e := newEnv(t)
	e.orgWorld()
	bobTok := e.fake.AddUser(2, "bob") // a new token for bob, a plain member
	e.tokens.m[2] = github.UserToken{Value: bobTok}
	_, err := e.svc.EnableOrgPool(context.Background(), bobAdmin, 20)
	var noo *ErrNotOrgOwner
	if !errors.As(err, &noo) || !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("want ErrNotOrgOwner, got %v", err)
	}
	if _, ok := e.fake.GroupByName("acme", "crsp"); ok {
		t.Fatal("a refused call creates no group")
	}
	if e.fake.Count("POST", "/orgs/acme/actions/runner-groups") != 0 {
		t.Fatal("a refused call reaches no group endpoint")
	}
	if v, _ := e.svc.OrgPool(context.Background(), adminP, 20); v.Enabled {
		t.Fatal("the mode stays off")
	}
	// A viewer is refused by role before GitHub is asked.
	if _, err := e.svc.EnableOrgPool(context.Background(), viewerP, 20); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("viewer: %v", err)
	}
	// A person who is not in the organization at all is refused too.
	e.tokens.m[4] = github.UserToken{Value: e.fake.AddUser(4, "dave")}
	if _, err := e.svc.EnableOrgPool(context.Background(), auth.Principal{UserID: 4, Login: "dave", Role: auth.RoleAdmin, Source: "web"}, 20); !errors.As(err, &noo) {
		t.Fatalf("outsider: %v", err)
	}
}

func TestOrgOwnerCheckAsAppWithoutGitHubToken(t *testing.T) { // R5.21, as R5.19
	e := newEnv(t)
	e.orgWorld()
	delete(e.tokens.m, 1) // a CLI token has no GitHub user token
	cli := auth.Principal{UserID: 1, Login: "alice", Role: auth.RoleAdmin, Source: "cli"}
	if _, err := e.svc.EnableOrgPool(context.Background(), cli, 20); err != nil {
		t.Fatalf("owner by App read: %v", err)
	}
	delete(e.tokens.m, 2)
	if _, err := e.svc.EnableOrgPool(context.Background(), auth.Principal{UserID: 2, Login: "bob", Role: auth.RoleAdmin, Source: "cli"}, 20); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("member by App read: %v", err)
	}
}

func TestOrgGroupRestrictedToSelectedRepositories(t *testing.T) { // R5.21
	e := newEnv(t)
	e.orgWorld()
	ctx := context.Background()
	// A group of the pool's name already exists, wide open and with a repository
	// that is not selected. It is narrowed, never reused as it was.
	e.fake.AddGroup("acme", "crsp", "all", 202)
	if _, err := e.svc.EnableOrgPool(ctx, adminP, 20); err != nil {
		t.Fatal(err)
	}
	if got := groupRepos(t, e, "crsp"); !reflect.DeepEqual(got, []int64{200, 201}) {
		t.Fatalf("group repositories %v", got)
	}
	if e.fake.Count("POST", "/orgs/acme/actions/runner-groups") != 0 {
		t.Fatal("the existing group was found, not created twice")
	}
	// Turning a repository on adds it; turning one off removes it; nothing else moves.
	e.turnOn(202)
	if got := groupRepos(t, e, "crsp"); !reflect.DeepEqual(got, []int64{200, 201, 202}) {
		t.Fatalf("after pool on: %v", got)
	}
	plan, err := e.svc.PlanChanges(ctx, adminP, []Change{{RepositoryID: 200, Pool: false}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ApplyPlan(ctx, adminP, plan.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got := groupRepos(t, e, "crsp"); !reflect.DeepEqual(got, []int64{201, 202}) {
		t.Fatalf("after pool off: %v", got)
	}
	// The default group is never changed.
	if def, _ := e.fake.GroupByName("acme", "Default"); def.Visibility != "all" || len(def.Repos) != 0 {
		t.Fatalf("default group touched: %+v", def)
	}
}

func TestOrgGroupIsNotTheDefaultGroup(t *testing.T) { // R5.21
	e := newEnv(t)
	e.orgWorld()
	e.svc.cfg.PoolName = "Default"
	_, err := e.svc.EnableOrgPool(context.Background(), adminP, 20)
	var inv *auth.InvalidError
	if !errors.As(err, &inv) {
		t.Fatalf("want a refusal, got %v", err)
	}
	if def, _ := e.fake.GroupByName("acme", "Default"); def.Visibility != "all" {
		t.Fatal("the default group is left as it is")
	}
}

func TestPersonalAccountStaysRepositoryLevel(t *testing.T) { // R5.21
	e := newEnv(t)
	e.orgWorld()
	ctx := context.Background()
	_, err := e.svc.EnableOrgPool(ctx, adminP, 10) // alice's own account
	var np *ErrNotOrganization
	if !errors.As(err, &np) {
		t.Fatalf("want ErrNotOrganization, got %v", err)
	}
	e.turnOn(101)
	if _, ok, err := OrgRunnerScope(ctx, e.store, 101); err != nil || ok {
		t.Fatalf("a personal repository registers at repository level: %v %v", ok, err)
	}
	if e.fake.Count("GET", "/orgs/") != 0 || e.fake.Count("GET", "/user/memberships") != 0 {
		t.Fatal("no organization call for a personal account")
	}
	// Even a stored mode on a personal account does nothing.
	_ = e.store.SetOrgPool(ctx, OrgPool{InstallationID: 10, Organization: "alice", Enabled: true, GroupID: 5, GroupName: "crsp"})
	if _, ok, _ := OrgRunnerScope(ctx, e.store, 101); ok {
		t.Fatal("a personal account has no organization mode")
	}
	if err := e.svc.SyncOrgGroup(ctx, 10); err != nil {
		t.Fatal(err)
	}
}

func TestOrgPoolOffReturnsToRepositoryLevel(t *testing.T) { // R5.21
	e := newEnv(t)
	e.orgWorld()
	ctx := context.Background()
	if _, err := e.svc.EnableOrgPool(ctx, adminP, 20); err != nil {
		t.Fatal(err)
	}
	v, err := e.svc.DisableOrgPool(ctx, adminP, 20)
	if err != nil || v.Enabled {
		t.Fatalf("%+v %v", v, err)
	}
	if _, ok, _ := OrgRunnerScope(ctx, e.store, 200); ok {
		t.Fatal("new runners register at repository level again")
	}
	// A repository turned on later no longer reaches the group.
	e.turnOn(202)
	if got := groupRepos(t, e, "crsp"); !reflect.DeepEqual(got, []int64{200, 201}) {
		t.Fatalf("the group is not widened while the mode is off: %v", got)
	}
	if !e.audit.has("org_pool.off") {
		t.Fatal("the change is in the audit log")
	}
	// Found by login for the removal of a runner that was registered there.
	if op, ok, err := OrgPoolByLogin(ctx, e.store, "ACME"); err != nil || !ok || op.GroupName != "crsp" {
		t.Fatalf("%+v %v %v", op, ok, err)
	}
}

func TestOrgPoolListShowsEligibility(t *testing.T) { // R5.21
	e := newEnv(t)
	e.orgWorld()
	vs, err := e.svc.OrgPools(context.Background(), viewerP)
	if err != nil || len(vs) != 2 {
		t.Fatalf("%v %v", vs, err)
	}
	for _, v := range vs {
		switch v.Organization {
		case "acme":
			if !v.Eligible || !v.IsOrganization {
				t.Fatalf("%+v", v)
			}
		case "alice":
			if v.Eligible || v.IsOrganization || v.Reason == "" {
				t.Fatalf("%+v", v)
			}
		default:
			t.Fatalf("%+v", v)
		}
	}
}
