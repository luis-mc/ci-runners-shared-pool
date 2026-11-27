package github

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
)

func TestOrgMembershipRoles(t *testing.T) { // R5.21
	e := newEnv(t)
	owner := e.fake.AddUser(1, "alice")
	member := e.fake.AddUser(2, "bob")
	outsider := e.fake.AddUser(3, "eve")
	e.fake.AddInstall(20, "acme", "Organization", 1, 2)
	e.fake.AddOrgMember("acme", "alice", "admin")
	e.fake.AddOrgMember("acme", "bob", "member")
	ctx := context.Background()
	for _, c := range []struct {
		tok, role string
	}{{owner, OrgRoleOwner}, {member, OrgRoleMember}} {
		role, active, err := e.client.OrgMembership(ctx, UserToken{Value: c.tok}, "acme")
		if err != nil || !active || role != c.role {
			t.Fatalf("%s: %q %v %v", c.role, role, active, err)
		}
	}
	if _, _, err := e.client.OrgMembership(ctx, UserToken{Value: outsider}, "acme"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a user outside the organization: %v", err)
	}
	// The App's read answers the same.
	role, active, err := e.client.OrgMemberRole(ctx, 20, "acme", "Alice")
	if err != nil || !active || role != OrgRoleOwner {
		t.Fatalf("%q %v %v", role, active, err)
	}
	if _, _, err := e.client.OrgMemberRole(ctx, 20, "acme", "eve"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
}

func TestRunnerGroupsSelectedOnly(t *testing.T) { // R5.21
	e := newEnv(t)
	e.fake.AddUser(1, "alice")
	e.fake.AddInstall(20, "acme", "Organization", 1)
	e.fake.AddRepo(200, 20, "acme", "api", true)
	e.fake.AddRepo(201, 20, "acme", "web", true)
	e.fake.AddOrgMember("acme", "alice", "admin")
	ctx := context.Background()

	gs, err := e.client.ListRunnerGroups(ctx, 20, "acme")
	if err != nil || len(gs) != 1 || !gs[0].Default {
		t.Fatalf("%+v %v", gs, err)
	}
	g, err := e.client.CreateRunnerGroup(ctx, 20, "acme", "crsp", []int64{200})
	if err != nil || g.ID == 0 || g.Visibility != VisibilitySelected {
		t.Fatalf("%+v %v", g, err)
	}
	if fg, _ := e.fake.GroupByName("acme", "crsp"); fg.Visibility != "selected" || !reflect.DeepEqual(fg.Repos, []int64{200}) {
		t.Fatalf("what GitHub holds: %+v", fg)
	}
	if err := e.client.SetRunnerGroupRepositories(ctx, 20, "acme", g.ID, []int64{200, 201}); err != nil {
		t.Fatal(err)
	}
	ids, err := e.client.ListRunnerGroupRepositories(ctx, 20, "acme", g.ID)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if err != nil || !reflect.DeepEqual(ids, []int64{200, 201}) {
		t.Fatalf("%v %v", ids, err)
	}
	// An empty list is sent as an empty list, never left out (which would not narrow).
	if err := e.client.SetRunnerGroupRepositories(ctx, 20, "acme", g.ID, nil); err != nil {
		t.Fatal(err)
	}
	if ids, _ := e.client.ListRunnerGroupRepositories(ctx, 20, "acme", g.ID); len(ids) != 0 {
		t.Fatalf("%v", ids)
	}
	// A wide group is narrowed.
	wide := e.fake.AddGroup("acme", "wide", "all")
	if err := e.client.SelectRunnerGroup(ctx, 20, "acme", wide.ID); err != nil {
		t.Fatal(err)
	}
	if fg, _ := e.fake.GroupByName("acme", "wide"); fg.Visibility != "selected" {
		t.Fatalf("%+v", fg)
	}
	if _, err := e.client.CreateRunnerGroup(ctx, 20, "acme", " ", nil); err == nil {
		t.Fatal("a group needs a name")
	}
}

func TestOrgLevelRegistrationUsesOrgEndpointsAndGroup(t *testing.T) { // R5.21, R5.34
	e := newEnv(t)
	e.fake.AddInstall(20, "acme", "Organization")
	e.fake.AddOrgMember("acme", "alice", "admin")
	g := e.fake.AddGroup("acme", "crsp", "selected", 200)
	ctx := context.Background()
	scope := RunnerScope{Owner: "acme", Organization: "acme", GroupID: g.ID}
	if _, err := e.client.CreateRegistrationToken(ctx, 20, scope); err != nil {
		t.Fatal(err)
	}
	if e.fake.Count("POST", "/orgs/acme/actions/runners/registration-token") != 1 || e.fake.Count("POST", "/repos/") != 0 {
		t.Fatal("organization scope must call the organization endpoint only")
	}
	cfg, err := e.client.GenerateJITConfig(ctx, 20, scope, JITRequest{Name: "crsp-job-1", Labels: []string{"self-hosted"}})
	if err != nil || cfg.Encoded() == "" {
		t.Fatalf("%v", err)
	}
	reqs := e.fake.JITRequests()
	if len(reqs) != 1 || reqs[0].Scope != "acme" || reqs[0].Group != g.ID {
		t.Fatalf("request seen by GitHub: %+v", reqs)
	}
}
