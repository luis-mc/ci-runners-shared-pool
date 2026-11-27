package auth

import (
	"context"
	"errors"
	"testing"
)

func TestRoles(t *testing.T) { // R5.12
	for _, c := range []struct {
		have, need Role
		ok         bool
	}{
		{RoleViewer, RoleViewer, true}, {RoleViewer, RoleAdmin, false}, {RoleViewer, RoleOwner, false},
		{RoleAdmin, RoleViewer, true}, {RoleAdmin, RoleAdmin, true}, {RoleAdmin, RoleOwner, false},
		{RoleOwner, RoleViewer, true}, {RoleOwner, RoleAdmin, true}, {RoleOwner, RoleOwner, true},
		{Role(""), RoleViewer, false}, {Role("root"), RoleViewer, false},
	} {
		err := Require(Principal{UserID: 1, Role: c.have}, c.need)
		if (err == nil) != c.ok {
			t.Errorf("%s needs %s: err=%v", c.have, c.need, err)
		}
	}
	if err := Require(Principal{}, RoleViewer); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("no user: %v", err)
	}
	if err := Require(Principal{UserID: 1, Role: RoleViewer}, RoleAdmin); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
}

func TestOnlyOwnerManagesUsers(t *testing.T) { // R5.12
	e := newEnv(t)
	ctx := context.Background()
	owner := e.ownerSignIn(1, "alice").Principal
	admin := e.addUser(owner, 2, "bob", RoleAdmin)
	adminP := Principal{UserID: admin.GitHubID, Login: "bob", Role: RoleAdmin}
	e.fake.AddUser(3, "carol")
	if _, err := e.svc.AddUser(ctx, adminP, "carol", RoleViewer); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin adds a user: %v", err)
	}
	if _, err := e.svc.ListUsers(ctx, adminP); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if err := e.svc.RemoveUser(ctx, adminP, 2); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdateRole(ctx, adminP, 2, RoleViewer, false); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
}

func TestAddUserResolvesLogin(t *testing.T) { // R5.11
	e := newEnv(t)
	ctx := context.Background()
	owner := e.ownerSignIn(1, "alice").Principal
	e.fake.AddUser(5, "Octocat")
	u, err := e.svc.AddUser(ctx, owner, "@octocat", RoleViewer)
	if err != nil || u.GitHubID != 5 || u.Login != "Octocat" || u.AddedBy != "alice" {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err := e.svc.AddUser(ctx, owner, "octocat", RoleAdmin); !errors.Is(err, ErrConflict) {
		t.Fatalf("again: %v", err)
	}
	if _, err := e.svc.AddUser(ctx, owner, "nobody-here", RoleViewer); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown login: %v", err)
	}
	if _, err := e.svc.AddUser(ctx, owner, "octocat", RoleOwner); err == nil {
		t.Fatal("a second owner cannot be added")
	}
	if _, err := e.svc.AddUser(ctx, owner, " ", RoleViewer); err == nil {
		t.Fatal("empty login")
	}
	if !hasAudit(e.store, "user.added") {
		t.Fatal("audit")
	}
}

func TestAddedUserSignsInWithRole(t *testing.T) { // R5.11
	e := newEnv(t)
	owner := e.ownerSignIn(1, "alice").Principal
	e.addUser(owner, 2, "bob", RoleViewer)
	si := e.signIn(2, "")
	if si.Principal.Role != RoleViewer || si.Owner {
		t.Fatalf("%+v", si)
	}
}

func TestRoleChangeEndsSessions(t *testing.T) { // R5.13
	e := newEnv(t)
	ctx := context.Background()
	owner := e.ownerSignIn(1, "alice").Principal
	e.addUser(owner, 2, "bob", RoleViewer)
	si := e.signIn(2, "")
	if _, err := e.svc.Authenticate(ctx, si.Cookie); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdateRole(ctx, owner, 2, RoleAdmin, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Authenticate(ctx, si.Cookie); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("a role change ends the user's sessions: %v", err)
	}
	if !hasAudit(e.store, "user.role_changed") {
		t.Fatal("audit")
	}
}

func TestRemoveUserEndsSessionsAndTokens(t *testing.T) { // R5.13, R5.15
	e := newEnv(t)
	ctx := context.Background()
	owner := e.ownerSignIn(1, "alice").Principal
	e.addUser(owner, 2, "bob", RoleAdmin)
	si := e.signIn(2, "")
	tok := e.fake.AddUser(2, "bob")
	cli, _, err := e.svc.CLIExchange(ctx, tok, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RemoveUser(ctx, owner, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Authenticate(ctx, si.Cookie); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("session must end")
	}
	if _, err := e.svc.AuthenticateCLI(ctx, cli); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("cli token must end")
	}
	if _, err := e.trySignIn(2, ""); err == nil {
		t.Fatal("a removed user cannot sign in")
	}
	if !hasAudit(e.store, "user.removed") {
		t.Fatal("audit")
	}
}

func TestOwnerCannotBeRemovedOrDemoted(t *testing.T) { // R5.10
	e := newEnv(t)
	ctx := context.Background()
	owner := e.ownerSignIn(1, "alice").Principal
	if err := e.svc.RemoveUser(ctx, owner, 1); err == nil {
		t.Fatal("owner cannot remove themselves")
	}
	if _, err := e.svc.UpdateRole(ctx, owner, 1, RoleAdmin, true); err == nil {
		t.Fatal("owner cannot demote themselves")
	}
	if o, _ := e.store.GetOwner(ctx); o.GitHubID != 1 {
		t.Fatal("still owner")
	}
}

func TestOwnershipHandover(t *testing.T) { // R5.10
	e := newEnv(t)
	ctx := context.Background()
	owner := e.ownerSignIn(1, "alice").Principal
	e.addUser(owner, 2, "bob", RoleViewer)
	e.addUser(owner, 3, "carol", RoleAdmin)
	if _, err := e.svc.UpdateRole(ctx, owner, 2, RoleOwner, true); err == nil {
		t.Fatal("ownership goes to an admin only")
	}
	if _, err := e.svc.UpdateRole(ctx, owner, 3, RoleOwner, false); !errors.Is(err, ErrNeedsConfirm) {
		t.Fatalf("the owner asks first: %v", err)
	}
	if o, _ := e.store.GetOwner(ctx); o.GitHubID != 1 {
		t.Fatal("nothing changed without a yes")
	}
	u, err := e.svc.UpdateRole(ctx, owner, 3, RoleOwner, true)
	if err != nil || u.Role != RoleOwner {
		t.Fatalf("%+v %v", u, err)
	}
	old, _ := e.store.GetUser(ctx, 1)
	if old.Role != RoleAdmin {
		t.Fatalf("old owner becomes admin: %v", old.Role)
	}
	users, _ := e.store.ListUsers(ctx)
	owners := 0
	for _, x := range users {
		if x.Role == RoleOwner {
			owners++
		}
	}
	if owners != 1 {
		t.Fatalf("%d owners", owners)
	}
	if !hasAudit(e.store, "user.ownership_handed") {
		t.Fatal("audit")
	}
}
