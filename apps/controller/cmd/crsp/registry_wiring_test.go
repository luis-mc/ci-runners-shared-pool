package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agentlink"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/config"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/events"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/builds"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

type nullSender struct{}

func (nullSender) Send(string, *agentv1.SessionResponse) error { return nil }

type fakeRegistry struct{ ev chan registry.Event }

func (fakeRegistry) Handler() http.Handler                                { return http.NotFoundHandler() }
func (fakeRegistry) SetReadOnly(bool)                                     {}
func (fakeRegistry) DeleteManifest(context.Context, string, string) error { return nil }
func (fakeRegistry) GC(context.Context, bool) (int64, error)              { return 0, nil }
func (f fakeRegistry) Events() <-chan registry.Event                      { return f.ev }

type fakeTokens struct{}

func (fakeTokens) Issue(context.Context, string, []registry.Scope) (registry.Token, error) {
	return registry.Token{}, nil
}
func (fakeTokens) Verify(context.Context, string) (string, []registry.Scope, error) {
	return "", nil, nil
}

func registryTestDeps(t *testing.T) (context.Context, context.CancelFunc, controllerDeps, *controller) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	dir := t.TempDir()
	db, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	t.Cleanup(cancel)
	d := controllerDeps{
		Cfg: config.Config{DataDir: dir, ListenAddr: "0.0.0.0:8460"}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		DB: db, PublicHost: "ctl.example", Link: agentlink.New(nullSender{}),
	}
	return ctx, cancel, d, &controller{}
}

