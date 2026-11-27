package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCLIExchange(t *testing.T) { // R5.14
	e := newEnv(t)
	ctx := context.Background()
	owner := e.ownerSignIn(1, "alice").Principal
	e.addUser(owner, 2, "bob", RoleAdmin)
	tok := e.fake.AddUser(2, "bob")
	raw, info, err := e.svc.CLIExchange(ctx, tok, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, CLITokenPrefix) || info.Name != "laptop" || info.UserLogin != "bob" {
		t.Fatalf("%q %+v", raw, info)
	}
	if info.ExpiresAt.Sub(info.CreatedAt) != CLITokenLifetime || CLITokenLifetime != 30*24*time.Hour {
		t.Fatal("30 days (R5.15)")
	}
	p, err := e.svc.AuthenticateCLI(ctx, raw)
	if err != nil || p.UserID != 2 || p.Role != RoleAdmin || p.Source != "cli" || p.TokenID != info.ID {
		t.Fatalf("%+v %v", p, err)
	}
	noSecretIn(t, e.store.Dump(), raw, tok)
	if e.fake.Count("POST", "/applications/") != 1 {
		t.Fatal("GitHub is asked once whether the token is for this App")
	}
}

func TestCLIExchangeRefusals(t *testing.T) { // R5.14
	e := newEnv(t)
	ctx := context.Background()
	e.ownerSignIn(1, "alice")
	if _, _, err := e.svc.CLIExchange(ctx, "", "x"); !errors.Is(err, ErrBadCode) {
		t.Fatal(err)
	}
	if _, _, err := e.svc.CLIExchange(ctx, "ghu_notforthisapp", "x"); err == nil {
		t.Fatal("a token of another App is refused")
	}
	stranger := e.fake.AddUser(9, "stranger")
	_, _, err := e.svc.CLIExchange(ctx, stranger, "x")
	var na *NotAllowedError
	if !errors.As(err, &na) {
		t.Fatalf("a user not on the list: %v", err)
	}
	if !hasAudit(e.store, "auth.cli_failed") {
		t.Fatal("audit")
	}
}

func TestCLITokenExpiresAndRevokes(t *testing.T) { // R5.15
	e := newEnv(t)
	ctx := context.Background()
	owner := e.ownerSignIn(1, "alice").Principal
	e.addUser(owner, 2, "bob", RoleViewer)
	tok := e.fake.AddUser(2, "bob")
	raw, info, _ := e.svc.CLIExchange(ctx, tok, "laptop")
	bob := Principal{UserID: 2, Login: "bob", Role: RoleViewer}

	list, _ := e.svc.ListCLITokens(ctx, bob)
	if len(list) != 1 || list[0].ID != info.ID {
		t.Fatalf("own tokens: %+v", list)
	}
	if all, _ := e.svc.ListCLITokens(ctx, owner); len(all) != 1 {
		t.Fatalf("the owner sees every token: %+v", all)
	}
	carol := e.addUser(owner, 3, "carol", RoleViewer)
	carolP := Principal{UserID: carol.GitHubID, Login: "carol", Role: RoleViewer}
	if list, _ := e.svc.ListCLITokens(ctx, carolP); len(list) != 0 {
		t.Fatal("a user sees only their own tokens")
	}
	if err := e.svc.RevokeCLIToken(ctx, carolP, info.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("someone else's token: %v", err)
	}
	if err := e.svc.RevokeCLIToken(ctx, bob, info.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AuthenticateCLI(ctx, raw); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("revoked")
	}

	raw2, info2, _ := e.svc.CLIExchange(ctx, tok, "second")
	e.advance(CLITokenLifetime + time.Second)
	if _, err := e.svc.AuthenticateCLI(ctx, raw2); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("expired")
	}
	if err := e.svc.RevokeCLIToken(ctx, owner, info2.ID); err != nil {
		t.Fatalf("the owner revokes any token: %v", err)
	}
	if err := e.svc.RevokeCLIToken(ctx, owner, "ct_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestCLITokenCarriesCurrentRole(t *testing.T) { // R5.15
	e := newEnv(t)
	ctx := context.Background()
	owner := e.ownerSignIn(1, "alice").Principal
	e.addUser(owner, 2, "bob", RoleViewer)
	raw, _, _ := e.svc.CLIExchange(ctx, e.fake.AddUser(2, "bob"), "x")
	_, _ = e.svc.UpdateRole(ctx, owner, 2, RoleAdmin, false)
	p, err := e.svc.AuthenticateCLI(ctx, raw)
	if err != nil || p.Role != RoleAdmin {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestUnknownCLIToken(t *testing.T) {
	e := newEnv(t)
	for _, tok := range []string{"", "crsp_cli_nope"} {
		if _, err := e.svc.AuthenticateCLI(context.Background(), tok); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("%q: %v", tok, err)
		}
	}
}
