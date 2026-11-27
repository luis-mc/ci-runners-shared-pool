package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// insightsEnv is a REST client over a fake API server and a fake blob server (where GitHub redirects logs
// and artifact zips to). It records the Authorization header each server saw.
type insightsEnv struct {
	c        *REST
	mu       sync.Mutex
	apiAuth  []string
	blobAuth []string
	paths    []string
}

func newInsightsEnv(t *testing.T) *insightsEnv {
	t.Helper()
	e := &insightsEnv{}
	blob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		e.blobAuth = append(e.blobAuth, r.Header.Get("Authorization"))
		e.mu.Unlock()
		_, _ = io.WriteString(w, "BODY of "+r.URL.Path)
	}))
	t.Cleanup(blob.Close)
	var api *httptest.Server
	api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		e.apiAuth = append(e.apiAuth, r.Header.Get("Authorization"))
		e.paths = append(e.paths, r.URL.RequestURI())
		e.mu.Unlock()
		switch p := r.URL.Path; {
		case p == "/repos/octo/repo/actions/jobs/1/logs", p == "/repos/octo/repo/actions/artifacts/9/zip":
			http.Redirect(w, r, blob.URL+"/file"+p[strings.LastIndex(p, "/"):], http.StatusFound)
		case p == "/repos/octo/repo/actions/jobs/2/logs":
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		case p == "/repos/octo/repo/actions/jobs/3/logs":
			http.Error(w, `{"message":"Gone"}`, http.StatusGone)
		case p == "/repos/octo/repo/actions/jobs/4/logs":
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(time.Hour).Unix()))
			http.Error(w, `{"message":"API rate limit exceeded"}`, http.StatusForbidden)
		case p == "/repos/octo/repo/actions/jobs/5/logs":
			http.Error(w, `{"message":"boom"}`, http.StatusBadGateway)
		case p == "/repos/octo/repo/actions/runs/77/artifacts" && r.URL.Query().Get("page") == "":
			w.Header().Set("Link", `<`+api.URL+`/repos/octo/repo/actions/runs/77/artifacts?page=2>; rel="next"`)
			_, _ = io.WriteString(w, `{"total_count":3,"artifacts":[{"id":1,"name":"a","size_in_bytes":10,"expired":false},{"id":2,"name":"b","size_in_bytes":20,"expired":true}]}`)
		case p == "/repos/octo/repo/actions/runs/77/artifacts":
			_, _ = io.WriteString(w, `{"total_count":3,"artifacts":[{"id":3,"name":"c","size_in_bytes":30,"expired":false}]}`)
		case p == "/repos/octo/repo/git/commits/abc123":
			_, _ = io.WriteString(w, `{"sha":"abc123","tree":{"sha":"tree456"},"message":"never read"}`)
		case p == "/repos/octo/repo/git/commits/nottree":
			_, _ = io.WriteString(w, `{"sha":"nottree"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(api.Close)
	e.c = New(Config{APIBase: api.URL, HTTP: &http.Client{}})
	e.c.tokens[7] = cachedToken{value: "install-token", expires: time.Now().Add(time.Hour)}
	return e
}

func readAll(t *testing.T, rc io.ReadCloser) string {
	t.Helper()
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// R5.2, R5.3: a log and a zip are streamed through GitHub's redirect and the installation token is not sent to
// the address it redirects to.
func TestJobLogAndArtifactFollowRedirectWithoutToken(t *testing.T) {
	e := newInsightsEnv(t)
	ctx := context.Background()
	rc, size, err := e.c.JobLog(ctx, 7, "octo", "repo", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, rc); got != "BODY of /file/logs" || size != int64(len(got)) {
		t.Fatalf("log = %q, size %d", got, size)
	}
	rc, _, err = e.c.DownloadArtifact(ctx, 7, "octo", "repo", 9)
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, rc); got != "BODY of /file/zip" {
		t.Fatalf("zip = %q", got)
	}
	if len(e.apiAuth) != 2 || e.apiAuth[0] != "Bearer install-token" {
		t.Fatalf("api auth = %v", e.apiAuth)
	}
	for _, a := range e.blobAuth {
		if a != "" {
			t.Fatalf("the token went to the redirect address: %q", a)
		}
	}
}

// 404 and 410 are ErrGone; a rate limit and a server error keep their own kind.
func TestLogErrors(t *testing.T) {
	e := newInsightsEnv(t)
	ctx := context.Background()
	for _, id := range []int64{2, 3} {
		if _, _, err := e.c.JobLog(ctx, 7, "octo", "repo", id); !errors.Is(err, ErrGone) {
			t.Errorf("job %d: %v, want ErrGone", id, err)
		}
	}
	_, _, err := e.c.JobLog(ctx, 7, "octo", "repo", 4)
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Errorf("rate limit: %v", err)
	}
	if _, _, err := e.c.JobLog(ctx, 7, "octo", "repo", 5); !errors.Is(err, ErrUnavailable) {
		t.Errorf("server error: %v", err)
	}
}

// R6.1: the calls are background calls: past half of the hourly limit they stop before any request.
func TestInsightsCallsAreBackground(t *testing.T) {
	e := newInsightsEnv(t)
	e.c.budgets[7] = &budget{limit: 5000, remaining: 100, reset: time.Now().Add(time.Hour)}
	e.c.cfg.Now = time.Now
	var rl *RateLimitError
	if _, _, err := e.c.JobLog(context.Background(), 7, "octo", "repo", 1); !errors.As(err, &rl) {
		t.Fatalf("job log: %v", err)
	}
	if _, err := e.c.RunArtifacts(context.Background(), 7, "octo", "repo", 77); !errors.As(err, &rl) {
		t.Fatalf("artifacts: %v", err)
	}
	if len(e.paths) != 0 {
		t.Fatalf("requests were sent: %v", e.paths)
	}
}

func TestRunArtifactsPagesAndCommitTree(t *testing.T) {
	e := newInsightsEnv(t)
	ctx := context.Background()
	got, err := e.c.RunArtifacts(ctx, 7, "octo", "repo", 77)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[1].ID != 2 || !got[1].Expired || got[2].Size != 30 || got[0].Name != "a" {
		t.Fatalf("artifacts = %+v", got)
	}
	tree, err := e.c.CommitTree(ctx, 7, "octo", "repo", "abc123")
	if err != nil || tree != "tree456" {
		t.Fatalf("tree = %q, %v", tree, err)
	}
	if _, err := e.c.CommitTree(ctx, 7, "octo", "repo", "nottree"); err == nil {
		t.Fatal("a commit without a tree was accepted")
	}
	if _, err := e.c.CommitTree(ctx, 7, "octo", "repo", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing commit: %v", err)
	}
}
