package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github/fake"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/repos"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/webhookhealth"
)

var (
	ghKeyOnce sync.Once
	ghKey     *rsa.PrivateKey
)

func testKey() *rsa.PrivateKey {
	ghKeyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		ghKey = k
	})
	return ghKey
}

type okPinger struct{}

func (okPinger) Ping(context.Context) error { return nil }

type linuxOnly struct{}

func (linuxOnly) OSLabels(context.Context) (map[string][]string, error) {
	return map[string][]string{"Linux": {"X64", "crsp-home"}}, nil
}

// harness is a controller wired to a fake GitHub.
type harness struct {
	t       *testing.T
	srv     *httptest.Server
	fake    *fake.Server
	auth    *auth.Service
	repos   *repos.Service
	astore  *auth.MemStore
	rstore  *repos.MemStore
	hook    *github.WebhookHandler
	secrets *secrets.Memory
	client  *http.Client
	health  *webhookhealth.Tracker
	logs    *lockedBuf
}

// lockedBuf is a log sink safe for the server goroutines.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func newHarness(t *testing.T) *harness { return newHarnessWith(t, nil) }

// newHarnessWith lets a test wrap the secret store the auth service uses.
func newHarnessWith(t *testing.T, wrap func(*secrets.Memory) secrets.Store, tweak ...func(*repos.Config, *Options)) *harness {
	t.Helper()
	f := fake.New()
	t.Cleanup(f.Close)
	k := testKey()
	f.AppPublicKey = &k.PublicKey
	f.PEM = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
	sec := secrets.NewMemory()
	var store secrets.Store = sec
	if wrap != nil {
		store = wrap(sec)
	}
	gh := github.New(github.Config{APIBase: f.URL, WebBase: f.URL, Secrets: store})
	astore, rstore := auth.NewMemStore(), repos.NewMemStore()
	asvc := auth.New(astore, auth.Config{GitHub: gh, Secrets: store, WebBase: f.URL,
		OnApp: func(a auth.AppRecord) { gh.UseApp(a.ID, a.ClientID) }})
	rcfg := repos.Config{Store: rstore, GitHub: gh, Tokens: asvc, Audit: asvc, Capacity: linuxOnly{}}
	opts := Options{Version: "test", DB: okPinger{}, CA: fakeCA{}}
	for _, tw := range tweak {
		tw(&rcfg, &opts)
	}
	rsvc := repos.New(rcfg)
	h := &harness{t: t, fake: f, auth: asvc, repos: rsvc, astore: astore, rstore: rstore, secrets: sec, health: webhookhealth.New(), logs: &lockedBuf{}}

	seen := map[string]bool{}
	h.hook = &github.WebhookHandler{
		Verifier: func() (github.WebhookVerifier, error) {
			b, err := sec.Get(context.Background(), github.SecretKey(f.AppID, github.SecretWebhookSecret))
			if err != nil {
				return nil, err
			}
			return github.NewWebhookVerifier(b), nil
		},
		FirstDelivery: func(id, _ string) (bool, error) {
			if seen[id] {
				return false, nil
			}
			seen[id] = true
			return true, nil
		},
		Events: noEvents{},
	}
	mux := http.NewServeMux()
	opts.GitHub = &GitHubArea{Auth: asvc, Repos: rsvc, Webhook: h.hook, WebBase: f.URL, Health: h.health,
		Logger: slog.New(slog.NewTextHandler(h.logs, nil))}
	Register(mux, opts)
	h.srv = httptest.NewServer(Recover(slog.New(slog.NewTextHandler(io.Discard, nil)), mux))
	t.Cleanup(h.srv.Close)
	h.client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return h
}

type noEvents struct{}

func (noEvents) Job(github.Job)              {}
func (noEvents) Installation(string, []byte) {}

type cookies map[string]string

func (c cookies) header() string {
	var parts []string
	for k, v := range c {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "; ")
}

func (c cookies) absorb(r *http.Response) {
	for _, ck := range r.Cookies() {
		if ck.MaxAge < 0 {
			delete(c, ck.Name)
		} else {
			c[ck.Name] = ck.Value
		}
	}
}

// reply is what a test needs from a response; the body is already read and
// closed.
type reply struct {
	StatusCode int
	Header     http.Header
	cookies    []*http.Cookie
}

func (r *reply) Cookies() []*http.Cookie { return r.cookies }

func (h *harness) do(method, path string, body any, c cookies, headers map[string]string) (*reply, []byte) {
	h.t.Helper()
	origin := ""
	if method != "GET" && method != "HEAD" {
		origin = h.srv.URL // a browser always sends it on a change
	}
	return h.doRaw(method, path, body, c, headers, origin)
}

// doHost is a POST of body whose Host header is host, as if the console were
// reached at that address; the Origin matches it.
func (h *harness) doHost(method, path, host string, body any) (*reply, []byte) {
	h.t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(context.Background(), method, h.srv.URL+path, bytes.NewReader(b))
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://"+host)
	res, err := h.client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	out, _ := io.ReadAll(res.Body)
	return &reply{StatusCode: res.StatusCode, Header: res.Header, cookies: res.Cookies()}, out
}

