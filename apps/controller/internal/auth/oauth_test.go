package auth

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestStartSignInURL(t *testing.T) { // R5.8
	e := newEnv(t)
	e.withApp()
	start, err := e.svc.StartSignIn(context.Background(), SignInOptions{Origin: devConsole})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(start.RedirectURL)
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("state") == "" ||
		q.Get("redirect_uri") != devConsole+"/auth/github/callback" || q.Get("client_id") == "" {
		t.Fatalf("authorize URL: %v", q)
	}
}

func TestSignInNeedsApp(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.StartSignIn(context.Background(), SignInOptions{Origin: devConsole}); !errors.Is(err, ErrNoApp) {
		t.Fatal(err)
	}
}

func TestRedirectURIPerAddress(t *testing.T) { // R5.8, R6.4
	e := newEnv(t)
	e.consoles = []string{devConsole, "https://pool.lan:8460"}
	e.withApp()
	ctx := context.Background()
	s, err := e.svc.StartSignIn(ctx, SignInOptions{Origin: "https://pool.lan:8460"})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(s.RedirectURL)
	if u.Query().Get("redirect_uri") != "https://pool.lan:8460/auth/github/callback" {
		t.Fatalf("redirect uri is the address the browser used: %v", u.Query())
	}
	for _, bad := range []string{"http://pool.lan:8460", "http://192.168.1.4:8460", "https://other.lan:8460", "https://localhost:8460"} {
		if _, err := e.svc.StartSignIn(ctx, SignInOptions{Origin: bad}); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
}

func TestCallbackOverHTTPOnNonLoopbackRefused(t *testing.T) { // R6.4
	e := newEnv(t)
	e.consoles = []string{devConsole, "https://pool.lan:8460"}
	e.withApp()
	ctx := context.Background()
	s, _ := e.svc.StartSignIn(ctx, SignInOptions{Origin: "https://pool.lan:8460"})
	state := mustQuery(t, s.RedirectURL, "state")
	e.fake.IssueOAuthCode("x", 1, mustQuery(t, s.RedirectURL, "code_challenge"))
	_, err := e.svc.CompleteSignIn(ctx, "x", state, "http://pool.lan:8460")
	if !errors.Is(err, ErrInsecure) {
		t.Fatalf("http callback on a non-loopback host: %v", err)
	}
}

func mustQuery(t *testing.T, raw, key string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get(key)
}

func TestCallbackStateAndPKCE(t *testing.T) { // R5.8
	e := newEnv(t)
	e.withApp()
	e.fake.AddUser(1, "alice")
	ctx := context.Background()
	s, _ := e.svc.StartSignIn(ctx, SignInOptions{Origin: devConsole})
	state := mustQuery(t, s.RedirectURL, "state")

	// A code bound to a different verifier fails at GitHub (PKCE).
	e.fake.IssueOAuthCode("bad", 1, challengeOf("other-verifier"))
	if _, err := e.svc.CompleteSignIn(ctx, "bad", state, devConsole); err == nil {
		t.Fatal("PKCE mismatch must fail")
	}
	// The state is gone after one try.
	e.fake.IssueOAuthCode("good", 1, mustQuery(t, s.RedirectURL, "code_challenge"))
	if _, err := e.svc.CompleteSignIn(ctx, "good", state, devConsole); !errors.Is(err, ErrBadState) {
		t.Fatalf("state is single use: %v", err)
	}
	if _, err := e.svc.CompleteSignIn(ctx, "good", "forged", devConsole); !errors.Is(err, ErrBadState) {
		t.Fatalf("forged state: %v", err)
	}
	if !hasAudit(e.store, "auth.signin_failed") {
		t.Fatal("failed sign-ins are audited (R5.16)")
	}
}

func TestCallbackStateExpires(t *testing.T) { // R5.8
	e := newEnv(t)
	e.withApp()
	s, _ := e.svc.StartSignIn(context.Background(), SignInOptions{Origin: devConsole})
	e.advance(StateTTL + time.Second)
	_, err := e.svc.CompleteSignIn(context.Background(), "c", mustQuery(t, s.RedirectURL, "state"), devConsole)
	if !errors.Is(err, ErrBadState) {
		t.Fatalf("%v", err)
	}
}

func TestCallbackStateBoundToAddress(t *testing.T) {
	e := newEnv(t)
	e.consoles = []string{devConsole, "https://pool.lan:8460"}
	e.withApp()
	s, _ := e.svc.StartSignIn(context.Background(), SignInOptions{Origin: devConsole})
	_, err := e.svc.CompleteSignIn(context.Background(), "c", mustQuery(t, s.RedirectURL, "state"), "https://pool.lan:8460")
	if !errors.Is(err, ErrBadState) {
		t.Fatalf("a state made on one address does not finish on another: %v", err)
	}
}

func TestReturnToKept(t *testing.T) {
	e := newEnv(t)
	si := e.ownerSignIn(1, "alice")
	if si.ReturnTo != "/machines" {
		t.Fatalf("return_to %q", si.ReturnTo)
	}
}

func TestSafeReturnTo(t *testing.T) { // web-console R5.9
	for in, want := range map[string]string{
		"/machines": "/machines", "/a?b=c": "/a?b=c", "": "/", "//evil.example": "/", "https://evil.example": "/",
		"/\\evil.example": "/", "evil": "/", "/ok\r\nSet-Cookie: x=y": "/", "/x\\y": "/",
	} {
		if got := SafeReturnTo(in); got != want {
			t.Errorf("SafeReturnTo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIdentityByIDNotLogin(t *testing.T) { // R5.9
	e := newEnv(t)
	owner := e.ownerSignIn(1, "alice").Principal
	e.addUser(owner, 2, "bob", RoleViewer)
	e.fake.Lock(func() { e.fake.Users[2].Login = "bobby" }) // renamed on GitHub
	si := e.signIn(2, "")
	if si.Principal.UserID != 2 || si.Principal.Login != "bobby" {
		t.Fatalf("%+v", si.Principal)
	}
	u, _ := e.store.GetUser(context.Background(), 2)
	if u.Login != "bobby" || u.Role != RoleViewer || u.LastSignIn.IsZero() {
		t.Fatalf("login is refreshed, role kept: %+v", u)
	}
	// Someone else taking the old login does not inherit the user.
	e.fake.AddUser(3, "bob")
	_, err := e.trySignIn(3, "")
	var na *NotAllowedError
	if !errors.As(err, &na) || na.Login != "bob" {
		t.Fatalf("a new user with the old login is not allowed: %v", err)
	}
}

func TestNotAllowedNamesLogin(t *testing.T) { // R5.11
	e := newEnv(t)
	e.ownerSignIn(1, "alice")
	e.fake.AddUser(9, "stranger")
	_, err := e.trySignIn(9, "")
	var na *NotAllowedError
	if !errors.As(err, &na) || na.Login != "stranger" || !strings.Contains(na.Error(), "ask the owner") {
		t.Fatalf("%v", err)
	}
	if n, _ := e.store.CountUsers(context.Background()); n != 1 {
		t.Fatal("a stranger must not be added")
	}
}

func TestFirstSignInWithSetupCodeIsOwner(t *testing.T) { // R5.10
	e := newEnv(t)
	si := e.ownerSignIn(7, "alice")
	if !si.Owner || si.Principal.Role != RoleOwner {
		t.Fatalf("%+v", si)
	}
	o, err := e.store.GetOwner(context.Background())
	if err != nil || o.GitHubID != 7 || o.AddedBy != "setup" {
		t.Fatalf("%+v %v", o, err)
	}
	if !hasAudit(e.store, "auth.owner_claimed") {
		t.Fatal("audit")
	}
}

func TestSignInWithoutSetupCookieIsNotOwner(t *testing.T) { // R5.10
	e := newEnv(t)
	e.withApp()
	e.fake.AddUser(7, "mallory")
	if _, err := e.trySignIn(7, ""); err == nil {
		t.Fatal("with no owner and no setup code nobody may sign in")
	}
	if _, err := e.trySignIn(7, "forged-cookie"); err == nil {
		t.Fatal("a forged setup cookie must not claim ownership")
	}
	if n, _ := e.store.CountUsers(context.Background()); n != 0 {
		t.Fatal("no user may exist")
	}
}

func TestExactlyOneOwner(t *testing.T) { // R5.10
	e := newEnv(t)
	ctx := context.Background()
	e.withApp()
	code1, _ := e.svc.NewSetupCode(ctx)
	code2, _ := e.svc.NewSetupCode(ctx)
	c1, _ := e.svc.RedeemSetupCode(ctx, code1)
	c2, _ := e.svc.RedeemSetupCode(ctx, code2)
	e.fake.AddUser(1, "alice")
	e.fake.AddUser(2, "bob")
	// Both start with a valid cookie, before either finishes.
	s1, _ := e.svc.StartSignIn(ctx, SignInOptions{Origin: devConsole, SetupCookie: c1})
	s2, _ := e.svc.StartSignIn(ctx, SignInOptions{Origin: devConsole, SetupCookie: c2})
	finish := func(s SignInStart, id int64) error {
		e.fake.IssueOAuthCode("k"+mustQuery(t, s.RedirectURL, "state"), id, mustQuery(t, s.RedirectURL, "code_challenge"))
		_, err := e.svc.CompleteSignIn(ctx, "k"+mustQuery(t, s.RedirectURL, "state"), mustQuery(t, s.RedirectURL, "state"), devConsole)
		return err
	}
	if err := finish(s1, 1); err != nil {
		t.Fatal(err)
	}
	var na *NotAllowedError
	if err := finish(s2, 2); !errors.As(err, &na) {
		t.Fatalf("the second claim loses: %v", err)
	}
	users, _ := e.store.ListUsers(ctx)
	if len(users) != 1 || users[0].Role != RoleOwner {
		t.Fatalf("%+v", users)
	}
}
