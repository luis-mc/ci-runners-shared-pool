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

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata/reconcile"
)

// jobsProxy answers the two job routes itself and sends everything else (App tokens) to the fake GitHub.
func jobsProxy(t *testing.T, e *env, routes map[string]string) (*REST, *[]string) {
	t.Helper()
	target, err := url.Parse(e.fake.URL)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var seen []string
	rp := httputil.NewSingleHostReverseProxy(target)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body, ok := routes[r.URL.Path]; ok {
			mu.Lock()
			seen = append(seen, r.URL.RequestURI())
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
			return
		}
		if strings.Contains(r.URL.Path, "/actions/") {
			mu.Lock()
			seen = append(seen, r.URL.RequestURI())
			mu.Unlock()
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		rp.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	c := New(Config{APIBase: srv.URL, WebBase: srv.URL, Secrets: e.secrets, Now: e.clock,
		Sleep: func(context.Context, time.Duration) error { return nil }})
	c.UseApp(e.fake.AppID, "Iv23liFAKECLIENTID00")
	return c, &seen
}

const jobBody = `{"id":55,"run_id":9,"run_attempt":2,"status":"completed","conclusion":"success",
 "runner_name":"old-pool-3","runner_group_name":"Default","labels":["self-hosted","linux"],
 "created_at":"2026-11-20T10:00:00Z","started_at":"2026-11-20T10:01:00Z","completed_at":"2026-11-20T10:05:00Z",
 "steps":[{"number":1,"name":"Set up job","status":"completed","conclusion":"success","started_at":"2026-11-20T10:01:00Z","completed_at":"2026-11-20T10:01:05Z"},
          {"number":2,"name":"Run tests","status":"queued","conclusion":null,"started_at":null,"completed_at":null}]}`

func installed(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	e.fake.AddInstall(10, "alice", "User", 1)
	e.fake.AddRepo(100, 10, "alice", "demo", true, 1)
	return e
}

func TestRESTJobReadsRunnerAndSteps(t *testing.T) { // R5.39, R5.40
	e := installed(t)
	c, seen := jobsProxy(t, e, map[string]string{"/repos/alice/demo/actions/jobs/55": jobBody})
	j, err := c.Job(context.Background(), 10, "alice", "demo", 55)
	if err != nil {
		t.Fatal(err)
	}
	if j.RunnerName != "old-pool-3" || j.RunnerGroupName != "Default" || len(j.Labels) != 2 || j.RunAttempt != 2 {
		t.Fatalf("job %+v", j)
	}
	if len(j.Steps) != 2 || j.Steps[0].Name != "Set up job" || j.Steps[0].CompletedAt.IsZero() || !j.Steps[1].StartedAt.IsZero() {
		t.Fatalf("steps %+v", j.Steps)
	}
	if len(*seen) != 1 {
		t.Fatalf("calls %v", *seen)
	}
}

func TestRESTJobGoneIs404(t *testing.T) { // R5.41
	e := installed(t)
	c, _ := jobsProxy(t, e, nil)
	if _, err := c.Job(context.Background(), 10, "alice", "demo", 56); !errors.Is(err, reconcile.ErrGone) {
		t.Fatalf("err = %v, want ErrGone", err)
	}
	if _, err := c.RunJobs(context.Background(), 10, "alice", "demo", 9, 1); !errors.Is(err, reconcile.ErrGone) {
		t.Fatalf("RunJobs err = %v, want ErrGone", err)
	}
}

func TestRESTRunJobsAllPages(t *testing.T) { // R5.41
	e := installed(t)
	routes := map[string]string{}
	c, seen := jobsProxy(t, e, routes)
	routes["/repos/alice/demo/actions/runs/9/attempts/2/jobs"] = `{"total_count":2,"jobs":[` + jobBody + `]}`
	jobs, err := c.RunJobs(context.Background(), 10, "alice", "demo", 9, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != 55 || len(*seen) != 1 || !strings.Contains((*seen)[0], "per_page=100") {
		t.Fatalf("jobs %+v, calls %v", jobs, *seen)
	}
}

func TestParseRunnerFields(t *testing.T) { // R5.39
	n, g, l, err := ParseRunnerFields([]byte(`{"action":"in_progress","workflow_job":{"runner_name":"GitHub Actions 3","runner_group_name":"GitHub Actions","labels":["ubuntu-latest"]}}`))
	if err != nil || n != "GitHub Actions 3" || g != "GitHub Actions" || len(l) != 1 {
		t.Fatalf("%q %q %v %v", n, g, l, err)
	}
	if _, _, _, err := ParseRunnerFields([]byte("{")); err == nil {
		t.Fatal("bad body accepted")
	}
}
