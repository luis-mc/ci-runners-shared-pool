package api

import (
	"net/url"
	"strings"
	"testing"
)

// OPS17: with TLS on the LAN listener, sign-in on the plain loopback listener
// works, and its session cookie is not Secure (a Secure cookie would never
// come back over http).
func TestSignInOnLoopbackWithTLSOnOtherListener(t *testing.T) { // R5.8, R5.13
	h := newHarness(t) // ListenAddr 0.0.0.0:8460: the LAN listener is TLS; h.srv is the http loopback one
	c, _ := h.setup(1, "alice")
	res, _ := h.do("GET", "/auth/github/login", nil, nil, nil)
	loc, _ := url.Parse(res.Header.Get("Location"))
	q := loc.Query()
	h.fake.IssueOAuthCode("loop", 1, q.Get("code_challenge"))
	res, _ = h.do("GET", "/auth/github/callback?code=loop&state="+url.QueryEscape(q.Get("state")), nil, nil, nil)
	if res.StatusCode != 302 || strings.Contains(res.Header.Get("Location"), "/sign-in") {
		t.Fatalf("%d %s", res.StatusCode, res.Header.Get("Location"))
	}
	cs := res.Cookies()
	if len(cs) != 1 || cs[0].Secure || !cs[0].HttpOnly {
		t.Fatalf("session cookie on loopback: %+v", cs)
	}
	got := cookies{cs[0].Name: cs[0].Value}
	if r, b := h.do("GET", "/api/v1/me", nil, got, nil); r.StatusCode != 200 {
		t.Fatalf("%d %s", r.StatusCode, b)
	}
	_ = c
}

// Every sign-in failure has its own code, and is logged at WARN with it.
func TestSignInFailuresAreCodedAndLogged(t *testing.T) { // R5.8
	h := newHarness(t)
	h.setup(1, "alice")

	start := func() (state, challenge string) {
		res, _ := h.do("GET", "/auth/github/login", nil, nil, nil)
		loc, _ := url.Parse(res.Header.Get("Location"))
		return loc.Query().Get("state"), loc.Query().Get("code_challenge")
	}
	callback := func(code, state string) string {
		res, _ := h.do("GET", "/auth/github/callback?code="+code+"&state="+url.QueryEscape(state), nil, nil, nil)
		if len(res.Cookies()) != 0 {
			t.Fatal("no session on a failure")
		}
		return res.Header.Get("Location")
	}
	want := func(loc, code string) {
		t.Helper()
		if !strings.HasPrefix(loc, "/sign-in?") || !strings.Contains(loc, "error="+code) {
			t.Fatalf("want %s, got %s", code, loc)
		}
		if logs := h.logs.String(); !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "code="+code) {
			t.Fatalf("code %s not logged at WARN:\n%s", code, logs)
		}
	}

	want(callback("x", "forged"), "state_missing")

	st, _ := start()
	if loc := callback("never-issued", st); !strings.Contains(loc, "error=exchange_failed") {
		t.Fatalf("exchange: %s", loc)
	} else {
		want(loc, "exchange_failed")
	}
	// The state is single use.
	want(callback("x", st), "state_missing")

	h.fake.AddUser(9, "stranger")
	st, ch := start()
	h.fake.IssueOAuthCode("s", 9, ch)
	want(callback("s", st), "not_allowed")

	if logs := h.logs.String(); strings.Contains(logs, "verifier") || strings.Contains(logs, "code=s ") {
		t.Fatalf("log leaks a secret:\n%s", logs)
	}
}
