package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/secrets"
)

func TestManifestPermissionsExact(t *testing.T) { // R5.23
	m, err := BuildManifest(ManifestOptions{AppName: "crsp-home", ConsoleURLs: []string{devConsole}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"administration": "write", "actions": "read", "actions_variables": "write",
		"contents": "read", "metadata": "read", "organization_self_hosted_runners": "write",
	}
	if !reflect.DeepEqual(m.DefaultPermissions, want) {
		t.Fatalf("permissions must be exactly the R5.23 set:\n got %v\nwant %v", m.DefaultPermissions, want)
	}
	if len(m.DefaultEvents) != 0 {
		t.Fatalf("a local-only App subscribes to no events: %v", m.DefaultEvents)
	}
	if m.Public {
		t.Fatal("the App is private to its owner")
	}
}

func TestManifestContent(t *testing.T) { // R5.2, R5.4, R5.5
	m, err := BuildManifest(ManifestOptions{AppName: "crsp-home", ConsoleURLs: []string{devConsole}})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.CallbackURLs) != 1 || m.CallbackURLs[0] != "http://127.0.0.1:8460/auth/github/callback" {
		t.Fatalf("callbacks %v", m.CallbackURLs)
	}
	if m.RedirectURL != "http://127.0.0.1:8460/auth/github/app-created" || m.SetupURL == "" || m.URL == "" {
		t.Fatalf("%+v", m)
	}
	if m.HookAttributes != nil {
		t.Fatal("webhook must be off when every console address is loopback (R5.5)")
	}
	// GitHub refuses a manifest with a hook URL it cannot reach, even inactive.
	if strings.Contains(string(m.JSON()), "hook_attributes") || strings.Contains(string(m.JSON()), "127.0.0.1:8460/webhooks") {
		t.Fatal("a local-only manifest must not carry a hook")
	}
	if !strings.Contains(string(m.JSON()), `"default_permissions"`) {
		t.Fatal("json")
	}
}

func TestManifestWebhookOnlyWithPublicURL(t *testing.T) { // R5.5
	opts := ManifestOptions{AppName: "crsp-home", ConsoleURLs: []string{devConsole, "https://pool.lan:8460"}}
	m, _ := BuildManifest(opts)
	if m.HookAttributes != nil {
		t.Fatal("off without a webhook URL")
	}
	opts.WebhookURL = "https://hooks.example.org/crsp"
	m, err := BuildManifest(opts)
	if err != nil || m.HookAttributes == nil || !m.HookAttributes.Active || m.HookAttributes.URL != opts.WebhookURL {
		t.Fatalf("%+v %v", m.HookAttributes, err)
	}
	if !reflect.DeepEqual(m.DefaultEvents, []string{"workflow_job", "repository"}) {
		t.Fatalf("events with a public webhook %v", m.DefaultEvents)
	}
	opts.WebhookURL = "http://hooks.example.org/crsp"
	if _, err := BuildManifest(opts); err == nil {
		t.Fatal("http webhook must be refused")
	}
	// All loopback: the URL is ignored and the hook stays off.
	m, _ = BuildManifest(ManifestOptions{AppName: "crsp-home", ConsoleURLs: []string{devConsole}, WebhookURL: "https://hooks.example.org/x"})
	if m.HookAttributes != nil {
		t.Fatal("local-only controller keeps webhooks off")
	}
}

