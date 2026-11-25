package api

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backup"
	"github.com/luis-mc/ci-runners-shared-pool/internal/notify"
	"github.com/luis-mc/ci-runners-shared-pool/internal/secrets"
)

const (
	apiHookSecret = "whsec-api-0123456789-SIGNING"
	apiMailSecret = "pw-api-Sup3rS3cret"
)

// notifyEnv signs three users in (owner, admin, viewer) on the harness of the
// GitHub area and mounts the notification routes next to it.
type notifyEnv struct {
	h       *harness
	srv     *httptest.Server
	secrets *secrets.Memory
	logs    *bytes.Buffer
	audit   *audit.Log
	users   map[string]struct {
		c    cookies
		csrf string
	}
}

func newNotifyEnv(t *testing.T, trust *x509.CertPool) *notifyEnv {
	t.Helper()
	h := newHarness(t)
	ownerC, ownerCSRF := h.setup(1, "alice")
	for _, u := range []struct {
		id    int64
		login string
		role  string
	}{{2, "bob", "viewer"}, {3, "carol", "admin"}} {
		h.fake.AddUser(u.id, u.login)
		if res, b := h.do("POST", "/api/v1/users", map[string]string{"login": u.login, "role": u.role}, ownerC, map[string]string{"X-CSRF-Token": ownerCSRF}); res.StatusCode != 201 {
			t.Fatalf("add %s: %d %s", u.login, res.StatusCode, b)
		}
	}
	e := &notifyEnv{h: h, secrets: secrets.NewMemory(), logs: &bytes.Buffer{}, users: map[string]struct {
		c    cookies
		csrf string
	}{}}
	e.users["owner"] = struct {
		c    cookies
		csrf string
	}{ownerC, ownerCSRF}
	for name, id := range map[string]int64{"viewer": 2, "admin": 3} {
		c, csrf := h.signIn(id, cookies{})
		e.users[name] = struct {
			c    cookies
			csrf string
		}{c, csrf}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	db, err := backup.OpenDBForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	e.audit = audit.New(db)
	logger := slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc := notify.New(notify.Config{DB: db, Secrets: e.secrets, Logger: logger, AllowLoopback: true, Trust: trust})
	mux := http.NewServeMux()
	Register(mux, Options{Version: "test", DB: okPinger{}, Logger: logger, Notify: svc,
		GitHub: &GitHubArea{Auth: h.auth, Repos: h.repos, WebBase: h.fake.URL}})
	e.srv = httptest.NewServer(Recover(logger, mux))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *notifyEnv) do(as, method, path string, body any) (int, []byte) {
	e.h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, e.srv.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if as != "" {
		u := e.users[as]
		req.Header.Set("Cookie", u.c.header())
		req.Header.Set("X-CSRF-Token", u.csrf)
		req.Header.Set("Origin", e.srv.URL)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.h.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

func webhookBody(url string) map[string]any {
	return map[string]any{"kind": "webhook", "name": "Team chat", "events": []string{"machine.offline", "job.waiting"},
		"min_severity": "warning", "url": url, "secret": apiHookSecret}
}

func TestNotifyRequiresSignIn(t *testing.T) {
	e := newNotifyEnv(t, nil)
	for _, c := range []struct{ m, p string }{
		{"GET", "/api/v1/notification-channels"}, {"POST", "/api/v1/notification-channels"},
		{"PATCH", "/api/v1/notification-channels/x"}, {"DELETE", "/api/v1/notification-channels/x"},
		{"POST", "/api/v1/notification-channels/x/test"},
		{"GET", "/api/v1/notices"}, {"GET", "/api/v1/notification-thresholds"}, {"PUT", "/api/v1/notification-thresholds"},
	} {
		if code, b := e.do("", c.m, c.p, map[string]any{}); code != 401 || errCode(b) != CodeUnauthenticated {
			t.Errorf("%s %s: %d %s", c.m, c.p, code, b)
		}
	}
}

func TestNotifyRolesAndSecretsNeverReturned(t *testing.T) { // R5.9, R6.2
	e := newNotifyEnv(t, nil)
	// A viewer may not add a webhook; an admin may.
	if code, b := e.do("viewer", "POST", "/api/v1/notification-channels", webhookBody("https://hooks.example.com/x")); code != 403 || errCode(b) != CodeForbidden {
		t.Fatalf("%d %s", code, b)
	}
	code, b := e.do("admin", "POST", "/api/v1/notification-channels", webhookBody("https://hooks.example.com/x"))
	if code != 201 {
		t.Fatalf("%d %s", code, b)
	}
	var hook struct {
		ID        string `json:"id"`
		Scope     string `json:"scope"`
		SecretSet bool   `json:"secret_set"`
	}
	_ = json.Unmarshal(b, &hook)
	if hook.Scope != "pool" || !hook.SecretSet || !strings.HasPrefix(hook.ID, "nch_") {
		t.Fatalf("%s", b)
	}
	// The viewer adds an email channel of their own.
	code, b = e.do("viewer", "POST", "/api/v1/notification-channels", map[string]any{
		"kind": "email", "name": "My mail", "events": []string{"disk.low"}, "address": "bob@example.com",
		"smtp": map[string]any{"host": "smtp.example.com", "port": 587, "username": "bob", "from": "crsp@example.com", "password": apiMailSecret}})
	if code != 201 || !strings.Contains(string(b), `"has_password":true`) {
		t.Fatalf("%d %s", code, b)
	}
	var mail struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(b, &mail)

	// Lists: the viewer sees only their own channel, the admin sees the webhook.
	_, b = e.do("viewer", "GET", "/api/v1/notification-channels", nil)
	if !strings.Contains(string(b), mail.ID) || strings.Contains(string(b), hook.ID) {
		t.Fatalf("viewer list: %s", b)
	}
	_, b = e.do("admin", "GET", "/api/v1/notification-channels", nil)
	if !strings.Contains(string(b), hook.ID) || strings.Contains(string(b), mail.ID) {
		t.Fatalf("admin list: %s", b)
	}
	// The viewer may not change or test or remove the webhook.
	for _, c := range []struct {
		m, p string
		body any
	}{
		{"PATCH", "/api/v1/notification-channels/" + hook.ID, map[string]any{"name": "x"}},
		{"DELETE", "/api/v1/notification-channels/" + hook.ID + "?confirm=true", nil},
		{"POST", "/api/v1/notification-channels/" + hook.ID + "/test", nil},
	} {
		if code, b := e.do("viewer", c.m, c.p, c.body); code != 403 {
			t.Errorf("%s %s: %d %s", c.m, c.p, code, b)
		}
	}
	// Nobody else sees the viewer's channel, not even the owner.
	if code, _ := e.do("owner", "PATCH", "/api/v1/notification-channels/"+mail.ID, map[string]any{"name": "x"}); code != 404 {
		t.Fatalf("%d", code)
	}

	// No answer, log or audit row carries a secret.
	everything := e.logs.String()
	for _, p := range []string{"/api/v1/notification-channels"} {
		_, b := e.do("admin", "GET", p, nil)
		everything += string(b)
		_, b = e.do("viewer", "GET", p, nil)
		everything += string(b)
	}
	page, _ := e.audit.List(context.Background(), audit.Filter{})
	raw, _ := json.Marshal(page)
	everything += string(raw)
	for _, s := range []string{apiHookSecret, apiMailSecret} {
		if strings.Contains(everything, s) {
			t.Fatalf("secret %q leaked", s)
		}
	}
	if len(e.secrets.Keys()) != 2 {
		t.Fatalf("the two secrets are in the secret store: %v", e.secrets.Keys())
	}
	if len(page.Items) != 2 || page.Items[0].Action != "notification.channel.create" {
		t.Fatalf("audit: %+v", page.Items)
	}
}

func TestNotifyValidationAnswers400WithProblems(t *testing.T) {
	e := newNotifyEnv(t, nil)
	code, b := e.do("admin", "POST", "/api/v1/notification-channels", webhookBody("http://hooks.example.com/x"))
	if code != 400 || errCode(b) != CodeBadRequest || !strings.Contains(string(b), "https") {
		t.Fatalf("%d %s", code, b)
	}
	if strings.Contains(string(b), apiHookSecret) {
		t.Fatal("the secret came back in an error")
	}
	if code, b := e.do("admin", "POST", "/api/v1/notification-channels", map[string]any{"kind": "desktop", "name": "d", "events": []string{"nope"}}); code != 400 {
		t.Fatalf("%d %s", code, b)
	}
}

func TestNotifyUpdateDeleteConfirmAndTest(t *testing.T) {
	var mu sync.Mutex
	var gotSig, gotTS string
	var gotBody []byte
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotSig, gotTS, gotBody = r.Header.Get(notify.HeaderSignature), r.Header.Get(notify.HeaderTimestamp), b
		mu.Unlock()
		w.WriteHeader(204)
	}))
	t.Cleanup(target.Close)
	pool := x509.NewCertPool()
	pool.AddCert(target.Certificate())
	e := newNotifyEnv(t, pool)

	code, b := e.do("owner", "POST", "/api/v1/notification-channels", webhookBody(target.URL+"/hook"))
	if code != 201 {
		t.Fatalf("%d %s", code, b)
	}
	var c struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(b, &c)
	path := "/api/v1/notification-channels/" + c.ID

	// Update: a field left out keeps its value; the secret is not returned.
	code, b = e.do("owner", "PATCH", path, map[string]any{"enabled": false, "events": []string{"disk.low"}})
	if code != 200 || !strings.Contains(string(b), `"enabled":false`) || !strings.Contains(string(b), `"secret_set":true`) || strings.Contains(string(b), apiHookSecret) {
		t.Fatalf("%d %s", code, b)
	}
	// Test: really sends, signed.
	code, b = e.do("owner", "POST", path+"/test", nil)
	var res struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
		SentAt  string `json:"sent_at"`
	}
	_ = json.Unmarshal(b, &res)
	if code != 200 || !res.OK || res.SentAt == "" || !strings.Contains(res.Message, "HTTP 204") {
		t.Fatalf("%d %s", code, b)
	}
	mu.Lock()
	if gotSig != notify.Sign([]byte(apiHookSecret), gotTS, gotBody) {
		t.Errorf("signature %q does not match the body", gotSig)
	}
	mu.Unlock()
	// A second test at once is refused (R5.31).
	if code, b := e.do("owner", "POST", path+"/test", nil); code != 429 || errCode(b) != CodeRateLimited {
		t.Fatalf("%d %s", code, b)
	}
	// Delete without a yes names the channel and its events (UX-62).
	code, b = e.do("owner", "DELETE", path, nil)
	if code != 409 || errCode(b) != CodeNeedsConfirmation || !strings.Contains(string(b), "Team chat") || !strings.Contains(string(b), "disk.low") {
		t.Fatalf("%d %s", code, b)
	}
	if code, b = e.do("owner", "DELETE", path+"?confirm=true", nil); code != 204 {
		t.Fatalf("%d %s", code, b)
	}
	if code, _ = e.do("owner", "DELETE", path+"?confirm=true", nil); code != 404 {
		t.Fatalf("%d", code)
	}
	if len(e.secrets.Keys()) != 0 {
		t.Fatalf("the secret must go with the channel: %v", e.secrets.Keys())
	}
	page, _ := e.audit.List(context.Background(), audit.Filter{})
	actions := map[string]int{}
	for _, it := range page.Items {
		actions[it.Action]++
	}
	for _, want := range []string{"notification.channel.create", "notification.channel.update", "notification.channel.test", "notification.channel.delete"} {
		if actions[want] != 1 {
			t.Errorf("audit %s: %v", want, actions)
		}
	}
	if strings.Contains(e.logs.String(), apiHookSecret) {
		t.Fatal("secret in logs")
	}
}

