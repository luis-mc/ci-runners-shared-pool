package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTrayLinkOnceLoopbackOnly(t *testing.T) { // tray R5.7, R6.1
	e := newEnv(t)
	ctx := context.Background()
	e.ownerSignIn(1, "alice")
	code, err := e.svc.IssueLink(ctx, 1, LinkSignIn, "/machines")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RedeemLink(ctx, code, "192.168.1.9:5555"); !errors.Is(err, ErrLoopbackOnly) {
		t.Fatalf("from the network: %v", err)
	}
	si, err := e.svc.RedeemLink(ctx, code, "127.0.0.1:5555")
	if err != nil || si.ReturnTo != "/machines" || si.Principal.Source != "tray" || si.Principal.UserID != 1 {
		t.Fatalf("%+v %v", si, err)
	}
	if _, err := e.svc.RedeemLink(ctx, code, "127.0.0.1:5555"); !errors.Is(err, ErrBadCode) {
		t.Fatalf("used: %v", err)
	}
	if _, err := e.svc.Authenticate(ctx, si.Cookie); err != nil {
		t.Fatal("the link makes a working session")
	}
	if !hasAudit(e.store, "auth.link_failed") {
		t.Fatal("audit")
	}
	noSecretIn(t, e.store.Dump(), code)
}

func TestTrayLinkExpiresAfter60Seconds(t *testing.T) { // tray R6.1
	e := newEnv(t)
	ctx := context.Background()
	e.ownerSignIn(1, "alice")
	code, _ := e.svc.IssueLink(ctx, 1, LinkSignIn, "/")
	e.advance(LinkTTL + time.Second)
	if _, err := e.svc.RedeemLink(ctx, code, "[::1]:4000"); !errors.Is(err, ErrBadCode) {
		t.Fatal(err)
	}
	if LinkTTL != 60*time.Second {
		t.Fatal("60 seconds")
	}
}

func TestTrayLinkTargetSafe(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.ownerSignIn(1, "alice")
	code, _ := e.svc.IssueLink(ctx, 1, LinkSignIn, "//evil.example")
	si, _ := e.svc.RedeemLink(ctx, code, "127.0.0.1:1")
	if si.ReturnTo != "/" {
		t.Fatalf("%q", si.ReturnTo)
	}
	if _, err := e.svc.IssueLink(ctx, 1, "other", "/"); err == nil {
		t.Fatal("purpose")
	}
	if _, err := e.svc.IssueLink(ctx, 99, LinkSignIn, "/"); err == nil {
		t.Fatal("unknown user")
	}
	if len(code) < 22 {
		t.Fatalf("128 bits need 22 base64 characters, got %d", len(code))
	}
}

func TestTrayBindingByGitHubSignIn(t *testing.T) { // owner decision for M1
	e := newEnv(t)
	ctx := context.Background()
	owner := e.ownerSignIn(1, "alice").Principal
	e.addUser(owner, 2, "bob", RoleViewer)
	if _, err := e.svc.IssueLinkForOSUser(ctx, "m1", "S-1-5-21-1", "/"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no binding yet: %v", err)
	}
	ticket := e.svc.CreateBindTicket("m1", "S-1-5-21-1")
	start, err := e.svc.StartSignIn(ctx, SignInOptions{Origin: devConsole, BindTray: ticket})
	if err != nil {
		t.Fatal(err)
	}
	e.fake.IssueOAuthCode("bindcode", 2, mustQuery(t, start.RedirectURL, "code_challenge"))
	if _, err := e.svc.CompleteSignIn(ctx, "bindcode", mustQuery(t, start.RedirectURL, "state"), devConsole); err != nil {
		t.Fatal(err)
	}
	code, err := e.svc.IssueLinkForOSUser(ctx, "m1", "S-1-5-21-1", "/")
	if err != nil {
		t.Fatal(err)
	}
	si, err := e.svc.RedeemLink(ctx, code, "127.0.0.1:9")
	if err != nil || si.Principal.UserID != 2 {
		t.Fatalf("the link signs in as the bound GitHub user: %+v %v", si, err)
	}
	if _, err := e.svc.IssueLinkForOSUser(ctx, "m1", "other-os-user", "/"); !errors.Is(err, ErrNotFound) {
		t.Fatal("binding is per OS user")
	}
	// A ticket works once.
	start2, _ := e.svc.StartSignIn(ctx, SignInOptions{Origin: devConsole, BindTray: ticket})
	e.fake.IssueOAuthCode("bind2", 1, mustQuery(t, start2.RedirectURL, "code_challenge"))
	_, _ = e.svc.CompleteSignIn(ctx, "bind2", mustQuery(t, start2.RedirectURL, "state"), devConsole)
	b, _ := e.store.GetTrayBinding(ctx, "m1", "S-1-5-21-1")
	if b.UserID != 2 {
		t.Fatalf("a used ticket must not rebind: %+v", b)
	}
}
