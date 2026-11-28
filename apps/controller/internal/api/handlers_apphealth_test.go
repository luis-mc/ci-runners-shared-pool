package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/repos"
)

type appState struct{ st github.AppState }

func (a *appState) State() github.AppState { return a.st }

func (h *harness) withAppHealth(src AppHealthSource) {
	h.t.Helper()
	mux := http.NewServeMux()
	Register(mux, Options{Version: "test", DB: okPinger{},
		GitHub: &GitHubArea{Auth: h.auth, Repos: h.repos, Webhook: h.hook, WebBase: h.fake.URL, Health: h.health, AppHealth: src}})
	h.srv = httptest.NewServer(Recover(slog.New(slog.NewTextHandler(io.Discard, nil)), mux))
	h.t.Cleanup(h.srv.Close)
}

func TestAppHealthOverHTTP(t *testing.T) { // R5.6
	h := newHarness(t)
	owner, _ := h.setup(1, "alice")
	src := &appState{}
	h.withAppHealth(src)
	res, b := h.do("GET", "/api/v1/github/app-health", nil, owner, nil)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"gone":false`) || strings.Contains(string(b), "reason") {
		t.Fatalf("a healthy App: %d %s", res.StatusCode, b)
	}
	src.st = github.AppState{Gone: true, Reason: github.ReasonAppDeleted, CheckedAt: time.Now()}
	res, b = h.do("GET", "/api/v1/github/app-health", nil, owner, nil)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"gone":true`) || !strings.Contains(string(b), github.ReasonAppDeleted) ||
		!strings.Contains(string(b), "checked_at") {
		t.Fatalf("a gone App says why: %d %s", res.StatusCode, b)
	}
	if res, _ = h.do("GET", "/api/v1/github/app-health", nil, nil, nil); res.StatusCode != 401 {
		t.Fatalf("a signed-out caller is refused: %d", res.StatusCode)
	}
}

func TestRepositoryThatLeftSaysWhy(t *testing.T) { // R5.22
	h := newHarness(t)
	owner, _ := h.setup(1, "alice")
	h.fake.AddInstall(10, "alice", "User", 1)
	h.fake.AddRepo(100, 10, "alice", "app", true, 1)
	h.fake.AddRepo(101, 10, "alice", "other", true, 1)
	if res, b := h.do("GET", "/api/v1/github/repositories", nil, owner, nil); res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	ctx := context.Background()
	if err := h.rstore.SetRepositoryPool(ctx, 100, false, repos.ChangedByInstallation, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := h.rstore.SetRepositoryPool(ctx, 101, false, "alice", time.Now()); err != nil {
		t.Fatal(err)
	}
	_, b := h.do("GET", "/api/v1/github/repositories", nil, owner, nil)
	if n := strings.Count(string(b), `"pool_off_reason":"left_installation"`); n != 1 {
		t.Fatalf("only the leaver carries the reason (%d): %s", n, b)
	}
}

// CRSP384, R5.14: the health answer says Device Flow is off, with the page where
// the one setting is turned on; the CLI login config names the same page.
func TestAppHealthAndLoginConfigNameTheDeviceFlowSetting(t *testing.T) {
	h := newHarness(t)
	owner, _ := h.setup(1, "alice")
	src := &appState{}
	h.withAppHealth(src)
	_, b := h.do("GET", "/api/v1/github/app-health", nil, owner, nil)
	if strings.Contains(string(b), "device_flow_off") || strings.Contains(string(b), "settings_url") {
		t.Fatalf("a healthy App says nothing about Device Flow: %s", b)
	}
	src.st = github.AppState{CheckedAt: time.Now(), DeviceFlowOff: true}
	res, b := h.do("GET", "/api/v1/github/app-health", nil, owner, nil)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"gone":false`) || !strings.Contains(string(b), `"device_flow_off":true`) ||
		!strings.Contains(string(b), "/settings/apps/") {
		t.Fatalf("Device Flow off: %d %s", res.StatusCode, b)
	}
	res, b = h.do("GET", "/api/v1/auth/cli/config", nil, nil, nil)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"settings_url"`) || !strings.Contains(string(b), "/settings/apps/") {
		t.Fatalf("login config: %d %s", res.StatusCode, b)
	}
}
