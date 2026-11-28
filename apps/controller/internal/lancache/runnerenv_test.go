package lancache_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/lancache"
)

func TestRunnerEnvPointsNpmAndPipAtTheHubWithAFallback(t *testing.T) {
	g := lancache.ProxyGrant{Repo: "luis-mc__site", Token: "t0k"}
	env := lancache.RunnerEnv("https://10.0.0.5:8460/", g, "/r/ca.pem", "/r/combined.pem")
	want := map[string]string{
		"npm_config_registry": "https://10.0.0.5:8460/pkg/npm/luis-mc__site/t0k/",
		"PIP_INDEX_URL":       "https://10.0.0.5:8460/pkg/pypi/luis-mc__site/t0k/simple/",
		"PIP_EXTRA_INDEX_URL": "https://pypi.org/simple",
		"PIP_CERT":            "/r/combined.pem",
		"NODE_EXTRA_CA_CERTS": "/r/ca.pem",
	}
	if len(env) != len(want) {
		t.Fatalf("env = %v", env)
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
	for _, off := range []map[string]string{
		lancache.RunnerEnv("https://h", lancache.ProxyGrant{}, "/ca", "/b"),
		lancache.RunnerEnv("https://h", g, "", "/b"),
		lancache.RunnerEnv("", g, "/ca", "/b"),
		lancache.RunnerEnv("https://h", lancache.ProxyGrant{Repo: "a/b", Token: "x"}, "/ca", "/b"),
	} {
		if len(off) != 0 {
			t.Errorf("no grant, CA or hub must give no values, got %v", off)
		}
	}
}

func TestRunnerEnvWithoutABundleIsNpmOnly(t *testing.T) {
	env := lancache.RunnerEnv("https://h", lancache.ProxyGrant{Repo: "r", Token: "t"}, "C:\\crsp\\ca.pem", "")
	if env["npm_config_registry"] == "" || env["NODE_EXTRA_CA_CERTS"] == "" {
		t.Fatalf("npm values missing: %v", env)
	}
	for _, k := range []string{"PIP_INDEX_URL", "PIP_EXTRA_INDEX_URL", "PIP_CERT"} {
		if _, ok := env[k]; ok {
			t.Errorf("%s set without a bundle", k)
		}
	}
}

func TestProbeSkipsADownHubFor30Seconds(t *testing.T) {
	var up atomic.Bool
	var asked atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if !up.Load() || r.URL.Path != "/pkg/ping" {
			http.Error(w, "no", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()
	now := time.Now()
	p := &lancache.Probe{Now: func() time.Time { return now }}
	ctx := context.Background()
	if p.Up(ctx, srv.URL) {
		t.Fatal("a failing hub was up")
	}
	up.Store(true)
	n := asked.Load()
	now = now.Add(29 * time.Second)
	if p.Up(ctx, srv.URL) || asked.Load() != n {
		t.Fatal("a hub that failed was asked again within 30 s")
	}
	now = now.Add(2 * time.Second)
	if !p.Up(ctx, srv.URL) {
		t.Fatal("the hub was not asked again after 30 s")
	}
	if p.Up(ctx, "http://127.0.0.1:1") {
		t.Fatal("a closed port was up")
	}
}
