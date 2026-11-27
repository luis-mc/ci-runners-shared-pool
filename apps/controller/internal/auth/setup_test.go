package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSetupCodeSingleUse(t *testing.T) { // R5.1
	e := newEnv(t)
	ctx := context.Background()
	needs, err := e.svc.NeedsSetup(ctx)
	if err != nil || !needs {
		t.Fatalf("a fresh controller needs setup: %v %v", needs, err)
	}
	code, err := e.svc.NewSetupCode(ctx)
	if err != nil || code == "" {
		t.Fatal(code, err)
	}
	if _, err := e.svc.RedeemSetupCode(ctx, "WRONG-CODE"); !errors.Is(err, ErrBadCode) {
		t.Fatalf("wrong code: %v", err)
	}
	cookie, err := e.svc.RedeemSetupCode(ctx, code)
	if err != nil || cookie == "" {
		t.Fatal(err)
	}
	if _, err := e.svc.RedeemSetupCode(ctx, code); !errors.Is(err, ErrBadCode) {
		t.Fatalf("second use: %v", err)
	}
	if !e.svc.CheckSetupCookie(ctx, cookie) || e.svc.CheckSetupCookie(ctx, "nope") || e.svc.CheckSetupCookie(ctx, "") {
		t.Fatal("cookie check wrong")
	}
	if !hasAudit(e.store, "setup.code_refused") {
		t.Fatal("a refused code is audited")
	}
}

func TestSetupCodeTypedLoosely(t *testing.T) {
	e := newEnv(t)
	code, _ := e.svc.NewSetupCode(context.Background())
	typed := ""
	for _, r := range code {
		if r != '-' {
			typed += string(r)
		}
	}
	if _, err := e.svc.RedeemSetupCode(context.Background(), " "+lower(typed)+" "); err != nil {
		t.Fatalf("a code typed in lower case without dashes works: %v", err)
	}
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func TestSetupCodeExpires(t *testing.T) {
	e := newEnv(t)
	code, _ := e.svc.NewSetupCode(context.Background())
	e.advance(SetupCodeTTL + time.Second)
	if _, err := e.svc.RedeemSetupCode(context.Background(), code); !errors.Is(err, ErrBadCode) {
		t.Fatalf("%v", err)
	}
}

func TestSetupCodeVoidOnceOwnerExists(t *testing.T) { // R5.1
	e := newEnv(t)
	ctx := context.Background()
	e.withApp()
	spare, _ := e.svc.NewSetupCode(ctx) // printed before the owner signs in
	code, _ := e.svc.NewSetupCode(ctx)
	cookie, _ := e.svc.RedeemSetupCode(ctx, code)
	e.fake.AddUser(1, "alice")
	e.signIn(1, cookie)
	if c, err := e.svc.NewSetupCode(ctx); err != nil || c != "" {
		t.Fatalf("no code is made once an owner exists: %q %v", c, err)
	}
	if _, err := e.svc.RedeemSetupCode(ctx, spare); !errors.Is(err, ErrSetupDone) {
		t.Fatalf("an old code is void: %v", err)
	}
	if e.svc.CheckSetupCookie(ctx, cookie) {
		t.Fatal("the setup cookie is void once an owner exists")
	}
	if needs, _ := e.svc.NeedsSetup(ctx); needs {
		t.Fatal("setup is done")
	}
}

func TestSetupCookieExpires(t *testing.T) {
	e := newEnv(t)
	code, _ := e.svc.NewSetupCode(context.Background())
	cookie, _ := e.svc.RedeemSetupCode(context.Background(), code)
	e.advance(SetupCookieTTL + time.Second)
	if e.svc.CheckSetupCookie(context.Background(), cookie) {
		t.Fatal("expired")
	}
}