// Without a registry core the program runs as before: the registry routes keep
// answering 501, agents are not offered the registry path, and only what needs
// no core is wired (build records, profiles, key choice).
func TestWireRegistryWithoutCore(t *testing.T) {
	old := openRegistryCore
	openRegistryCore = nil
	t.Cleanup(func() { openRegistryCore = old })
	if registryAvailable() {
		t.Fatal("no core: the handshake must not offer the registry")
	}
	ctx, _, d, c := registryTestDeps(t)
	w, err := wireRegistry(ctx, d, c, nil, nil, events.NewHub(events.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	if w.Core != nil || w.Keyer == nil || w.Stacks == nil || w.Builds == nil {
		t.Fatalf("wiring %+v", w)
	}
	o := c.Options
	if o.Builds == nil {
		t.Fatal("the build records are wired without a core")
	}
	if o.RegistryRead != nil || o.RegistryRetention != nil || o.RegistryReplace != nil {
		t.Fatal("the registry routes must stay 501 without a core")
	}
	if d.Link.Registry != nil {
		t.Fatal("no registry sink without a core: the agent link answers not-in-registry")
	}
	if d.Link.Builds == nil {
		t.Fatal("agents' build reports are kept")
	}
	ps, err := d.DB.Profiles().List(ctx)
	if err != nil || len(ps) == 0 {
		t.Fatalf("standard profiles: %v %v", ps, err)
	}
	c.Close()
}

// With a core the routes are served, the sink answers the agents, and the
// three promoted hooks are registered.
func TestWireRegistryWithCore(t *testing.T) {
	old := openRegistryCore
	t.Cleanup(func() { openRegistryCore = old })
	var hooks []registry.PromotedHook
	openRegistryCore = func(_ context.Context, rd registryCoreDeps) (*registryCore, error) {
		if rd.Images == nil || rd.Host != "ctl.example:8460" {
			t.Errorf("core deps %+v", rd)
		}
		return &registryCore{
			Registry: fakeRegistry{ev: make(chan registry.Event)}, Tokens: fakeTokens{},
			Layers:          layerSourceFunc(func(context.Context, string, string) ([]registry.Layer, error) { return nil, nil }),
			AddPromotedHook: func(h registry.PromotedHook) { hooks = append(hooks, h) },
		}, nil
	}
	if !registryAvailable() {
		t.Fatal("a core: the handshake offers the registry")
	}
	ctx, cancel, d, c := registryTestDeps(t)
	w, err := wireRegistry(ctx, d, c, nil, nil, events.NewHub(events.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	if w.Core == nil {
		t.Fatal("the core is kept")
	}
	o := c.Options
	if o.RegistryRead == nil || o.RegistryRetention == nil || o.RegistryReplace == nil || o.Builds == nil {
		t.Fatalf("options %+v", o)
	}
	if d.Link.Registry == nil {
		t.Fatal("the sink answers the agents")
	}
	if len(hooks) != 3 {
		t.Fatalf("%d promoted hooks, want layers, detection and scan", len(hooks))
	}
	var _ builds.Sink = w.Builds
	cancel()
	c.Close()
}

type layerSourceFunc func(ctx context.Context, name, digest string) ([]registry.Layer, error)

func (f layerSourceFunc) Layers(ctx context.Context, name, digest string) ([]registry.Layer, error) {
	return f(ctx, name, digest)
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// R5.1, R5.2, R5.3: the real core mounts under /v2/ on a private address,
// answers nothing without a token, and is not served on a wildcard or public
// address.
func TestOpenCore(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	sec := secrets.NewMemory()
	deps := func(addr string) registryCoreDeps {
		return registryCoreDeps{DB: db, Secrets: sec, DataDir: t.TempDir(), ListenAddr: addr, Host: "ctl.lan:8460", Logger: quietLog()}
	}
	for _, addr := range []string{"0.0.0.0:8460", "[::]:8460", "8.8.8.8:8460", ":8460"} {
		if rc, err := openCore(ctx, deps(addr)); rc != nil || err != nil {
			t.Fatalf("the registry started on %q: %v %v", addr, rc, err)
		}
	}
	rc, err := openCore(ctx, deps("192.168.1.20:8460"))
	if err != nil || rc == nil {
		t.Fatalf("the registry did not start on a private address: %v", err)
	}
	defer rc.close()

	get := func(h http.Handler, token string) int {
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v2/", nil)
		req.RemoteAddr = "192.168.1.30:5000"
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := get(rc.Registry.Handler(), ""); c != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", c)
	}
	tok, err := rc.Tokens.Issue(ctx, "m1", []registry.Scope{{Name: "crsp/std/base", Actions: []string{"pull"}}})
	if err != nil || tok.Value == "" {
		t.Fatalf("no token: %v", err)
	}
	if c := get(rc.Registry.Handler(), tok.Value); c != http.StatusOK {
		t.Fatalf("with a token: %d", c)
	}
	// the mirror is guarded by the same token
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	if c := get(rc.MirrorGuard(ok), ""); c != http.StatusUnauthorized {
		t.Fatalf("mirror anonymous: %d", c)
	}
	if c := get(rc.MirrorGuard(ok), tok.Value); c != http.StatusNoContent {
		t.Fatalf("mirror with a token: %d", c)
	}
	// the key is kept: a restart reads the same key and the token still verifies
	rc2, err := openCore(ctx, deps("192.168.1.20:8460"))
	if err != nil || rc2 == nil {
		t.Fatal(err)
	}
	defer rc2.close()
	if _, _, err := rc2.Tokens.Verify(ctx, tok.Value); err != nil {
		t.Fatalf("the token key changed: %v", err)
	}
}

// The real core, wired: the sink answers tokens through it and the three
// promoted hooks are registered on its promoter.
func TestWireRegistryWithRealCore(t *testing.T) {
	ctx, cancel, d, c := registryTestDeps(t)
	d.Cfg.ListenAddr = "127.0.0.1:8460"
	d.Secrets = secrets.NewMemory()
	w, err := wireRegistry(ctx, d, c, nil, nil, events.NewHub(events.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	if w.Core == nil || w.Core.Registry == nil || w.Core.Layers == nil || w.Core.MirrorGuard == nil {
		t.Fatalf("the core is not whole: %+v", w.Core)
	}
	if d.Link.Registry == nil || c.Options.RegistryRead == nil {
		t.Fatal("the registry is not wired")
	}
	cancel()
	c.Close()
}
