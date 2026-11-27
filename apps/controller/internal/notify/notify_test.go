package notify

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

const (
	webhookSecret = "whsec-0123456789abcdef-SIGNING"
	smtpPassword  = "pw-Sup3rS3cret-mail"
)

var (
	owner  = Caller{Login: "octo", Admin: true, Actor: audit.Actor{Login: "octo", ID: "1", Source: audit.SourceWeb}}
	alice  = Caller{Login: "alice", Actor: audit.Actor{Login: "alice", ID: "2", Source: audit.SourceWeb}}
	bobber = Caller{Login: "bob", Actor: audit.Actor{Login: "bob", ID: "3", Source: audit.SourceWeb}}
)

type env struct {
	svc  *Service
	db   *store.Store
	sec  *secrets.Memory
	logs *bytes.Buffer
	now  *time.Time
}

func newEnv(t *testing.T, tweak func(*Config)) *env {
	t.Helper()
	db, err := store.OpenForTests(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	e := &env{db: db, sec: secrets.NewMemory(), logs: &bytes.Buffer{}}
	start := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	e.now = &start
	cfg := Config{
		DB: db, Secrets: e.sec, AllowLoopback: true,
		Logger: slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Now:    func() time.Time { return *e.now },
	}
	if tweak != nil {
		tweak(&cfg)
	}
	e.svc = New(cfg)
	return e
}

func webhookInput(url string) Input {
	return Input{Kind: KindWebhook, Name: "Team chat", Events: []string{"job.waiting", "machine.offline"}, MinSeverity: "warning", URL: url, Secret: webhookSecret}
}

func emailInput(port int) Input {
	return Input{Kind: KindEmail, Name: "My mail", Events: []string{"machine.offline"}, Address: "alice@example.com",
		SMTP: &SMTPInput{Host: "127.0.0.1", Port: port, Username: "mailer", From: "crsp@example.com", Password: smtpPassword}}
}

func mustInvalid(t *testing.T, err error, want string) {
	t.Helper()
	var inv *InvalidError
	if !errors.As(err, &inv) {
		t.Fatalf("want an InvalidError containing %q, got %v", want, err)
	}
	if !strings.Contains(inv.Error(), want) {
		t.Fatalf("problems %q do not contain %q", inv.Error(), want)
	}
}

func TestCreateWebhookKeepsSecretOnlyInSecretStore(t *testing.T) { // R5.22, R6.2
	e := newEnv(t, nil)
	ctx := context.Background()
	c, err := e.svc.Create(ctx, owner, webhookInput("https://hooks.example.com/crsp/T000/B000/xyz"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Scope != ScopePool || !c.SecretSet || c.Kind != KindWebhook || !c.Enabled {
		t.Fatalf("%+v", c)
	}
	if got, err := e.sec.Get(ctx, signingKey(c.ID)); err != nil || string(got) != webhookSecret {
		t.Fatalf("the secret store must hold the secret: %q %v", got, err)
	}
	// Nothing in the channel, the list, the stored row or the audit log carries the secret.
	list, _ := e.svc.List(ctx, owner)
	raw, _ := json.Marshal(list)
	row, _ := e.db.GetNotificationChannel(ctx, c.ID)
	rowJSON, _ := json.Marshal(row)
	rows, _ := e.db.ListAudit(ctx, store.AuditFilter{})
	auditJSON, _ := json.Marshal(rows)
	for name, text := range map[string]string{"list": string(raw), "row": string(rowJSON), "audit": string(auditJSON), "logs": e.logs.String()} {
		if strings.Contains(text, webhookSecret) {
			t.Errorf("%s contains the secret: %s", name, text)
		}
	}
	if len(rows) != 1 || rows[0].Action != "notification.channel.create" || rows[0].Actor != "octo" || rows[0].TargetID != c.ID {
		t.Fatalf("audit: %+v", rows)
	}
}

func TestCreateEmailKeepsPasswordOnlyInSecretStore(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	c, err := e.svc.Create(ctx, alice, emailInput(2525))
	if err != nil {
		t.Fatal(err)
	}
	if c.Scope != ScopePersonal || c.SMTP == nil || !c.SMTP.HasPassword || c.SecretSet {
		t.Fatalf("%+v %+v", c, c.SMTP)
	}
	if got, err := e.sec.Get(ctx, passwordKey(c.ID)); err != nil || string(got) != smtpPassword {
		t.Fatalf("%q %v", got, err)
	}
	raw, _ := json.Marshal(c)
	rows, _ := e.db.ListAudit(ctx, store.AuditFilter{})
	auditJSON, _ := json.Marshal(rows)
	if strings.Contains(string(raw)+string(auditJSON)+e.logs.String(), smtpPassword) {
		t.Fatal("the password leaked")
	}
}

type validationCase struct {
	name string
	who  Caller
	in   Input
	want string
}

// runValidation creates each case on a loopback-refusing env and checks the refusal leaves nothing behind.
func runValidation(t *testing.T, cases []validationCase) {
	t.Helper()
	e := newEnv(t, func(c *Config) { c.AllowLoopback = false })
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.svc.Create(ctx, tc.who, tc.in)
			mustInvalid(t, err, tc.want)
		})
	}
	if rows, _ := e.db.ListNotificationChannels(ctx); len(rows) != 0 {
		t.Fatalf("a refused request left %d channels", len(rows))
	}
	if len(e.sec.Keys()) != 0 {
		t.Fatalf("a refused request left secrets: %v", e.sec.Keys())
	}
}

