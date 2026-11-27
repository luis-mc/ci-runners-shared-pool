package auth

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSessionNeverExpiresByTime(t *testing.T) { // R5.13 (owner decision for M1)
	e := newEnv(t)
	ctx := context.Background()
	si := e.ownerSignIn(1, "alice")
	e.advance(5 * 365 * 24 * time.Hour)
	p, err := e.svc.Authenticate(ctx, si.Cookie)
	if err != nil || p.UserID != 1 || p.Role != RoleOwner {
		t.Fatalf("a session ends at sign-out, not by time: %+v %v", p, err)
	}
}

// D39: no idle expiry either: a session nobody used for years, and one that a
// restarted controller (a new Service on the same store) reads back, still works.
func TestSessionHasNoIdleExpiryAndSurvivesARestart(t *testing.T) { // R5.13, D39
	e := newEnv(t)
	ctx := context.Background()
	si := e.ownerSignIn(1, "alice")
	e.advance(3 * 365 * 24 * time.Hour) // no request in between
	restarted := New(e.store, Config{GitHub: e.gh, Secrets: e.secrets, WebBase: e.fake.URL, Now: e.clock,
		ConsoleURLs: func() []string { return e.consoles }})
	for _, svc := range []*Service{e.svc, restarted} {
		p, err := svc.Authenticate(ctx, si.Cookie)
		if err != nil || p.UserID != 1 {
			t.Fatalf("a session ends at sign-out, removal or a role change only: %+v %v", p, err)
		}
	}
}

func TestSessionEndsAtSignOut(t *testing.T) { // R5.13
	e := newEnv(t)
	ctx := context.Background()
	si := e.ownerSignIn(1, "alice")
	p, _ := e.svc.Authenticate(ctx, si.Cookie)
	if err := e.svc.SignOut(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Authenticate(ctx, si.Cookie); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("%v", err)
	}
	if !hasAudit(e.store, "auth.signout") {
		t.Fatal("audit")
	}
}

func TestSessionStoresOnlyHash(t *testing.T) { // R5.13
	e := newEnv(t)
	si := e.ownerSignIn(1, "alice")
	for _, v := range e.store.Dump() {
		if v == si.Cookie {
			t.Fatal("the session id must not be stored")
		}
	}
	if _, _, err := e.store.GetSession(context.Background(), Hash(si.Cookie)); err != nil {
		t.Fatalf("the session is found by its hash: %v", err)
	}
	if si.Principal.SessionHash != Hash(si.Cookie) {
		t.Fatal("hash")
	}
}

func TestUnknownAndEmptyCookie(t *testing.T) {
	e := newEnv(t)
	for _, c := range []string{"", "nope"} {
		if _, err := e.svc.Authenticate(context.Background(), c); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("%q: %v", c, err)
		}
	}
}

func TestSessionCookieAttributes(t *testing.T) { // web-console R6.1, D38
	c := SessionCookie("v", true)
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" {
		t.Fatalf("session cookie must be HttpOnly, Secure, Lax and host-only: %+v", c)
	}
	if c.MaxAge <= 0 {
		t.Fatal("a browser restart must not sign anyone out")
	}
	if lc := SessionCookie("v", false); lc.Secure || !lc.HttpOnly || lc.SameSite != http.SameSiteLaxMode || lc.Domain != "" {
		t.Fatalf("loopback cookie: %+v", lc)
	}
	if cl := ClearCookie("x", true); cl.MaxAge >= 0 || !cl.Secure || !cl.HttpOnly {
		t.Fatalf("clear: %+v", cl)
	}
}

// D38: Secure on every address except 127.0.0.1 and ::1, whatever the scheme.
func TestCookieSecureByAddress(t *testing.T) {
	mk := func(host string, tlsOn bool) *http.Request {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		r.Host = host
		if tlsOn {
			r.TLS = &tls.ConnectionState{}
		}
		return r
	}
	for _, c := range []struct {
		host string
		tls  bool
		want bool
	}{
		{"127.0.0.1:8460", false, false}, {"[::1]:8460", false, false},
		{"127.0.0.1:8460", true, true},
		{"192.168.1.20:8460", true, true}, {"192.168.1.20:8460", false, true},
		{"ci.example.com", true, true}, {"localhost:8460", false, true}, {"0.0.0.0:8460", false, true},
	} {
		if got := CookieSecure(mk(c.host, c.tls)); got != c.want {
			t.Errorf("%s tls=%v: Secure = %v, want %v", c.host, c.tls, got, c.want)
		}
	}
	if !CookieSecure(nil) {
		t.Error("no request: Secure")
	}
}

func TestCSRF(t *testing.T) {
	e := newEnv(t)
	si := e.ownerSignIn(1, "alice")
	p, _ := e.svc.Authenticate(context.Background(), si.Cookie)
	if p.CSRF == "" || p.CSRF != si.Principal.CSRF {
		t.Fatal("csrf must be stable for a session so GET /me can show it again")
	}
	if !CheckCSRF(p, p.CSRF) || CheckCSRF(p, "") || CheckCSRF(p, "wrong") {
		t.Fatal("csrf check")
	}
	if CheckCSRF(Principal{CSRF: "x"}, "x") {
		t.Fatal("a CLI principal has no session csrf")
	}
}

func TestGitHubTokenInMemoryOnly(t *testing.T) { // R6.2
	e := newEnv(t)
	ctx := context.Background()
	si := e.ownerSignIn(1, "alice")
	tok, err := e.svc.GitHubToken(1)
	if err != nil || tok.Value == "" {
		t.Fatalf("%v", err)
	}
	noSecretIn(t, e.store.Dump(), tok.Value)
	for _, k := range e.secrets.Keys() {
		v, _ := e.secrets.Get(ctx, k)
		if string(v) == tok.Value {
			t.Fatal("the user token must not be stored")
		}
	}
	e.advance(9 * time.Hour) // GitHub user tokens last 8 hours
	if _, err := e.svc.GitHubToken(1); !errors.Is(err, ErrGitHubSessionExpired) {
		t.Fatalf("%v", err)
	}
	// Web session still lives; only GitHub-backed calls ask for a new sign-in.
	if _, err := e.svc.Authenticate(ctx, si.Cookie); err != nil {
		t.Fatal(err)
	}
	e.signIn(1, "")
	p, _ := e.svc.Authenticate(ctx, si.Cookie)
	if _, err := e.svc.GitHubToken(1); err != nil {
		t.Fatal(err)
	}
	_ = e.svc.SignOut(ctx, p)
	if _, err := e.svc.GitHubToken(1); !errors.Is(err, ErrGitHubSessionExpired) {
		t.Fatal("sign-out drops the token")
	}
}

func TestGitHubSignInState(t *testing.T) {
	e := newEnv(t)
	if st := e.svc.GitHubSignInState(1); st.Active {
		t.Fatal("no token yet: ended")
	}
	si := e.ownerSignIn(1, "alice")
	st := e.svc.GitHubSignInState(1)
	if !st.Active || st.Expires.IsZero() {
		t.Fatalf("active with expiry: %+v", st)
	}
	e.advance(9 * time.Hour)
	if st := e.svc.GitHubSignInState(1); st.Active {
		t.Fatal("expired token: ended")
	}
	e.signIn(1, "")
	if !e.svc.GitHubSignInState(1).Active {
		t.Fatal("new sign-in: active")
	}
	p, _ := e.svc.Authenticate(context.Background(), si.Cookie)
	_ = e.svc.SignOut(context.Background(), p)
	if e.svc.GitHubSignInState(1).Active {
		t.Fatal("sign-out: ended")
	}
}