func TestNotifyUnwiredAnswers501(t *testing.T) {
	h := newHarness(t)
	owner, csrf := h.setup(1, "alice")
	mux := http.NewServeMux()
	Register(mux, Options{Version: "test", DB: okPinger{}, GitHub: &GitHubArea{Auth: h.auth, Repos: h.repos, WebBase: h.fake.URL}})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL+"/api/v1/notification-channels", nil)
	req.Header.Set("Cookie", owner.header())
	req.Header.Set("X-CSRF-Token", csrf)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 501 {
		t.Fatalf("%d", res.StatusCode)
	}
}

func TestThresholdsNeedAdmin(t *testing.T) { // R5.8
	e := newNotifyEnv(t, nil)
	good := map[string]any{"offline_grace_minutes": 3, "job_waiting_minutes": 20, "disk_percent": 15, "flap_window_minutes": 10, "hourly_cap": 30}
	if code, _ := e.do("viewer", "PUT", "/api/v1/notification-thresholds", good); code != 403 {
		t.Fatalf("a viewer must not change thresholds: %d", code)
	}
	bad := map[string]any{"offline_grace_minutes": 0, "job_waiting_minutes": 20, "disk_percent": 15, "flap_window_minutes": 10, "hourly_cap": 30}
	if code, b := e.do("admin", "PUT", "/api/v1/notification-thresholds", bad); code != 400 || errCode(b) != CodeBadRequest {
		t.Fatalf("%d %s", code, b)
	}
	if code, b := e.do("admin", "PUT", "/api/v1/notification-thresholds", good); code != 200 || !strings.Contains(string(b), `"disk_percent":15`) {
		t.Fatalf("%d %s", code, b)
	}
	// A viewer reads them.
	if code, b := e.do("viewer", "GET", "/api/v1/notification-thresholds", nil); code != 200 || !strings.Contains(string(b), `"hourly_cap":30`) {
		t.Fatalf("%d %s", code, b)
	}
	if code, b := e.do("viewer", "GET", "/api/v1/notices", nil); code != 200 || !strings.Contains(string(b), `"items"`) {
		t.Fatalf("%d %s", code, b)
	}
}