func TestValidationWebhook(t *testing.T) { // R5.19: webhook address, secret, events, name
	runValidation(t, []validationCase{
		{"http webhook", owner, webhookInput("http://hooks.example.com/x"), "must use https"},
		{"credentials in the URL", owner, webhookInput("https://u:p@hooks.example.com/x"), "must not carry a user name"},
		{"no host", owner, webhookInput("https:///x"), "https URL"},
		{"loopback literal", owner, webhookInput("https://127.0.0.1:8460/api"), "points to this machine"},
		{"localhost", owner, webhookInput("https://localhost/x"), "points to this machine"},
		{"metadata address", owner, webhookInput("https://169.254.169.254/latest"), "link-local"},
		{"unspecified", owner, webhookInput("https://0.0.0.0/"), "points to this machine"},
		{"ipv6 loopback", owner, webhookInput("https://[::1]/"), "points to this machine"},
		{"short secret", owner, func() Input { i := webhookInput("https://hooks.example.com/x"); i.Secret = "short"; return i }(), "signing secret"},
		{"no events", owner, func() Input { i := webhookInput("https://hooks.example.com/x"); i.Events = nil; return i }(), "at least one event"},
		{"unknown event", owner, func() Input { i := webhookInput("https://hooks.example.com/x"); i.Events = []string{"nope"}; return i }(), "Unknown event"},
		{"bad severity", owner, func() Input { i := webhookInput("https://hooks.example.com/x"); i.MinSeverity = "loud"; return i }(), "lowest severity"},
		{"no name", owner, func() Input { i := webhookInput("https://hooks.example.com/x"); i.Name = " "; return i }(), "Write a name"},
	})
}

