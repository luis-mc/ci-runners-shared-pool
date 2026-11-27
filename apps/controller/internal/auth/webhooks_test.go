package auth

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

type nopEvents struct{}

func (nopEvents) Job(github.Job)              {}
func (nopEvents) Installation(string, []byte) {}

type hookHealth struct {
	at  time.Time
	res string
}

func (h hookHealth) Last() (time.Time, string) { return h.at, h.res }

// hookEnv serves the controller's own webhook handler over TLS and points the
// self-test client at it, whatever the address is (a public name cannot be
// used in a test).
func hookEnv(t *testing.T, e *env, status int) *httptest.Server {
	t.Helper()
	h := &github.WebhookHandler{
		Verifier: func() (github.WebhookVerifier, error) {
			b, err := e.secrets.Get(context.Background(), github.SecretKey(e.apps[0].ID, github.SecretWebhookSecret))
			if err != nil {
				return nil, err
			}
			return github.NewWebhookVerifier(b), nil
		},
		FirstDelivery: func(string, string) (bool, error) { return true, nil },
		Events:        nopEvents{},
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	c := srv.Client()
	tr := c.Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, n, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, n, srv.Listener.Addr().String())
	}
	c.Transport = tr
	e.svc.cfg.SelfTestHTTP = c
	return srv
}

func storedHook(e *env) string {
	b, err := e.secrets.Get(context.Background(), github.SecretKey(e.apps[0].ID, github.SecretWebhookSecret))
	if err != nil {
		return ""
	}
	return string(b)
}

func TestSetWebhookMakesSecretTestsAndSetsTheHook(t *testing.T) { // R5.51
	e := newEnv(t)
	owner := e.ownerSignIn(1, "alice").Principal
	hookEnv(t, e, 0)
	var saved string
	e.svc.cfg.SaveWebhookAddress = func(_ context.Context, a string) error { saved = a; return nil }
	e.svc.cfg.LoadWebhookAddress = func(context.Context) (string, error) { return saved, nil }
	st, err := e.svc.SetWebhook(context.Background(), owner, " https://example.com/ ")
	if err != nil {
		t.Fatal(err)
	}
	sec := storedHook(e)
	if len(sec) != 64 {
		t.Fatalf("a 32-byte secret in hex is stored, got %d characters", len(sec))
	}
	if e.fake.HookPatches != 1 || e.fake.Hook["url"] != "https://example.com/webhooks/github" ||
		e.fake.Hook["secret"] != sec || e.fake.Hook["content_type"] != "json" {
		t.Fatalf("GitHub got %v (%d patches)", e.fake.Hook, e.fake.HookPatches)
	}
	if st.State != WebhookOn || st.Address != "https://example.com" || saved != "https://example.com" {
		t.Fatalf("status %+v, saved %q", st, saved)
	}
	if !hasAudit(e.store, "app.webhook_on") {
		t.Fatal("not audited")
	}
	for _, a := range e.store.AuditEvents() {
		if strings.Contains(a.Detail+a.Subject, sec) {
			t.Fatal("the secret is in the audit log")
		}
	}
	// A second save makes a new secret.
	if _, err := e.svc.SetWebhook(context.Background(), owner, "https://example.com"); err != nil {
		t.Fatal(err)
	}
	if storedHook(e) == sec || e.fake.Hook["secret"] != storedHook(e) {
		t.Fatal("the secret did not change with the save, or GitHub and the store differ")
	}
}

func TestSetWebhookSelfTestFailureKeepsTheOldState(t *testing.T) { // R5.51
	for name, c := range map[string]struct {
		status int
		want   string
	}{"not this controller": {401, "not this controller"}, "nothing there": {404, "404"}, "error": {502, "502"}} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			owner := e.ownerSignIn(1, "alice").Principal
			before := storedHook(e)
			hookEnv(t, e, c.status)
			_, err := e.svc.SetWebhook(context.Background(), owner, "https://example.com")
			var inv *InvalidError
			if !errors.As(err, &inv) || !strings.Contains(inv.Msg, c.want) {
				t.Fatalf("want a refusal with %q, got %v", c.want, err)
			}
			if storedHook(e) != before {
				t.Fatal("the old secret was not put back")
			}
			if e.fake.HookPatches != 0 {
				t.Fatal("GitHub was changed although the test failed")
			}
			if !hasAudit(e.store, "app.webhook_failed") {
				t.Fatal("failure not audited")
			}
		})
	}
}

func TestSetWebhookUntrustedCertificate(t *testing.T) { // R5.51
	e := newEnv(t)
	owner := e.ownerSignIn(1, "alice").Principal
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	// The default roots do not know the test server's certificate: the same
	// failure a self-signed certificate gives.
	c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, n, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, n, srv.Listener.Addr().String())
	}}}
	e.svc.cfg.SelfTestHTTP = c
	_, err := e.svc.SetWebhook(context.Background(), owner, "https://example.com")
	var inv *InvalidError
	if !errors.As(err, &inv) || !strings.Contains(inv.Msg, "certificate") || !strings.Contains(inv.Msg, "self-signed") {
		t.Fatalf("want the certificate named, got %v", err)
	}
	if e.fake.HookPatches != 0 {
		t.Fatal("GitHub was changed")
	}
}