func TestManifestNameChecked(t *testing.T) {
	for _, bad := range []string{"", "-x", strings.Repeat("a", 40), "a<b>"} {
		if _, err := BuildManifest(ManifestOptions{AppName: bad, ConsoleURLs: []string{devConsole}}); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestStartManifestPostURL(t *testing.T) { // R5.2
	e := newEnv(t)
	form, err := e.svc.StartManifest(context.Background(), ManifestOptions{AppName: "crsp", ConsoleURLs: []string{devConsole}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(form.PostURL, e.fake.URL+"/settings/apps/new?state=") {
		t.Fatalf("personal: %s", form.PostURL)
	}
	form, _ = e.svc.StartManifest(context.Background(), ManifestOptions{AppName: "crsp", ConsoleURLs: []string{devConsole}, Organization: "acme corp"})
	if !strings.Contains(form.PostURL, "/organizations/acme%20corp/settings/apps/new?state=") {
		t.Fatalf("organization: %s", form.PostURL)
	}
	u, _ := url.Parse(form.PostURL)
	if u.Query().Get("state") != form.State || form.State == "" {
		t.Fatal("state must be in the URL")
	}
}

func TestManifestStateExpiresAndIsSingleUse(t *testing.T) { // R5.2
	e := newEnv(t)
	ctx := context.Background()
	opts := ManifestOptions{AppName: "crsp", ConsoleURLs: []string{devConsole}}
	form, _ := e.svc.StartManifest(ctx, opts)
	e.advance(StateTTL + time.Second)
	e.fake.IssueManifestCode("c1")
	if _, err := e.svc.CompleteManifest(ctx, "c1", form.State); !errors.Is(err, ErrBadState) {
		t.Fatalf("expired state: %v", err)
	}
	form, _ = e.svc.StartManifest(ctx, opts)
	if _, err := e.svc.CompleteManifest(ctx, "c1", "forged"); !errors.Is(err, ErrBadState) {
		t.Fatalf("forged state: %v", err)
	}
	if _, err := e.svc.CompleteManifest(ctx, "no-such-code", form.State); err == nil {
		t.Fatal("unknown code must fail")
	}
	if _, err := e.svc.CompleteManifest(ctx, "c1", form.State); !errors.Is(err, ErrBadState) {
		t.Fatalf("a state used (even for a failed attempt) is gone: %v", err)
	}
}

func TestConversionOnceSecretsToStore(t *testing.T) { // R5.3
	e := newEnv(t)
	ctx := context.Background()
	opts := ManifestOptions{AppName: "crsp", ConsoleURLs: []string{devConsole}}
	e.fake.IssueManifestCode("once")
	form, _ := e.svc.StartManifest(ctx, opts)
	rec, err := e.svc.CompleteManifest(ctx, "once", form.State)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != e.fake.AppID || rec.Slug == "" || rec.ClientID == "" || rec.OwnerLogin == "" || rec.HTMLURL == "" {
		t.Fatalf("public facts: %+v", rec)
	}
	if len(e.apps) != 1 {
		t.Fatal("OnApp must run so the GitHub client uses the App")
	}
	for _, name := range []string{"private-key", "client-secret", "webhook-secret"} {
		v, err := e.secrets.Get(ctx, secretKey(rec.ID, name))
		if err != nil || len(v) == 0 {
			t.Fatalf("%s must be in the OS store: %v", name, err)
		}
	}
	// A second use of the same code, with a fresh state, is refused.
	if _, err := e.svc.StartManifest(ctx, opts); !errors.Is(err, ErrSetupDone) {
		t.Fatalf("an App exists: %v", err)
	}
}

func TestConversionCodeNotReused(t *testing.T) { // R5.3
	e := newEnv(t)
	ctx := context.Background()
	opts := ManifestOptions{AppName: "crsp", ConsoleURLs: []string{devConsole}}
	f1, _ := e.svc.StartManifest(ctx, opts)
	f2, _ := e.svc.StartManifest(ctx, opts)
	// A conversion that fails on GitHub's side still uses the code up.
	if _, err := e.svc.CompleteManifest(ctx, "unknown", f1.State); err == nil {
		t.Fatal("unknown code must fail")
	}
	e.fake.IssueManifestCode("unknown")
	if _, err := e.svc.CompleteManifest(ctx, "unknown", f2.State); !errors.Is(err, ErrBadCode) {
		t.Fatalf("a code already tried is refused: %v", err)
	}
	if !hasAudit(e.store, "setup.app_failed") {
		t.Fatal("failure is audited")
	}
}

func TestDatabaseHoldsNoAppSecret(t *testing.T) { // R6.1
	e := newEnv(t)
	e.ownerSignIn(1, "alice")
	var dump []string
	dump = append(dump, e.store.Dump()...)
	app, _ := e.store.GetApp(context.Background())
	dump = append(dump, app.Slug, app.ClientID, app.OwnerLogin, app.HTMLURL)
	noSecretIn(t, dump, e.fake.ClientSecret, "FAKE-WEBHOOK-SECRET-NOT-REAL", "BEGIN RSA PRIVATE KEY", e.fake.PEM)
	// The store must hold the App secrets, the DB must not.
	if len(e.secrets.Keys()) != 3 {
		t.Fatalf("secret keys: %v", e.secrets.Keys())
	}
}

func TestSecretStoreDownStopsSetup(t *testing.T) { // section 8, R6.1
	e := newEnv(t)
	ctx := context.Background()
	e.fake.IssueManifestCode("c")
	form, _ := e.svc.StartManifest(ctx, ManifestOptions{AppName: "crsp", ConsoleURLs: []string{devConsole}})
	e.secrets.Down = true
	if _, err := e.svc.CompleteManifest(ctx, "c", form.State); err == nil {
		t.Fatal("must fail closed")
	}
	if _, err := e.store.GetApp(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no App record may exist without its secrets: %v", err)
	}
}

// refuseEmpty is a secret store that refuses an empty value the way the file
// store does on Windows, where DPAPI will not protect nothing.
type refuseEmpty struct{ *secrets.Memory }

func (r refuseEmpty) Put(ctx context.Context, k secrets.Key, v []byte) error {
	if len(v) == 0 {
		return fmt.Errorf("%w: empty secret", secrets.ErrUnavailable)
	}
	return r.Memory.Put(ctx, k, v)
}

func TestAppWithoutHookCompletes(t *testing.T) { // R5.3, R5.5
	e := newEnv(t)
	ctx := context.Background()
	mem := secrets.NewMemory()
	e.svc.cfg.Secrets = refuseEmpty{mem}
	e.fake.NoWebhookSecret = true
	e.fake.IssueManifestCode("nohook")
	form, _ := e.svc.StartManifest(ctx, ManifestOptions{AppName: "crsp", ConsoleURLs: []string{devConsole}})
	rec, err := e.svc.CompleteManifest(ctx, "nohook", form.State)
	if err != nil {
		t.Fatalf("an App with no hook must complete: %v", err)
	}
	for _, name := range []string{"private-key", "client-secret"} {
		if v, err := mem.Get(ctx, secretKey(rec.ID, name)); err != nil || len(v) == 0 {
			t.Fatalf("%s must be kept: %v", name, err)
		}
	}
	if _, err := mem.Get(ctx, secretKey(rec.ID, "webhook-secret")); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("no empty webhook secret is stored: %v", err)
	}
}

type failPut struct{ *secrets.Memory }

func (failPut) Put(context.Context, secrets.Key, []byte) error { return secrets.ErrUnavailable }

func TestFailureAfterConversionKeepsAppURL(t *testing.T) { // R5.3
	e := newEnv(t)
	ctx := context.Background()
	e.svc.cfg.Secrets = failPut{secrets.NewMemory()}
	e.fake.IssueManifestCode("half")
	form, _ := e.svc.StartManifest(ctx, ManifestOptions{AppName: "crsp", ConsoleURLs: []string{devConsole}})
	_, err := e.svc.CompleteManifest(ctx, "half", form.State)
	var made *AppCreatedError
	if !errors.As(err, &made) || !errors.Is(err, secrets.ErrUnavailable) {
		t.Fatalf("want AppCreatedError wrapping the store error: %v", err)
	}
	if made.AppURL != "https://github.com/apps/crsp-dev-fake" {
		t.Fatalf("app url: %q", made.AppURL)
	}
	if !hasAudit(e.store, "setup.app_incomplete") {
		t.Fatal("a half-made App is audited")
	}
	if _, err := e.store.GetApp(ctx); err == nil {
		t.Fatal("nothing is kept")
	}
}

func TestSafeAppURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/apps/x":     "https://github.com/apps/x",
		"http://github.com/apps/x":      "",
		"https://evil.example/apps/x":   "",
		"https://github.com.evil.io/a":  "",
		"https://u:p@github.com/apps/x": "",
		"javascript:alert(1)":           "",
		"":                              "",
	} {
		if got := SafeAppURL(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}