func TestValidationEmail(t *testing.T) { // R5.19: email address, mail server, kind
	runValidation(t, []validationCase{
		{"bad address", alice, func() Input { i := emailInput(25); i.Address = "not-an-address"; return i }(), "email address"},
		{"display name", alice, func() Input { i := emailInput(25); i.Address = "Alice <a@example.com>"; return i }(), "email address"},
		{"header injection", alice, func() Input { i := emailInput(25); i.Address = "a@example.com\r\nBcc: x@y.z"; return i }(), "email address"},
		{"no mail server", alice, func() Input { i := emailInput(25); i.SMTP = nil; return i }(), "needs a mail server"},
		{"bad port", alice, func() Input { i := emailInput(25); i.SMTP.Port = 70000; return i }(), "port"},
		{"smtp loopback", alice, emailInput(25), "points to this machine"},
		{"smtp host with a path", alice, func() Input { i := emailInput(25); i.SMTP.Host = "smtp.example.com/x"; return i }(), "host name"},
		{"password without user", alice, func() Input { i := emailInput(25); i.SMTP.Host = "smtp.example.com"; i.SMTP.Username = ""; return i }(), "user name"},
		{"unknown kind", owner, Input{Kind: "sms", Name: "x", Events: []string{"disk.low"}}, "email, webhook or desktop"},
	})
}

