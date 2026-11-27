package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// scheduling R5.100: the run cancel is POST /repos/{owner}/{repo}/actions/runs/{id}/cancel.
func TestCancelWorkflowRun(t *testing.T) {
	e := newEnv(t)
	e.fake.AddInstall(10, "alice", "User")
	e.fake.AddRepo(100, 10, "alice", "demo", false)
	target, _ := url.Parse(e.fake.URL)
	var mu sync.Mutex
	var hits []string
	status := http.StatusAccepted
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/cancel") {
			mu.Lock()
			hits = append(hits, r.Method+" "+r.URL.Path)
			code := status
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"message":"x"}`))
			return
		}
		httputil.NewSingleHostReverseProxy(target).ServeHTTP(w, r)
	}))
	defer front.Close()
	c := New(Config{APIBase: front.URL, WebBase: front.URL, Secrets: e.secrets, Now: e.clock, Sleep: func(context.Context, time.Duration) error { return nil }})
	c.UseApp(e.fake.AppID, "Iv23liFAKECLIENTID00")
	ctx := context.Background()
	if err := c.CancelWorkflowRun(ctx, 10, "alice", "demo", 77); err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0] != "POST /repos/alice/demo/actions/runs/77/cancel" {
		t.Fatalf("hits %v", hits)
	}
	mu.Lock()
	status = http.StatusConflict
	mu.Unlock()
	if err := c.CancelWorkflowRun(ctx, 10, "alice", "demo", 77); err != nil {
		t.Fatalf("a finished run is not an error: %v", err)
	}
	mu.Lock()
	status = http.StatusForbidden
	mu.Unlock()
	if err := c.CancelWorkflowRun(ctx, 10, "alice", "demo", 77); !errors.Is(err, ErrCancelNeedsWrite) {
		t.Fatalf("forbidden: %v", err)
	}
}
