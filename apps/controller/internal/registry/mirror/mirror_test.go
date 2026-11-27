package mirror

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/fsdriver"
)

func settings(ups ...string) registry.MirrorSettings {
	return registry.MirrorSettings{Enabled: true, Upstreams: ups, TagRecheck: time.Hour, BudgetBytes: 1 << 30}
}

// R5.63, R5.64: one proxy configuration per upstream, with its own folder.
func TestProxyConfigPerUpstream(t *testing.T) {
	s := settings("docker.io", "ghcr.io")
	d := t.TempDir()
	hub, err := ProxyConfig(s, d, "docker.io")
	if err != nil {
		t.Fatal(err)
	}
	if hub.Proxy.RemoteURL != "https://registry-1.docker.io" {
		t.Fatalf("docker.io remote %q", hub.Proxy.RemoteURL)
	}
	gh, _ := ProxyConfig(s, d, "ghcr.io")
	if gh.Proxy.RemoteURL != "https://ghcr.io" {
		t.Fatalf("ghcr.io remote %q", gh.Proxy.RemoteURL)
	}
	hubDir := hub.Storage.Parameters()["rootdirectory"]
	ghDir := gh.Storage.Parameters()["rootdirectory"]
	if hubDir != filepath.Join(d, "registry-mirror", "docker.io") || ghDir == hubDir {
		t.Fatalf("folders %v %v", hubDir, ghDir)
	}
	if hub.Storage.Type() != fsdriver.Name || hub.Proxy.TTL == nil || *hub.Proxy.TTL < 7*24*time.Hour {
		t.Fatalf("storage %v ttl %v", hub.Storage.Type(), hub.Proxy.TTL)
	}
}

func TestFolderSettingBeatsTheDefaultAndPortIsSafeInAPath(t *testing.T) {
	s := settings("registry.example.com:5000")
	s.Folder = filepath.Join(t.TempDir(), "elsewhere")
	c, err := ProxyConfig(s, "/data", "registry.example.com:5000")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Storage.Parameters()["rootdirectory"]; got != filepath.Join(s.Folder, "registry.example.com_5000") {
		t.Fatalf("folder %v", got)
	}
}

func TestValidate(t *testing.T) {
	ok, err := Validate(settings(" Docker.IO ", "docker.io", "ghcr.io"))
	if err != nil || len(ok.Upstreams) != 2 || ok.Upstreams[0] != "docker.io" {
		t.Fatalf("%+v %v", ok, err)
	}
	for name, s := range map[string]registry.MirrorSettings{
		"scheme":        settings("https://docker.io"),
		"path":          settings("docker.io/library"),
		"none":          settings(),
		"negative":      {Enabled: true, Upstreams: []string{"docker.io"}, TagRecheck: time.Hour, BudgetBytes: -1},
		"recheck zero":  {Enabled: true, Upstreams: []string{"docker.io"}},
		"recheck large": {Enabled: true, Upstreams: []string{"docker.io"}, TagRecheck: 8 * 24 * time.Hour},
	} {
		if _, err := Validate(s); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// Off needs no upstream.
	if _, err := Validate(registry.MirrorSettings{TagRecheck: time.Hour}); err != nil {
		t.Fatal(err)
	}
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
	return rec
}

func allow(next http.Handler) http.Handler { return next }

// R5.67: without a token guard the mirror refuses everything.
func TestMirrorWithoutGuardRefusesEveryRequest(t *testing.T) {
	m := New(Options{DataDir: t.TempDir()})
	if err := m.Apply(context.Background(), settings("docker.io")); err != nil {
		t.Fatal(err)
	}
	if rec := get(m.Handler(), "/mirror/docker.io/v2/"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("%d", rec.Code)
	}
}

func TestMirrorRoutesByUpstreamAndStopsWhenOff(t *testing.T) {
	m := New(Options{DataDir: t.TempDir(), Guard: allow})
	t.Cleanup(func() { _ = m.Close() })
	if rec := get(m.Handler(), "/mirror/docker.io/v2/"); rec.Code != http.StatusNotFound {
		t.Fatalf("before apply %d", rec.Code)
	}
	if err := m.Apply(context.Background(), settings("docker.io")); err != nil {
		t.Fatal(err)
	}
	if rec := get(m.Handler(), "/mirror/quay.io/v2/"); rec.Code != http.StatusNotFound {
		t.Fatalf("not mirrored upstream %d", rec.Code)
	}
	if rec := get(m.Handler(), "/mirror/"); rec.Code != http.StatusNotFound {
		t.Fatalf("no upstream %d", rec.Code)
	}
	off := settings("docker.io")
	off.Enabled = false
	if err := m.Apply(context.Background(), off); err != nil {
		t.Fatal(err)
	}
	if rec := get(m.Handler(), "/mirror/docker.io/v2/"); rec.Code != http.StatusNotFound {
		t.Fatalf("off %d", rec.Code)
	}
}

// R5.64: a full mirror refuses new reads, so engines fall back to the upstream.
func TestMirrorOverBudgetAnswers503(t *testing.T) {
	d := t.TempDir()
	m := New(Options{DataDir: d, Guard: allow})
	t.Cleanup(func() { _ = m.Close() })
	s := settings("docker.io")
	s.BudgetBytes = 10
	if err := m.Apply(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "registry-mirror", "docker.io", "blob"), []byte(strings.Repeat("x", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := get(m.Handler(), "/mirror/docker.io/v2/")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	if m.UsedBytes() < 100 {
		t.Fatalf("used %d", m.UsedBytes())
	}
}

func TestApplyRefusesBadSettingsAndKeepsTheOldOnes(t *testing.T) {
	m := New(Options{DataDir: t.TempDir()})
	if err := m.Apply(context.Background(), settings("docker.io")); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(context.Background(), settings("not a host")); err == nil {
		t.Fatal("bad upstream accepted")
	}
	if got := m.Settings().Upstreams; len(got) != 1 || got[0] != "docker.io" {
		t.Fatalf("settings changed: %v", got)
	}
}
