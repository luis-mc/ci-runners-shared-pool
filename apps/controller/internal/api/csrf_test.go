package api

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
)

const (
	csrfRefusal   = "the CSRF token is missing or wrong"
	originRefusal = "the request does not come from this console"
)

func isUnsafe(method string) bool { return method != "GET" && method != "HEAD" && method != "OPTIONS" }

// R6.2: every operation that changes something and needs a session refuses a
// browser request without the session's CSRF token, with a wrong one, and with
// a missing or foreign Origin; with both right it gets past the check.
func TestEveryChangeNeedsCSRFTokenAndOwnOrigin(t *testing.T) {
	h := newHarness(t)
	h.setup(1, "alice")
	n := 0
	for _, op := range loadContractOps(t) {
		if !isUnsafe(op.Method) || op.Auth == authPublic {
			continue
		}
		n++
		// A fresh session per operation: signOut and removeUser end sessions.
		owner, csrf := h.signIn(1, cookies{})
		path := pathParam.ReplaceAllString(op.Path, "1")
		var body any
		if op.HasBody {
			body = map[string]any{}
		}
		call := func(headers map[string]string, origin string) (int, string) {
			t.Helper()
			oh := map[string]string{}
			for k, v := range headers {
				oh[k] = v
			}
			// harness.do adds the right Origin; override it, or blank it.
			res, b := h.doRaw(op.Method, path, body, owner, oh, origin)
			return res.StatusCode, string(b)
		}
		cases := []struct {
			name    string
			headers map[string]string
			origin  string
			want    string
		}{
			{"no token", nil, h.srv.URL, csrfRefusal},
			{"wrong token", map[string]string{"X-CSRF-Token": "wrong"}, h.srv.URL, csrfRefusal},
			{"no origin", map[string]string{"X-CSRF-Token": csrf}, "", originRefusal},
			{"foreign origin", map[string]string{"X-CSRF-Token": csrf}, "https://evil.example", originRefusal},
			{"null origin", map[string]string{"X-CSRF-Token": csrf}, "null", originRefusal},
		}
		for _, c := range cases {
			status, b := call(c.headers, c.origin)
			if status != 403 || !strings.Contains(b, c.want) {
				t.Errorf("%s %s (%s) %s: status %d body %s; want 403 %q", op.Method, op.Path, op.ID, c.name, status, b, c.want)
			}
		}
		status, b := call(map[string]string{"X-CSRF-Token": csrf}, h.srv.URL)
		if strings.Contains(b, csrfRefusal) || strings.Contains(b, originRefusal) || status == 401 {
			t.Errorf("%s %s (%s): the right token and origin were refused: %d %s", op.Method, op.Path, op.ID, status, b)
		}
	}
	if n < 10 {
		t.Fatalf("only %d changing operations found in the contract; the walk is broken", n)
	}
}

// R6.2: one session's token does not work for another session.
func TestCSRFTokenIsPerSession(t *testing.T) {
	h := newHarness(t)
	owner, csrfA := h.setup(1, "alice")
	h.fake.AddUser(2, "bob")
	if res, b := h.do("POST", "/api/v1/users", map[string]string{"login": "bob", "role": "admin"}, owner, map[string]string{"X-CSRF-Token": csrfA}); res.StatusCode != 201 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	_, csrfB := h.signIn(2, cookies{})
	if csrfB == "" || csrfB == csrfA {
		t.Fatalf("each session must have its own token: %q %q", csrfA, csrfB)
	}
	if res, _ := h.do("PUT", "/api/v1/me/preferences", map[string]string{"theme": "dark"}, owner, map[string]string{"X-CSRF-Token": csrfB}); res.StatusCode != 403 {
		t.Fatalf("another session's token was accepted: %d", res.StatusCode)
	}
}

// R6.2: a read does not need a token, and a foreign Origin on a read does not matter.
func TestReadsNeedNoCSRFToken(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.setup(1, "alice")
	if res, b := h.do("GET", "/api/v1/me", nil, owner, nil); res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

// R6.2: a public change made from a foreign page is refused too, so another
// site cannot post the setup form; a client with no Origin (curl, the CLI) is
// not a browser and passes.
func TestPublicChangesRefuseAForeignOrigin(t *testing.T) {
	h := newHarness(t)
	code, _ := h.auth.NewSetupCode(context.Background())
	res, b := h.doRaw("POST", "/api/v1/setup/redeem", map[string]string{"code": code}, nil, nil, "https://evil.example")
	if res.StatusCode != 403 || !strings.Contains(string(b), originRefusal) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	res, b = h.doRaw("POST", "/api/v1/setup/redeem", map[string]string{"code": code}, nil, nil, "")
	if res.StatusCode != 200 {
		t.Fatalf("a client without an Origin must pass: %d %s", res.StatusCode, b)
	}
}

// R6.2: a CLI bearer token is not a browser session: no Origin, no CSRF token.
func TestCLITokenNeedsNoCSRFToken(t *testing.T) {
	mux := newMux(fakeDB{}) // Authn: a CLI-style principal, owner
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/machines/1", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code == 403 || rec.Code == 401 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// R6.2: Origin compares scheme, host and port.
func TestOriginAllowed(t *testing.T) {
	mk := func(origin string, secure bool) *http.Request {
		r := httptest.NewRequestWithContext(context.Background(), "POST", "/x", nil)
		r.Host = "pool.lan:8460"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if secure {
			r.TLS = &tls.ConnectionState{}
		}
		return r
	}
	cases := []struct {
		origin   string
		tls      bool
		required bool
		want     bool
	}{
		{"https://pool.lan:8460", true, true, true},
		{"https://POOL.lan:8460", true, true, true},
		{"http://pool.lan:8460", true, true, false},  // wrong scheme
		{"https://pool.lan:8461", true, true, false}, // wrong port
		{"https://pool.lan", true, true, false},
		{"https://evil.example", true, true, false},
		{"null", true, true, false},
		{"", true, true, false},
		{"", true, false, true},
		{"https://evil.example", true, false, false},
	}
	for _, c := range cases {
		if got := originAllowed(mk(c.origin, c.tls), c.required); got != c.want {
			t.Errorf("origin %q required=%v: got %v want %v", c.origin, c.required, got, c.want)
		}
	}
	_ = auth.RoleOwner
}

// D38: on loopback the console is plain HTTP; the Origin check still holds
// there (scheme, host and port), so a foreign page cannot post to it.
func TestOriginAllowedOnLoopbackHTTP(t *testing.T) {
	mk := func(origin string) *http.Request {
		r := httptest.NewRequestWithContext(context.Background(), "POST", "/x", nil)
		r.Host = "127.0.0.1:8460"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}
	for origin, want := range map[string]bool{
		"http://127.0.0.1:8460":  true,
		"http://127.0.0.1:8461":  false,
		"https://127.0.0.1:8460": false,
		"http://localhost:8460":  false,
		"https://evil.example":   false,
		"null":                   false,
		"":                       false,
	} {
		if got := originAllowed(mk(origin), true); got != want {
			t.Errorf("origin %q: got %v want %v", origin, got, want)
		}
	}
}