func TestScopes(t *testing.T) { // R5.7, R5.9
	e := newEnv(t, nil)
	ctx := context.Background()
	if _, err := e.svc.Create(ctx, alice, webhookInput("https://hooks.example.com/x")); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a viewer adds no webhook: %v", err)
	}
	hook, err := e.svc.Create(ctx, owner, webhookInput("https://hooks.example.com/x"))
	if err != nil {
		t.Fatal(err)
	}
	mine, err := e.svc.Create(ctx, alice, Input{Kind: KindDesktop, Name: "My desktop", Events: []string{"disk.low"}})
	if err != nil {
		t.Fatal(err)
	}
	if mine.Scope != ScopePersonal {
		t.Fatal(mine.Scope)
	}
	// Alice sees her own channel only; a webhook URL can hold a token, so a viewer does not see it.
	if l, _ := e.svc.List(ctx, alice); len(l) != 1 || l[0].ID != mine.ID {
		t.Fatalf("alice: %+v", l)
	}
	if l, _ := e.svc.List(ctx, bobber); len(l) != 0 {
		t.Fatalf("bob: %+v", l)
	}
	if l, _ := e.svc.List(ctx, owner); len(l) != 1 || l[0].ID != hook.ID {
		t.Fatalf("the owner sees webhooks and only their own personal channels: %+v", l)
	}
	name := "x"
	if _, err := e.svc.Update(ctx, alice, hook.ID, Patch{Name: &name}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("%v", err)
	}
	if err := e.svc.Delete(ctx, alice, hook.ID, true); !errors.Is(err, ErrForbidden) {
		t.Fatalf("%v", err)
	}
	if _, err := e.svc.Test(ctx, alice, hook.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("%v", err)
	}
	// Another user's personal channel does not exist for anyone else, not even the owner.
	if _, err := e.svc.Update(ctx, bobber, mine.ID, Patch{Name: &name}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if _, err := e.svc.Update(ctx, owner, mine.ID, Patch{Name: &name}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if _, err := e.svc.Test(ctx, owner, "nch_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
}

func TestUpdateSecretSemantics(t *testing.T) { // contract: left out stays, empty string removes
	e := newEnv(t, nil)
	ctx := context.Background()
	c, err := e.svc.Create(ctx, owner, webhookInput("https://hooks.example.com/x"))
	if err != nil {
		t.Fatal(err)
	}
	name := "Renamed"
	c2, err := e.svc.Update(ctx, owner, c.ID, Patch{Name: &name})
	if err != nil || c2.Name != "Renamed" || !c2.SecretSet {
		t.Fatalf("%+v %v", c2, err)
	}
	if got, _ := e.sec.Get(ctx, signingKey(c.ID)); string(got) != webhookSecret {
		t.Fatal("a secret left out must stay")
	}
	next := "a-brand-new-signing-secret"
	if _, err := e.svc.Update(ctx, owner, c.ID, Patch{Secret: &next}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.sec.Get(ctx, signingKey(c.ID)); string(got) != next {
		t.Fatal("a new secret replaces the old one")
	}
	empty := ""
	c3, err := e.svc.Update(ctx, owner, c.ID, Patch{Secret: &empty})
	if err != nil || c3.SecretSet {
		t.Fatalf("%+v %v", c3, err)
	}
	if _, err := e.sec.Get(ctx, signingKey(c.ID)); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("an empty secret removes it: %v", err)
	}
	short := "short"
	if _, err := e.svc.Update(ctx, owner, c.ID, Patch{Secret: &short}); err == nil {
		t.Fatal("a short secret is refused")
	}
	// The audit rows name the fields, never the values.
	rows, _ := e.db.ListAudit(ctx, store.AuditFilter{Action: "notification.channel.update"})
	if len(rows) != 3 {
		t.Fatalf("%d update rows", len(rows))
	}
	for _, r := range rows {
		if strings.Contains(r.Detail, next) || strings.Contains(r.Detail, webhookSecret) {
			t.Fatalf("secret in audit: %s", r.Detail)
		}
	}
}

func TestUpdateEmailPasswordAndKindRules(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	c, _ := e.svc.Create(ctx, alice, emailInput(2525))
	empty := ""
	c2, err := e.svc.Update(ctx, alice, c.ID, Patch{SMTP: &SMTPPatch{Password: &empty}})
	if err != nil || c2.SMTP.HasPassword {
		t.Fatalf("%+v %v", c2, err)
	}
	if _, err := e.sec.Get(ctx, passwordKey(c.ID)); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("password must be removed")
	}
	u := "https://hooks.example.com/x"
	if _, err := e.svc.Update(ctx, alice, c.ID, Patch{URL: &u}); err == nil {
		t.Fatal("an email channel has no URL")
	}
	port := 587
	c3, err := e.svc.Update(ctx, alice, c.ID, Patch{SMTP: &SMTPPatch{Port: &port}})
	if err != nil || c3.SMTP.Port != 587 || c3.SMTP.Host != "127.0.0.1" {
		t.Fatalf("%+v %v", c3, err)
	}
	evs := []string{}
	if _, err := e.svc.Update(ctx, alice, c.ID, Patch{Events: &evs}); err == nil {
		t.Fatal("at least one event")
	}
}

func TestDeleteNeedsConfirmAndRemovesSecrets(t *testing.T) { // UX-62, R6.2
	e := newEnv(t, nil)
	ctx := context.Background()
	c, _ := e.svc.Create(ctx, owner, webhookInput("https://hooks.example.com/x"))
	err := e.svc.Delete(ctx, owner, c.ID, false)
	var nc *NeedsConfirmation
	if !errors.As(err, &nc) || nc.Name != "Team chat" || len(nc.Events) != 2 {
		t.Fatalf("%v", err)
	}
	if l, _ := e.svc.List(ctx, owner); len(l) != 1 {
		t.Fatal("nothing is removed without a yes")
	}
	if err := e.svc.Delete(ctx, owner, c.ID, true); err != nil {
		t.Fatal(err)
	}
	if l, _ := e.svc.List(ctx, owner); len(l) != 0 {
		t.Fatal("removed")
	}
	if len(e.sec.Keys()) != 0 {
		t.Fatalf("secrets left behind: %v", e.sec.Keys())
	}
	if err := e.svc.Delete(ctx, owner, c.ID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if rows, _ := e.db.ListAudit(ctx, store.AuditFilter{Action: "notification.channel.delete"}); len(rows) != 1 {
		t.Fatalf("%+v", rows)
	}
}

func TestSecretStoreDownFailsClosedWithoutLeak(t *testing.T) {
	e := newEnv(t, nil)
	e.sec.Down = true
	_, err := e.svc.Create(context.Background(), owner, webhookInput("https://hooks.example.com/x"))
	if !errors.Is(err, secrets.ErrUnavailable) {
		t.Fatalf("%v", err)
	}
	if rows, _ := e.db.ListNotificationChannels(context.Background()); len(rows) != 0 {
		t.Fatal("no channel without its secret")
	}
	if strings.Contains(e.logs.String(), webhookSecret) {
		t.Fatal("secret in logs")
	}
}

// ---------------------------------------------------------------- webhook test send

type hit struct {
	header http.Header
	body   []byte
	path   string
}

func hookServer(t *testing.T, status int, redirect string) (*httptest.Server, *[]hit, *sync.Mutex) {
	t.Helper()
	var mu sync.Mutex
	var hits []hit
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		hits = append(hits, hit{header: r.Header.Clone(), body: b, path: r.URL.Path})
		mu.Unlock()
		if redirect != "" && r.URL.Path == "/start" {
			http.Redirect(w, r, redirect, http.StatusFound)
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits, &mu
}

func trustOf(srv *httptest.Server) *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(srv.Certificate())
	return p
}

func TestWebhookTestIsSignedAndMarked(t *testing.T) { // R5.20, R5.21, R5.29, R5.30
	srv, hits, mu := hookServer(t, 204, "")
	e := newEnv(t, func(c *Config) { c.Trust = trustOf(srv) })
	ctx := context.Background()
	c, err := e.svc.Create(ctx, owner, webhookInput(srv.URL+"/crsp/T000/xyz-token"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.Test(ctx, owner, c.ID)
	if err != nil || !res.OK || !strings.Contains(res.Message, "HTTP 204") {
		t.Fatalf("%+v %v", res, err)
	}
	if strings.Contains(res.Message, "xyz-token") || strings.Contains(res.Message, "/crsp") {
		t.Fatalf("the URL path may hold a token and is never shown: %s", res.Message)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*hits) != 1 {
		t.Fatalf("%d deliveries", len(*hits))
	}
	h := (*hits)[0]
	ts := h.header.Get(HeaderTimestamp)
	if _, err := strconv.ParseInt(ts, 10, 64); err != nil {
		t.Fatalf("timestamp %q", ts)
	}
	if want := Sign([]byte(webhookSecret), ts, h.body); h.header.Get(HeaderSignature) != want || !strings.HasPrefix(want, "sha256=") {
		t.Fatalf("signature %q, want %q", h.header.Get(HeaderSignature), want)
	}
	var body map[string]any
	if err := json.Unmarshal(h.body, &body); err != nil {
		t.Fatal(err)
	}
	if body["test"] != true || body["type"] != "test" || body["schema_version"] != float64(1) || body["delivery_id"] != h.header.Get(HeaderDelivery) {
		t.Fatalf("%v", body)
	}
	if h.header.Get(HeaderTest) != "true" || h.header.Get("Content-Type") != "application/json" {
		t.Fatalf("%v", h.header)
	}
	if strings.Contains(string(h.body), webhookSecret) {
		t.Fatal("the body must not carry the secret")
	}
	// The result is recorded on the channel and in the audit log, with no secret.
	got, _ := e.db.GetNotificationChannel(ctx, c.ID)
	if got.LastTestOK == nil || !*got.LastTestOK || got.LastTestAt == nil {
		t.Fatalf("%+v", got)
	}
	rows, _ := e.db.ListAudit(ctx, store.AuditFilter{Action: "notification.channel.test"})
	if len(rows) != 1 || strings.Contains(rows[0].Detail, webhookSecret) {
		t.Fatalf("%+v", rows)
	}
	if strings.Contains(e.logs.String(), webhookSecret) {
		t.Fatal("secret in logs")
	}
}

func TestWebhookTestFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("server error", func(t *testing.T) {
		srv, _, _ := hookServer(t, 500, "")
		e := newEnv(t, func(c *Config) { c.Trust = trustOf(srv) })
		c, _ := e.svc.Create(ctx, owner, webhookInput(srv.URL+"/x"))
		res, err := e.svc.Test(ctx, owner, c.ID)
		if err != nil || res.OK || !strings.Contains(res.Message, "HTTP 500") {
			t.Fatalf("%+v %v", res, err)
		}
		got, _ := e.db.GetNotificationChannel(ctx, c.ID)
		if got.LastTestOK == nil || *got.LastTestOK {
			t.Fatal("a failed test is recorded as failed")
		}
	})
	t.Run("a redirect is not followed", func(t *testing.T) {
		target, targetHits, targetMu := hookServer(t, 200, "")
		srv, _, _ := hookServer(t, 200, target.URL+"/elsewhere")
		e := newEnv(t, func(c *Config) { c.Trust = trustOf(srv) })
		c, _ := e.svc.Create(ctx, owner, webhookInput(srv.URL+"/start"))
		res, _ := e.svc.Test(ctx, owner, c.ID)
		if res.OK || !strings.Contains(res.Message, "redirect") {
			t.Fatalf("%+v", res)
		}
		targetMu.Lock()
		defer targetMu.Unlock()
		if len(*targetHits) != 0 {
			t.Fatal("the redirect target was called")
		}
	})
	t.Run("untrusted certificate", func(t *testing.T) {
		srv, _, _ := hookServer(t, 200, "")
		e := newEnv(t, nil) // no trust added
		c, _ := e.svc.Create(ctx, owner, webhookInput(srv.URL+"/x"))
		res, _ := e.svc.Test(ctx, owner, c.ID)
		if res.OK || !strings.Contains(res.Message, "certificate") {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("nothing listens", func(t *testing.T) {
		srv, _, _ := hookServer(t, 200, "")
		url := srv.URL
		srv.Close()
		e := newEnv(t, nil)
		c, _ := e.svc.Create(ctx, owner, webhookInput(url+"/x"))
		res, _ := e.svc.Test(ctx, owner, c.ID)
		if res.OK || !strings.HasPrefix(res.Message, "Could not reach 127.0.0.1:") {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("the secret store cannot give the secret: nothing is sent unsigned", func(t *testing.T) { // R6.3
		srv, hits, mu := hookServer(t, 200, "")
		e := newEnv(t, func(c *Config) { c.Trust = trustOf(srv) })
		c, _ := e.svc.Create(ctx, owner, webhookInput(srv.URL+"/x"))
		e.sec.Down = true
		res, _ := e.svc.Test(ctx, owner, c.ID)
		if res.OK || !strings.Contains(res.Message, "never sends an unsigned") {
			t.Fatalf("%+v", res)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(*hits) != 0 {
			t.Fatal("an unsigned notice was sent")
		}
	})
}

func TestGuardRefusesLoopbackAtConnectTime(t *testing.T) {
	// A channel that was valid when saved (AllowLoopback) but is dialled by a service
	// that does not allow it: the connect-time guard refuses, whatever the name resolved to.
	srv, hits, mu := hookServer(t, 200, "")
	strict := newEnv(t, func(c *Config) { c.Trust = trustOf(srv) })
	allowed := newEnv(t, func(c *Config) { c.Trust = trustOf(srv) })
	ctx := context.Background()
	c, err := allowed.svc.Create(ctx, owner, webhookInput(srv.URL+"/x"))
	if err != nil {
		t.Fatal(err)
	}
	row, _ := allowed.db.GetNotificationChannel(ctx, c.ID)
	strict.svc.cfg.AllowLoopback = false
	_ = strict.sec.Put(ctx, signingKey(c.ID), []byte(webhookSecret))
	ok, msg := strict.svc.testWebhook(ctx, row)
	if ok || !strings.Contains(msg, "points to this machine") {
		t.Fatalf("%v %q", ok, msg)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*hits) != 0 {
		t.Fatal("the guarded dial reached the server")
	}
}

func TestGuardAddresses(t *testing.T) {
	for host, refused := range map[string]bool{
		"127.0.0.1": true, "::1": true, "0.0.0.0": true, "169.254.169.254": true, "fe80::1": true, "224.0.0.1": true,
		"::ffff:127.0.0.1": true, "localhost": true, "foo.localhost": true,
		"10.0.0.5": false, "192.168.1.10": false, "172.16.0.1": false, "hooks.example.lan": false, "8.8.8.8": false, "fd00::1": false,
	} {
		if got := hostRefused(host, false) != ""; got != refused {
			t.Errorf("%s: refused=%v, want %v", host, got, refused)
		}
	}
	if hostRefused("127.0.0.1", true) != "" {
		t.Error("the test switch allows loopback")
	}
}

// ---------------------------------------------------------------- email test send

func TestEmailTestStartTLSAndAuth(t *testing.T) { // R5.29
	hs := httptest.NewTLSServer(http.NotFoundHandler()) // only for its certificate
	t.Cleanup(hs.Close)
	smtpSrv := newFakeSMTP(t, hs.TLS, "mailer\x00"+smtpPassword)
	e := newEnv(t, func(c *Config) { c.Trust = trustOf(hs) })
	ctx := context.Background()
	c, err := e.svc.Create(ctx, alice, emailInput(smtpSrv.port()))
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.Test(ctx, alice, c.ID)
	if err != nil || !res.OK || !strings.Contains(res.Message, "alice@example.com") {
		t.Fatalf("%+v %v", res, err)
	}
	from, to, data, auth, secure := smtpSrv.got()
	if from != "crsp@example.com" || to != "alice@example.com" || !secure {
		t.Fatalf("from %q to %q secure %v", from, to, secure)
	}
	if auth != "|mailer|"+smtpPassword {
		t.Fatalf("auth %q", auth)
	}
	if !strings.Contains(data, "Subject: [crsp test]") || !strings.Contains(data, "X-CRP-Test: true") || strings.Contains(data, smtpPassword) {
		t.Fatalf("message:\n%s", data)
	}
	if strings.Contains(res.Message+e.logs.String(), smtpPassword) {
		t.Fatal("the password leaked")
	}
}

func TestEmailTestPlainWithoutAuth(t *testing.T) {
	smtpSrv := newFakeSMTP(t, nil, "")
	e := newEnv(t, nil)
	ctx := context.Background()
	in := emailInput(smtpSrv.port())
	in.SMTP.Username, in.SMTP.Password = "", ""
	c, err := e.svc.Create(ctx, alice, in)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := e.svc.Test(ctx, alice, c.ID)
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	if _, to, _, auth, secure := smtpSrv.got(); to != "alice@example.com" || auth != "" || secure {
		t.Fatalf("%q %q %v", to, auth, secure)
	}
}

func TestEmailTestFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("wrong password", func(t *testing.T) {
		hs := httptest.NewTLSServer(http.NotFoundHandler())
		t.Cleanup(hs.Close)
		smtpSrv := newFakeSMTP(t, hs.TLS, "mailer\x00another")
		e := newEnv(t, func(c *Config) { c.Trust = trustOf(hs) })
		c, _ := e.svc.Create(ctx, alice, emailInput(smtpSrv.port()))
		res, _ := e.svc.Test(ctx, alice, c.ID)
		if res.OK || !strings.Contains(res.Message, "sign-in") || !strings.Contains(res.Message, "535") || strings.Contains(res.Message, smtpPassword) {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("password is not sent without STARTTLS", func(t *testing.T) {
		smtpSrv := newFakeSMTP(t, nil, "mailer\x00"+smtpPassword)
		e := newEnv(t, func(c *Config) { c.AllowLoopback = true })
		in := emailInput(smtpSrv.port())
		in.SMTP.Host = "localhost"
		c, err := e.svc.Create(ctx, alice, in)
		if err != nil {
			t.Fatal(err)
		}
		// localhost is exempt from net/smtp's own check; a remote name is not.
		row, _ := e.db.GetNotificationChannel(ctx, c.ID)
		row.SMTPHost = "mail.example.lan"
		err = e.svc.sendMail(ctx, row, smtpPassword, []byte("x"))
		if err == nil {
			t.Fatal("must not send the password in clear")
		}
		if _, _, _, auth, _ := smtpSrv.got(); auth != "" {
			t.Fatalf("the password went out: %q", auth)
		}
	})
	t.Run("recipient refused", func(t *testing.T) {
		smtpSrv := newFakeSMTP(t, nil, "")
		smtpSrv.rejectTo = "alice@example.com"
		e := newEnv(t, nil)
		in := emailInput(smtpSrv.port())
		in.SMTP.Username, in.SMTP.Password = "", ""
		c, _ := e.svc.Create(ctx, alice, in)
		res, _ := e.svc.Test(ctx, alice, c.ID)
		if res.OK || !strings.Contains(res.Message, "alice@example.com") || !strings.Contains(res.Message, "550") {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("nothing listens", func(t *testing.T) {
		smtpSrv := newFakeSMTP(t, nil, "")
		port := smtpSrv.port()
		_ = smtpSrv.ln.Close()
		e := newEnv(t, nil)
		in := emailInput(port)
		in.SMTP.Username, in.SMTP.Password = "", ""
		c, _ := e.svc.Create(ctx, alice, in)
		res, _ := e.svc.Test(ctx, alice, c.ID)
		if res.OK || !strings.HasPrefix(res.Message, "Could not reach 127.0.0.1:") {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("the password cannot be read", func(t *testing.T) {
		smtpSrv := newFakeSMTP(t, nil, "")
		e := newEnv(t, nil)
		c, _ := e.svc.Create(ctx, alice, emailInput(smtpSrv.port()))
		e.sec.Down = true
		res, _ := e.svc.Test(ctx, alice, c.ID)
		if res.OK || !strings.Contains(res.Message, "secret store") {
			t.Fatalf("%+v", res)
		}
	})
}

func TestDesktopTestSaysWhatIsMissing(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	c, err := e.svc.Create(ctx, alice, Input{Kind: KindDesktop, Name: "Desk", Events: []string{"disk.low"}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.Test(ctx, alice, c.ID)
	if err != nil || res.OK || !strings.Contains(res.Message, "no way to send a notice to a tray") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestTestRateLimitPerChannelAndUser(t *testing.T) { // R5.31
	srv, _, _ := hookServer(t, 200, "")
	e := newEnv(t, func(c *Config) { c.Trust = trustOf(srv) })
	ctx := context.Background()
	c, _ := e.svc.Create(ctx, owner, webhookInput(srv.URL+"/x"))
	if _, err := e.svc.Test(ctx, owner, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Test(ctx, owner, c.ID); !errors.Is(err, ErrTooSoon) {
		t.Fatalf("%v", err)
	}
	other := Caller{Login: "admin2", Admin: true, Actor: audit.Actor{Login: "admin2"}}
	if _, err := e.svc.Test(ctx, other, c.ID); err != nil {
		t.Fatalf("the limit is per user: %v", err)
	}
	*e.now = e.now.Add(TestMinGap)
	if _, err := e.svc.Test(ctx, owner, c.ID); err != nil {
		t.Fatalf("after 10 s: %v", err)
	}
}

func TestSignKnownVector(t *testing.T) {
	// HMAC-SHA256("key", "1700000000.{}") computed independently.
	got := Sign([]byte("key"), "1700000000", []byte("{}"))
	if !strings.HasPrefix(got, "sha256=") || len(got) != len("sha256=")+64 {
		t.Fatal(got)
	}
	if got != Sign([]byte("key"), "1700000000", []byte("{}")) || got == Sign([]byte("key"), "1700000001", []byte("{}")) {
		t.Fatal("the signature covers the timestamp")
	}
}