func TestSetWebhookGitHubRefusesPutsOldSecretBack(t *testing.T) { // R5.51
	e := newEnv(t)
	owner := e.ownerSignIn(1, "alice").Principal
	before := storedHook(e)
	hookEnv(t, e, 0)
	k2, _ := newPEM(t)
	e.fake.AppPublicKey = &k2.PublicKey // GitHub no longer accepts the App's key
	if _, err := e.svc.SetWebhook(context.Background(), owner, "https://example.com"); err == nil {
		t.Fatal("GitHub refused the call, so Save must fail")
	}
	if storedHook(e) != before {
		t.Fatal("the old secret was not put back")
	}
}

func TestValidateWebhookAddress(t *testing.T) { // R5.51
	for in, want := range map[string]string{
		"https://hooks.example.com":        "https://hooks.example.com",
		"https://hooks.example.com/":       "https://hooks.example.com",
		"https://x.example.com:8443/crsp/": "https://x.example.com:8443/crsp",
	} {
		got, err := ValidateWebhookAddress(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v", in, got, err)
		}
	}
	for _, in := range []string{
		"", "hooks.example.com", "http://hooks.example.com", "ftp://x.example.com",
		"https://localhost", "https://127.0.0.1", "https://[::1]", "https://192.168.1.20", "https://10.0.0.5:8443",
		"https://crsp", "https://crsp.local", "https://nas.lan", "https://169.254.1.1",
		"https://user:pw@hooks.example.com", "https://hooks.example.com/?a=b", "https://hooks.example.com/#x",
	} {
		_, err := ValidateWebhookAddress(in)
		var inv *InvalidError
		if !errors.As(err, &inv) {
			t.Errorf("%q: want a refusal, got %v", in, err)
		}
	}
}

func TestSelfTestClientRefusesLocalAddresses(t *testing.T) { // R5.51
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	r, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil) // 127.0.0.1
	res, err := NewSelfTestClient().Do(r)
	if res != nil {
		_ = res.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "inside this network") {
		t.Fatalf("want the dial refused, got %v", err)
	}
}

func TestSetWebhookNeedsTheOwner(t *testing.T) { // R5.51
	e := newEnv(t)
	e.ownerSignIn(1, "alice")
	hookEnv(t, e, 0)
	for _, role := range []Role{RoleViewer, RoleAdmin} {
		p := Principal{UserID: 9, Login: "bob", Role: role}
		if _, err := e.svc.SetWebhook(context.Background(), p, "https://example.com"); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s: %v", role, err)
		}
		if _, err := e.svc.ClearWebhook(context.Background(), p); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s: %v", role, err)
		}
	}
	if e.fake.HookPatches != 0 {
		t.Fatal("GitHub was changed")
	}
}

func TestClearWebhookReturnsToPolling(t *testing.T) { // R5.51
	e := newEnv(t)
	owner := e.ownerSignIn(1, "alice").Principal
	hookEnv(t, e, 0)
	var saved = "x"
	e.svc.cfg.SaveWebhookAddress = func(_ context.Context, a string) error { saved = a; return nil }
	if _, err := e.svc.SetWebhook(context.Background(), owner, "https://example.com"); err != nil {
		t.Fatal(err)
	}
	st, err := e.svc.ClearWebhook(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != WebhookOff || saved != "" || storedHook(e) != "" {
		t.Fatalf("state %+v, address %q, secret %q", st, saved, storedHook(e))
	}
	if e.fake.Hook["url"] != "" || e.fake.Hook["secret"] != "" {
		t.Fatalf("GitHub still has %v", e.fake.Hook)
	}
	got, _ := e.svc.WebhookStatus(context.Background())
	if got.State != WebhookOff || !hasAudit(e.store, "app.webhook_off") {
		t.Fatalf("%+v", got)
	}
}

func TestWebhookStatusStatesAndEventsLink(t *testing.T) { // R5.51
	e := newEnv(t)
	owner := e.ownerSignIn(1, "alice").Principal
	hookEnv(t, e, 0)
	_, _ = e.svc.ClearWebhook(context.Background(), owner)
	if st, _ := e.svc.WebhookStatus(context.Background()); st.State != WebhookOff {
		t.Fatalf("after clear: %+v", st)
	}
	at := e.clock()
	e.svc.cfg.HookHealth = hookHealth{at: at, res: "ok"}
	if _, err := e.svc.SetWebhook(context.Background(), owner, "https://example.com"); err != nil {
		t.Fatal(err)
	}
	st, _ := e.svc.WebhookStatus(context.Background())
	if st.State != WebhookOn || !st.LastDeliveryAt.Equal(at) || st.LastResult != "ok" || st.EventsLink != "" {
		t.Fatalf("on: %+v", st)
	}
	e.svc.cfg.HookHealth = hookHealth{at: at, res: "missed"}
	if st, _ = e.svc.WebhookStatus(context.Background()); st.State != WebhookFailing {
		t.Fatalf("failing: %+v", st)
	}
	// The App does not subscribe to workflow_job: the owner gets one link.
	e.fake.AppEvents = []string{"repository"}
	st, _ = e.svc.WebhookStatus(context.Background())
	if !strings.HasSuffix(st.EventsLink, "/permissions") || !strings.HasPrefix(st.EventsLink, "https://github.com/") {
		t.Fatalf("events link %q (app url %q)", st.EventsLink, e.apps[0].HTMLURL)
	}
}