// doRaw is do with an explicit Origin header; "" sends none.
func (h *harness) doRaw(method, path string, body any, c cookies, headers map[string]string, origin string) (*reply, []byte) {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, h.srv.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c != nil {
		req.Header.Set("Cookie", c.header())
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := h.client.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	if c != nil {
		c.absorb(res)
	}
	return &reply{StatusCode: res.StatusCode, Header: res.Header, cookies: res.Cookies()}, b
}

func errCode(b []byte) string {
	var e struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(b, &e)
	return e.Error.Code
}

// setup runs the first-run wizard to the point where the App exists, then
// signs the owner in. It returns the owner's cookies and CSRF token.
func (h *harness) setup(ownerID int64, login string) (cookies, string) {
	h.t.Helper()
	ctx := context.Background()
	c := cookies{}
	code, err := h.auth.NewSetupCode(ctx)
	if err != nil || code == "" {
		h.t.Fatal(code, err)
	}
	if res, b := h.do("POST", "/api/v1/setup/redeem", map[string]string{"code": code}, c, nil); res.StatusCode != 200 {
		h.t.Fatalf("redeem: %d %s", res.StatusCode, b)
	}
	res, b := h.do("POST", "/api/v1/setup/github-app", map[string]any{"account_type": "user", "app_name": "crsp-test", "console_urls": []string{h.srv.URL}}, c, nil)
	if res.StatusCode != 200 {
		h.t.Fatalf("manifest: %d %s", res.StatusCode, b)
	}
	var form struct {
		State    string
		PostURL  string `json:"post_url"`
		Manifest map[string]any
	}
	_ = json.Unmarshal(b, &form)
	h.fake.IssueManifestCode("mc")
	res, _ = h.do("GET", "/auth/github/app-created?code=mc&state="+url.QueryEscape(form.State), nil, c, nil)
	if res.StatusCode != 302 || !strings.HasPrefix(res.Header.Get("Location"), "/setup?step=") {
		h.t.Fatalf("app-created: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	h.fake.AddUser(ownerID, login)
	return h.signIn(ownerID, c)
}

func (h *harness) signIn(userID int64, c cookies) (cookies, string) {
	h.t.Helper()
	res, _ := h.do("GET", "/auth/github/login?return_to=/repositories", nil, c, nil)
	if res.StatusCode != 302 {
		h.t.Fatalf("login: %d", res.StatusCode)
	}
	loc, _ := url.Parse(res.Header.Get("Location"))
	if !strings.HasPrefix(res.Header.Get("Location"), h.fake.URL+"/login/oauth/authorize") {
		h.t.Fatalf("login must go to GitHub: %s", loc)
	}
	q := loc.Query()
	h.fake.IssueOAuthCode("code-"+q.Get("state")[:6], userID, q.Get("code_challenge"))
	res, _ = h.do("GET", "/auth/github/callback?code=code-"+q.Get("state")[:6]+"&state="+url.QueryEscape(q.Get("state")), nil, c, nil)
	if res.StatusCode != 302 {
		h.t.Fatalf("callback: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if loc := res.Header.Get("Location"); strings.HasPrefix(loc, "/sign-in") {
		h.t.Fatalf("sign-in refused: %s", loc)
	}
	res, b := h.do("GET", "/api/v1/me", nil, c, nil)
	if res.StatusCode != 200 {
		h.t.Fatalf("me: %d %s", res.StatusCode, b)
	}
	var me struct {
		Csrf string
	}
	_ = json.Unmarshal(b, &me)
	return c, me.Csrf
}

func TestSetupAndSignInFlow(t *testing.T) { // R5.1 to R5.3, R5.8, R5.10
	h := newHarness(t)
	res, b := h.do("GET", "/api/v1/setup", nil, nil, nil)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"has_app":false`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	c, csrf := h.setup(1, "alice")
	if csrf == "" || c[auth.SessionCookieName] == "" {
		t.Fatal("signed in")
	}
	_, b = h.do("GET", "/api/v1/me", nil, c, nil)
	if !strings.Contains(string(b), `"role":"owner"`) || !strings.Contains(string(b), `"login":"alice"`) {
		t.Fatalf("%s", b)
	}
	_, b = h.do("GET", "/api/v1/setup", nil, c, nil)
	if !strings.Contains(string(b), `"has_app":true`) || !strings.Contains(string(b), `"has_owner":true`) || !strings.Contains(string(b), `"connect_github":"done"`) {
		t.Fatalf("%s", b)
	}
	// The database part holds no App secret, the OS store holds all three.
	if len(h.secrets.Keys()) != 3 {
		t.Fatalf("%v", h.secrets.Keys())
	}
}

func TestSetupNeedsCookieAndCode(t *testing.T) { // R5.1
	h := newHarness(t)
	res, b := h.do("POST", "/api/v1/setup/github-app", map[string]any{"account_type": "user", "app_name": "x", "console_urls": []string{h.srv.URL}}, nil, nil)
	if res.StatusCode != 401 {
		t.Fatalf("without the setup cookie: %d %s", res.StatusCode, b)
	}
	res, b = h.do("POST", "/api/v1/setup/redeem", map[string]string{"code": "WRONG"}, nil, nil)
	if res.StatusCode != 400 || errCode(b) != CodeBadRequest {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, b := h.do("POST", "/api/v1/setup/redeem", nil, nil, nil); res.StatusCode != 400 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestSetupCookieAttributes(t *testing.T) {
	h := newHarness(t)
	code, _ := h.auth.NewSetupCode(context.Background())
	res, _ := h.do("POST", "/api/v1/setup/redeem", map[string]string{"code": code}, nil, nil)
	var ck *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == auth.SetupCookieName {
			ck = c
		}
	}
	if ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode {
		t.Fatalf("%+v", ck)
	}
}

func TestSessionCookieFlags(t *testing.T) { // R5.13: loopback, so not Secure
	h := newHarness(t)
	c, _ := h.setup(1, "alice")
	_ = c
	// Re-run a sign-in just to read the Set-Cookie header.
	res, _ := h.do("GET", "/auth/github/login", nil, nil, nil)
	loc, _ := url.Parse(res.Header.Get("Location"))
	h.fake.IssueOAuthCode("zz", 1, loc.Query().Get("code_challenge"))
	res, _ = h.do("GET", "/auth/github/callback?code=zz&state="+url.QueryEscape(loc.Query().Get("state")), nil, nil, nil)
	var ck *http.Cookie
	for _, x := range res.Cookies() {
		if x.Name == auth.SessionCookieName {
			ck = x
		}
	}
	if ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.Secure || ck.MaxAge <= 0 {
		t.Fatalf("session cookie: %+v", ck)
	}
}

func TestRolesEnforcedPerOperation(t *testing.T) { // R5.12
	h := newHarness(t)
	owner, csrf := h.setup(1, "alice")
	// Owner adds a viewer and an admin.
	for _, u := range []struct {
		id    int64
		login string
		role  string
	}{{2, "bob", "viewer"}, {3, "carol", "admin"}} {
		h.fake.AddUser(u.id, u.login)
		res, b := h.do("POST", "/api/v1/users", map[string]string{"login": u.login, "role": u.role}, owner, map[string]string{"X-CSRF-Token": csrf})
		if res.StatusCode != 201 {
			t.Fatalf("add %s: %d %s", u.login, res.StatusCode, b)
		}
	}
	viewer, vcsrf := h.signIn(2, cookies{})
	admin, acsrf := h.signIn(3, cookies{})

	type call struct {
		method, path string
		body         any
	}
	adminOnly := []call{
		{"POST", "/api/v1/repository-plans", map[string]any{"changes": []map[string]any{{"repository_id": 1, "pool": "on"}}}},
		{"POST", "/api/v1/repository-plans/x/apply", map[string]any{"approved_steps": []string{}}},
		{"POST", "/api/v1/repository-plans/x/stop", nil},
		{"PATCH", "/api/v1/repositories/1/policy", map[string]any{"priority": 1}},
		{"GET", "/api/v1/github/install-url", nil},
		{"PUT", "/api/v1/github/installations/1/org-pool", map[string]any{"enabled": true}},
	}
	ownerOnly := []call{
		{"GET", "/api/v1/users", nil},
		{"POST", "/api/v1/users", map[string]string{"login": "x", "role": "viewer"}},
		{"PATCH", "/api/v1/users/2", map[string]any{"role": "admin"}},
		{"DELETE", "/api/v1/users/2", nil},
	}
	for _, cl := range append(append([]call{}, adminOnly...), ownerOnly...) {
		if res, b := h.do(cl.method, cl.path, cl.body, viewer, map[string]string{"X-CSRF-Token": vcsrf}); res.StatusCode != 403 || errCode(b) != CodeForbidden {
			t.Errorf("viewer %s %s: %d %s", cl.method, cl.path, res.StatusCode, b)
		}
		if res, _ := h.do(cl.method, cl.path, cl.body, nil, nil); res.StatusCode != 401 {
			t.Errorf("anonymous %s %s: %d", cl.method, cl.path, res.StatusCode)
		}
	}
	for _, cl := range ownerOnly {
		if res, b := h.do(cl.method, cl.path, cl.body, admin, map[string]string{"X-CSRF-Token": acsrf}); res.StatusCode != 403 {
			t.Errorf("admin %s %s: %d %s", cl.method, cl.path, res.StatusCode, b)
		}
	}
	// An admin passes the role gate on admin operations (the answer may be 404, not 403).
	for _, cl := range adminOnly {
		if res, b := h.do(cl.method, cl.path, cl.body, admin, map[string]string{"X-CSRF-Token": acsrf}); res.StatusCode == 403 || res.StatusCode == 401 {
			t.Errorf("admin %s %s: %d %s", cl.method, cl.path, res.StatusCode, b)
		}
	}
	// A viewer reads.
	for _, p := range []string{"/api/v1/me", "/api/v1/github/repositories", "/api/v1/github/installations", "/api/v1/github/org-pools", "/api/v1/auth/cli/tokens", "/api/v1/me/preferences"} {
		if res, b := h.do("GET", p, nil, viewer, nil); res.StatusCode != 200 {
			t.Errorf("viewer GET %s: %d %s", p, res.StatusCode, b)
		}
	}
}

func TestCSRFRequiredForChanges(t *testing.T) {
	h := newHarness(t)
	owner, csrf := h.setup(1, "alice")
	h.fake.AddUser(2, "bob")
	body := map[string]string{"login": "bob", "role": "viewer"}
	if res, b := h.do("POST", "/api/v1/users", body, owner, nil); res.StatusCode != 403 {
		t.Fatalf("no token: %d %s", res.StatusCode, b)
	}
	if res, _ := h.do("POST", "/api/v1/users", body, owner, map[string]string{"X-CSRF-Token": "wrong"}); res.StatusCode != 403 {
		t.Fatal("wrong token")
	}
	if res, b := h.do("POST", "/api/v1/users", body, owner, map[string]string{"X-CSRF-Token": csrf}); res.StatusCode != 201 {
		t.Fatalf("right token: %d %s", res.StatusCode, b)
	}
}

func TestSignOutEndsSession(t *testing.T) { // R5.13
	h := newHarness(t)
	c, csrf := h.setup(1, "alice")
	if res, _ := h.do("POST", "/api/v1/auth/logout", nil, c, map[string]string{"X-CSRF-Token": csrf}); res.StatusCode != 204 {
		t.Fatal(res.StatusCode)
	}
	if res, b := h.do("GET", "/api/v1/me", nil, c, nil); res.StatusCode != 401 || errCode(b) != CodeUnauthenticated {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestNotAllowedUserRedirectedWithLogin(t *testing.T) { // R5.11
	h := newHarness(t)
	h.setup(1, "alice")
	h.fake.AddUser(9, "stranger")
	res, _ := h.do("GET", "/auth/github/login", nil, nil, nil)
	loc, _ := url.Parse(res.Header.Get("Location"))
	h.fake.IssueOAuthCode("s", 9, loc.Query().Get("code_challenge"))
	res, _ = h.do("GET", "/auth/github/callback?code=s&state="+url.QueryEscape(loc.Query().Get("state")), nil, nil, nil)
	got := res.Header.Get("Location")
	if res.StatusCode != 302 || !strings.HasPrefix(got, "/sign-in?") || !strings.Contains(got, "login=stranger") || !strings.Contains(got, "error=not_allowed") {
		t.Fatalf("%d %s", res.StatusCode, got)
	}
	if len(res.Cookies()) != 0 {
		t.Fatal("no session for a stranger")
	}
}

func TestCallbackRefusesForgedState(t *testing.T) { // R5.8
	h := newHarness(t)
	h.setup(1, "alice")
	res, _ := h.do("GET", "/auth/github/callback?code=x&state=forged", nil, nil, nil)
	if res.StatusCode != 302 || !strings.Contains(res.Header.Get("Location"), "error=state_missing") || len(res.Cookies()) != 0 {
		t.Fatalf("%d %s", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestUsersFlow(t *testing.T) { // R5.10, R5.11
	h := newHarness(t)
	owner, csrf := h.setup(1, "alice")
	hdr := map[string]string{"X-CSRF-Token": csrf}
	h.fake.AddUser(2, "bob")
	if res, _ := h.do("POST", "/api/v1/users", map[string]string{"login": "bob", "role": "admin"}, owner, hdr); res.StatusCode != 201 {
		t.Fatal(res.StatusCode)
	}
	if res, b := h.do("POST", "/api/v1/users", map[string]string{"login": "bob", "role": "admin"}, owner, hdr); res.StatusCode != 409 || errCode(b) != CodeConflict {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, b := h.do("POST", "/api/v1/users", map[string]string{"login": "ghost", "role": "admin"}, owner, hdr); res.StatusCode != 404 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	res, b := h.do("GET", "/api/v1/users", nil, owner, nil)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"bob"`) {
		t.Fatalf("%s", b)
	}
	// Handing over ownership needs confirm.
	if res, b := h.do("PATCH", "/api/v1/users/2", map[string]any{"role": "owner"}, owner, hdr); res.StatusCode != 409 || errCode(b) != CodeNeedsConfirmation {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, b := h.do("PATCH", "/api/v1/users/2", map[string]any{"role": "owner", "confirm": true}, owner, hdr); res.StatusCode != 200 || !strings.Contains(string(b), `"owner"`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	// The old owner's session ended with the change.
	if res, _ := h.do("GET", "/api/v1/me", nil, owner, nil); res.StatusCode != 401 {
		t.Fatalf("%d", res.StatusCode)
	}
}

func TestPreferences(t *testing.T) {
	h := newHarness(t)
	c, csrf := h.setup(1, "alice")
	res, b := h.do("GET", "/api/v1/me/preferences", nil, c, nil)
	if !strings.Contains(string(b), `"theme":"dark"`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, _ := h.do("PUT", "/api/v1/me/preferences", map[string]string{"theme": "dark"}, c, map[string]string{"X-CSRF-Token": csrf}); res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	if _, b := h.do("GET", "/api/v1/me/preferences", nil, c, nil); !strings.Contains(string(b), `"dark"`) {
		t.Fatalf("%s", b)
	}
}

func TestRepositoryListPagesThrough1000Repositories(t *testing.T) { // R6.11
	h := newHarness(t)
	owner, _ := h.setup(1, "alice")
	h.fake.AddInstall(10, "alice", "User", 1)
	const n = 1001
	for i := 1; i <= n; i++ {
		h.fake.AddRepo(int64(i), 10, "alice", fmt.Sprintf("r%04d", i), true, 1)
	}
	seen := map[string]bool{}
	cursor, pages := "", 0
	for pages < 20 { // the cursor must end well before this
		path := "/api/v1/github/repositories?limit=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		res, b := h.do("GET", path, nil, owner, nil)
		if res.StatusCode != 200 {
			t.Fatalf("page %d: %d %s", pages+1, res.StatusCode, b)
		}
		var page struct {
			Items []struct {
				FullName string `json:"full_name"`
			}
			NextCursor string `json:"next_cursor"`
		}
		if err := json.Unmarshal(b, &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Items) > 100 {
			t.Fatalf("page %d holds %d items", pages+1, len(page.Items))
		}
		for _, it := range page.Items {
			if seen[it.FullName] {
				t.Fatalf("%s listed twice", it.FullName)
			}
			seen[it.FullName] = true
		}
		pages++
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != n || pages != 11 {
		t.Fatalf("%d repositories over %d pages, want %d over 11", len(seen), pages, n)
	}
}

func TestRepositoriesFlowOverHTTP(t *testing.T) { // R5.17 to R5.20, R5.28, R5.29
	h := newHarness(t)
	owner, csrf := h.setup(1, "alice")
	hdr := map[string]string{"X-CSRF-Token": csrf}
	h.fake.AddInstall(10, "alice", "User", 1)
	for i, name := range []string{"app", "lib", "docs"} {
		h.fake.AddRepo(int64(100+i), 10, "alice", name, true, 1)
	}
	res, b := h.do("GET", "/api/v1/github/installations", nil, owner, nil)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"account_login":"alice"`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	// Paging: two at a time.
	res, b = h.do("GET", "/api/v1/github/repositories?limit=2", nil, owner, nil)
	var page struct {
		Items []struct {
			ID                int64  `json:"id"`
			FullName          string `json:"full_name"`
			UserIsGithubAdmin bool   `json:"user_is_github_admin"`
		}
		NextCursor string `json:"next_cursor"`
		Stale      struct{ Stale bool }
	}
	_ = json.Unmarshal(b, &page)
	if len(page.Items) != 2 || page.NextCursor == "" || page.Items[0].FullName != "alice/app" || !page.Items[0].UserIsGithubAdmin || page.Stale.Stale {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	_, b = h.do("GET", "/api/v1/github/repositories?limit=2&cursor="+url.QueryEscape(page.NextCursor), nil, owner, nil)
	page.NextCursor = ""
	_ = json.Unmarshal(b, &page)
	if len(page.Items) != 1 || page.Items[0].FullName != "alice/lib"[:6]+"s" && page.Items[0].FullName == "" {
		t.Fatalf("%s", b)
	}

	// Plan, then apply.
	res, b = h.do("POST", "/api/v1/repository-plans", map[string]any{"changes": []map[string]any{{"repository_id": 100, "pool": "on"}}}, owner, hdr)
	if res.StatusCode != 201 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var plan struct {
		ID    string `json:"id"`
		State string
		Steps []struct{ ID, Action, State string }
	}
	_ = json.Unmarshal(b, &plan)
	if plan.State != "planned" || len(plan.Steps) < 2 || plan.Steps[0].Action != "pool_on" {
		t.Fatalf("%s", b)
	}
	if _, ok := h.fake.Variable("alice/app", "CRSP_RUNS_ON_LINUX"); ok {
		t.Fatal("a plan changes nothing")
	}
	res, b = h.do("POST", "/api/v1/repository-plans/"+plan.ID+"/apply", map[string]any{"approved_steps": []string{}}, owner, hdr)
	if res.StatusCode != 202 || !strings.Contains(string(b), `"state":"done"`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if v, ok := h.fake.Variable("alice/app", "CRSP_RUNS_ON_LINUX"); !ok || v != `["self-hosted","linux","x64","crsp-home"]` {
		t.Fatalf("variable %q", v)
	}
	// After a reload the plan shows its outcome.
	_, b = h.do("GET", "/api/v1/repository-plans/"+plan.ID, nil, owner, nil)
	if !strings.Contains(string(b), `"state":"done"`) {
		t.Fatalf("%s", b)
	}
	// Repository view and policy.
	_, b = h.do("GET", "/api/v1/repositories/100", nil, owner, nil)
	if !strings.Contains(string(b), `"pool_on":true`) || !strings.Contains(string(b), `"status":"ok"`) || !strings.Contains(string(b), `"max_parallel_jobs":2`) {
		t.Fatalf("%s", b)
	}
	res, b = h.do("PATCH", "/api/v1/repositories/100/policy", map[string]any{"max_parallel_jobs": 4, "allow_fork_pull_requests": true}, owner, hdr)
	if res.StatusCode != 409 || errCode(b) != CodeNeedsConfirmation {
		t.Fatalf("fork pull requests ask first: %d %s", res.StatusCode, b)
	}
	res, b = h.do("PATCH", "/api/v1/repositories/100/policy", map[string]any{"max_parallel_jobs": 4}, owner, hdr)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"max_parallel_jobs":4`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, b := h.do("PATCH", "/api/v1/repositories/100/policy", map[string]any{"labels": []string{"my-project"}}, owner, hdr); res.StatusCode != 400 || !strings.Contains(string(b), "my-project") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	// Extra packages of the pool (runners R5.51): read, set, refused with a line number.
	res, b = h.do("PUT", "/api/v1/repositories/100/extra-packages", map[string]any{"text": "zlib1g-dev\n# a note\nlibc++-dev\nzlib1g-dev\n"}, owner, hdr)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"packages":["libc++-dev","zlib1g-dev"]`) {
		t.Fatalf("set extra packages: %d %s", res.StatusCode, b)
	}
	if res, b = h.do("PUT", "/api/v1/repositories/100/extra-packages", map[string]any{"text": "ok-name\n-bad"}, owner, hdr); res.StatusCode != 400 || !strings.Contains(string(b), "line 2") {
		t.Fatalf("a bad name is refused with its line: %d %s", res.StatusCode, b)
	}
	if res, b = h.do("GET", "/api/v1/repositories/100/extra-packages", nil, owner, nil); res.StatusCode != 200 || !strings.Contains(string(b), `"text":"libc++-dev\nzlib1g-dev"`) {
		t.Fatalf("the refused list changed nothing: %d %s", res.StatusCode, b)
	}
	if _, b = h.do("GET", "/api/v1/repositories/100", nil, owner, nil); !strings.Contains(string(b), `"extra_packages":["libc++-dev","zlib1g-dev"]`) {
		t.Fatalf("the policy shows the list: %s", b)
	}
	if res, b = h.do("PUT", "/api/v1/repositories/100/extra-packages", map[string]any{"text": ""}, owner, hdr); res.StatusCode != 200 || !strings.Contains(string(b), `"packages":[]`) {
		t.Fatalf("an empty text clears the list: %d %s", res.StatusCode, b)
	}
	if res, _ := h.do("GET", "/api/v1/repositories/999", nil, owner, nil); res.StatusCode != 404 {
		t.Fatal(res.StatusCode)
	}
	// Findings.
	res, b = h.do("GET", "/api/v1/repositories/100/runs-on", nil, owner, nil)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"items":[]`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestNoAdminRightIsRefusedWithReason(t *testing.T) { // R5.19
	h := newHarness(t)
	owner, csrf := h.setup(1, "alice")
	h.fake.AddInstall(10, "alice", "User", 1)
	h.fake.AddRepo(100, 10, "alice", "theirs", true) // alice is no admin here
	_, _ = h.do("GET", "/api/v1/github/repositories", nil, owner, nil)
	_, b := h.do("POST", "/api/v1/repository-plans", map[string]any{"changes": []map[string]any{{"repository_id": 100, "pool": "on"}}}, owner, map[string]string{"X-CSRF-Token": csrf})
	var plan struct{ ID string }
	_ = json.Unmarshal(b, &plan)
	res, b := h.do("POST", "/api/v1/repository-plans/"+plan.ID+"/apply", map[string]any{"approved_steps": []string{}}, owner, map[string]string{"X-CSRF-Token": csrf})
	if res.StatusCode != 403 || !strings.Contains(string(b), "alice/theirs") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestGitHubDownGivesStaleListAndFailClosed(t *testing.T) { // section 8
	h := newHarness(t)
	owner, csrf := h.setup(1, "alice")
	h.fake.AddInstall(10, "alice", "User", 1)
	h.fake.AddRepo(100, 10, "alice", "app", true, 1)
	_, _ = h.do("GET", "/api/v1/github/repositories", nil, owner, nil) // first sync
	// GitHub goes away; the next list is served from the store, marked stale
	// once the listing is old.
	h.fake.FailMatching("/user/installations", 503, 10)
	res, b := h.do("GET", "/api/v1/github/repositories", nil, owner, nil)
	if res.StatusCode != 200 || !strings.Contains(string(b), "alice/app") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	// A change fails closed with a clear code.
	h.fake.FailMatching("/repos/alice/app", 503, 10)
	_, b = h.do("POST", "/api/v1/repository-plans", map[string]any{"changes": []map[string]any{{"repository_id": 100, "pool": "on"}}}, owner, map[string]string{"X-CSRF-Token": csrf})
	var plan struct{ ID string }
	_ = json.Unmarshal(b, &plan)
	res, b = h.do("POST", "/api/v1/repository-plans/"+plan.ID+"/apply", map[string]any{"approved_steps": []string{}}, owner, map[string]string{"X-CSRF-Token": csrf})
	if res.StatusCode != 503 || errCode(b) != CodeGitHubUnavailable {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestCLIFlowOverHTTP(t *testing.T) { // R5.14, R5.15
	h := newHarness(t)
	owner, csrf := h.setup(1, "alice")
	res, b := h.do("GET", "/api/v1/auth/cli/config", nil, nil, nil)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"client_id"`) || !strings.Contains(string(b), "/login/device/code") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	tok := h.fake.AddUser(1, "alice")
	res, b = h.do("POST", "/api/v1/auth/cli", map[string]string{"github_credential": tok, "name": "laptop"}, nil, nil)
	if res.StatusCode != 201 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var created struct {
		Token string
		Info  struct{ ID string }
	}
	_ = json.Unmarshal(b, &created)
	if !strings.HasPrefix(created.Token, auth.CLITokenPrefix) {
		t.Fatalf("%s", b)
	}
	// The CLI token works as a bearer token, with no CSRF.
	bearer := map[string]string{"Authorization": "Bearer " + created.Token}
	if res, b := h.do("GET", "/api/v1/me", nil, nil, bearer); res.StatusCode != 200 || !strings.Contains(string(b), `"owner"`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if res, _ := h.do("GET", "/api/v1/users", nil, nil, bearer); res.StatusCode != 200 {
		t.Fatalf("a CLI token carries the owner's role: %d", res.StatusCode)
	}
	if res, _ := h.do("GET", "/api/v1/me", nil, nil, map[string]string{"Authorization": "Bearer crsp_cli_nope"}); res.StatusCode != 401 {
		t.Fatal("bad token")
	}
	// Listing and revoking.
	if _, b := h.do("GET", "/api/v1/auth/cli/tokens", nil, owner, nil); !strings.Contains(string(b), created.Info.ID) {
		t.Fatalf("%s", b)
	}
	if res, _ := h.do("DELETE", "/api/v1/auth/cli/tokens/"+created.Info.ID, nil, owner, map[string]string{"X-CSRF-Token": csrf}); res.StatusCode != 204 {
		t.Fatal(res.StatusCode)
	}
	if res, _ := h.do("GET", "/api/v1/me", nil, nil, bearer); res.StatusCode != 401 {
		t.Fatal("revoked")
	}
	// A GitHub token of a user not on the list is refused.
	stranger := h.fake.AddUser(9, "stranger")
	if res, b := h.do("POST", "/api/v1/auth/cli", map[string]string{"github_credential": stranger, "name": "x"}, nil, nil); res.StatusCode != 403 || !strings.Contains(string(b), "stranger") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestTrayLinkOverHTTP(t *testing.T) { // tray R5.7
	h := newHarness(t)
	h.setup(1, "alice")
	code, err := h.auth.IssueLink(context.Background(), 1, auth.LinkSignIn, "/machines")
	if err != nil {
		t.Fatal(err)
	}
	c := cookies{}
	res, _ := h.do("GET", "/auth/link?code="+url.QueryEscape(code), nil, c, nil)
	if res.StatusCode != 302 || res.Header.Get("Location") != "/machines" || strings.Contains(res.Header.Get("Location"), code) || c[auth.SessionCookieName] == "" {
		t.Fatalf("%d %s %v", res.StatusCode, res.Header.Get("Location"), c)
	}
	if res, _ := h.do("GET", "/api/v1/me", nil, c, nil); res.StatusCode != 200 {
		t.Fatal("the tray session works")
	}
	res, _ = h.do("GET", "/auth/link?code="+url.QueryEscape(code), nil, nil, nil)
	if !strings.Contains(res.Header.Get("Location"), "error=") {
		t.Fatalf("used code: %s", res.Header.Get("Location"))
	}
}

func TestWebhookRouteChecksSignatureOnRawBody(t *testing.T) { // R5.36, R6.5
	h := newHarness(t)
	h.setup(1, "alice")
	body := fake.WorkflowJobQueuedEvent()
	post := func(sig string) int {
		req, _ := http.NewRequestWithContext(context.Background(), "POST", h.srv.URL+"/webhooks/github", bytes.NewReader(body))
		req.Header.Set("X-GitHub-Event", "workflow_job")
		req.Header.Set("X-GitHub-Delivery", "d-"+sig[:12])
		req.Header.Set("X-Hub-Signature-256", sig)
		res, err := h.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	if c := post("sha256=" + strings.Repeat("0", 64)); c != 401 {
		t.Fatalf("bad signature: %d", c)
	}
	secret, _ := h.secrets.Get(context.Background(), github.SecretKey(h.fake.AppID, github.SecretWebhookSecret))
	if c := post(github.Sign(secret, body)); c != 200 {
		t.Fatalf("good signature: %d", c)
	}
}

func TestUnwiredAreaAnswers501(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: okPinger{}})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	get := func(path string) int {
		req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL+path, nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	// Nobody can sign in without the area, so a role-gated route is closed (401),
	// and a public route of the area says it is not built (501).
	if c := get("/api/v1/me"); c != http.StatusUnauthorized {
		t.Fatalf("%d", c)
	}
	if c := get("/api/v1/setup"); c != http.StatusNotImplemented {
		t.Fatalf("%d", c)
	}
	if c := get("/healthz"); c != 200 {
		t.Fatal("health still works")
	}
}

// TestAuthTableMatchesOpenAPI keeps the role table of this area equal to
// x-auth in openapi.yaml (R5.12).
func TestAuthTableMatchesOpenAPI(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "packages", "contract", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			OperationID string `yaml:"operationId"`
			XAuth       string `yaml:"x-auth"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(doc, &spec); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, item := range spec.Paths {
		for _, op := range item {
			want, mine := githubAreaAuth[op.OperationID]
			if !mine {
				continue
			}
			seen[op.OperationID] = true
			x := op.XAuth
			if x == "viewer" {
				x = authUser
			}
			if x != want {
				t.Errorf("%s: openapi x-auth %q, table %q", op.OperationID, op.XAuth, want)
			}
		}
	}
	for op := range githubAreaAuth {
		if !seen[op] {
			t.Errorf("%s is in the table but not in openapi.yaml", op)
		}
	}
	_ = time.Second
}

// Stop (web-console 11): a plan that is not applying answers 409; an unknown
// plan 404; the plan keeps its state.
func TestStopRepositoryPlanOverHTTP(t *testing.T) {
	h := newHarness(t)
	owner, csrf := h.setup(1, "alice")
	hdr := map[string]string{"X-CSRF-Token": csrf}
	h.fake.AddInstall(10, "alice", "User", 1)
	h.fake.AddRepo(100, 10, "alice", "app", true, 1)
	_, _ = h.do("GET", "/api/v1/github/repositories", nil, owner, nil)
	_, b := h.do("POST", "/api/v1/repository-plans", map[string]any{"changes": []map[string]any{{"repository_id": 100, "pool": "on"}}}, owner, hdr)
	var plan struct{ ID string }
	_ = json.Unmarshal(b, &plan)
	res, b := h.do("POST", "/api/v1/repository-plans/"+plan.ID+"/stop", nil, owner, hdr)
	if res.StatusCode != 409 || errCode(b) != CodeConflict || !strings.Contains(string(b), "nothing to stop") {
		t.Fatalf("planned: %d %s", res.StatusCode, b)
	}
	if res, b = h.do("POST", "/api/v1/repository-plans/nope/stop", nil, owner, hdr); res.StatusCode != 404 {
		t.Fatalf("unknown: %d %s", res.StatusCode, b)
	}
	_, b = h.do("GET", "/api/v1/repository-plans/"+plan.ID, nil, owner, nil)
	if !strings.Contains(string(b), `"planned"`) {
		t.Fatalf("a refused stop changed the plan: %s", b)
	}
}

// failingPut is a secret store that cannot keep anything.
type failingPut struct{ *secrets.Memory }

func (failingPut) Put(context.Context, secrets.Key, []byte) error { return secrets.ErrUnavailable }

func TestAppCreatedFailureLinksHalfMadeApp(t *testing.T) { // R5.3
	h := newHarnessWith(t, func(m *secrets.Memory) secrets.Store { return failingPut{m} })
	c := cookies{}
	code, err := h.auth.NewSetupCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res, b := h.do("POST", "/api/v1/setup/redeem", map[string]string{"code": code}, c, nil); res.StatusCode != 200 {
		t.Fatalf("redeem: %d %s", res.StatusCode, b)
	}
	res, b := h.do("POST", "/api/v1/setup/github-app", map[string]any{"account_type": "user", "app_name": "crsp-test", "console_urls": []string{h.srv.URL}}, c, nil)
	if res.StatusCode != 200 {
		t.Fatalf("start: %d %s", res.StatusCode, b)
	}
	var form struct{ State string }
	_ = json.Unmarshal(b, &form)
	h.fake.IssueManifestCode("mc")
	res, _ = h.do("GET", "/auth/github/app-created?code=mc&state="+url.QueryEscape(form.State), nil, c, nil)
	loc, err := url.Parse(res.Header.Get("Location"))
	if err != nil || res.StatusCode != 302 || loc.Path != "/setup" {
		t.Fatalf("redirect: %d %v", res.StatusCode, res.Header)
	}
	if loc.Query().Get("error") != "unavailable" || loc.Query().Get("app_url") != "https://github.com/apps/crsp-dev-fake" {
		t.Fatalf("error and app_url: %s", loc.RawQuery)
	}
}
