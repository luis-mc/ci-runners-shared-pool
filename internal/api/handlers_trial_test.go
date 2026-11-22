package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/trial"
)

// R5.66: read the trial, set it, refuse a public repository, and the canary
// variables go away with off.
func TestRepositoryTrialOverHTTP(t *testing.T) {
	h := newHarness(t)
	owner, csrf := h.setup(1, "alice")
	hdr := map[string]string{"X-CSRF-Token": csrf}
	h.fake.AddInstall(10, "alice", "User", 1)
	h.fake.AddRepo(100, 10, "alice", "app", true, 1)
	h.fake.AddRepo(101, 10, "alice", "site", false, 1) // public
	tv := h.repos.Trial(nil)
	m := trial.New(trial.Config{Vars: tv, Dispatcher: tv, KV: &trial.MemKV{}, Now: time.Now})
	mux := http.NewServeMux()
	Register(mux, Options{Version: "test", DB: okPinger{}, Trial: m,
		GitHub: &GitHubArea{Auth: h.auth, Repos: h.repos, Webhook: h.hook, WebBase: h.fake.URL}})
	h.srv = httptest.NewServer(Recover(slog.New(slog.NewTextHandler(io.Discard, nil)), mux))
	t.Cleanup(h.srv.Close)
	if res, b := h.do("GET", "/api/v1/github/repositories", nil, owner, nil); res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}

	res, b := h.do("GET", "/api/v1/repositories/100/trial", nil, owner, nil)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"mode":"off"`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	res, b = h.do("PUT", "/api/v1/repositories/100/trial", map[string]any{"mode": "observe"}, owner, hdr)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"mode":"observe"`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	// A canary with no known job is refused, and changes nothing.
	res, b = h.do("PUT", "/api/v1/repositories/100/trial", map[string]any{"mode": "canary", "percent": 50}, owner, hdr)
	if res.StatusCode != 400 && res.StatusCode != 422 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	res, b = h.do("PUT", "/api/v1/repositories/100/trial", map[string]any{"mode": "canary", "jobs": []string{"build"}}, owner, hdr)
	var got struct {
		Mode      string
		Variables []string
	}
	_ = json.Unmarshal(b, &got)
	if res.StatusCode != 200 || got.Mode != "canary" || len(got.Variables) != 1 || got.Variables[0] != "CRSP_RUNS_ON_CANARY_BUILD" {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if _, ok := h.fake.Variable("alice/app", "CRSP_RUNS_ON_CANARY_BUILD"); !ok {
		t.Fatal("canary variable written")
	}
	if res, b = h.do("PUT", "/api/v1/repositories/100/trial", map[string]any{"mode": "off"}, owner, hdr); res.StatusCode != 200 {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	if _, ok := h.fake.Variable("alice/app", "CRSP_RUNS_ON_CANARY_BUILD"); ok {
		t.Fatal("off removes the canary variable")
	}
	// Public: refused.
	res, b = h.do("PUT", "/api/v1/repositories/101/trial", map[string]any{"mode": "observe"}, owner, hdr)
	if res.StatusCode != 400 && res.StatusCode != 422 || !strings.Contains(string(b), "public") {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	// A bad mode word.
	if res, _ = h.do("PUT", "/api/v1/repositories/100/trial", map[string]any{"mode": "wild"}, owner, hdr); res.StatusCode != 400 {
		t.Fatalf("%d", res.StatusCode)
	}
}
