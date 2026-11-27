package scheduler_test

import (
	"context"
	"encoding/json"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

type fixture struct {
	t   *testing.T
	st  *store.Store
	eng *scheduler.Engine
	mux *http.ServeMux
	now time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.OpenForTests(context.Background(), filepath.Join(t.TempDir(), "d"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.SeedMachine(context.Background(), "m1", "linux"); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, st: st, now: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	f.eng = scheduler.NewEngine(st, scheduler.WithClock(func() time.Time { return f.now }))
	f.mux = http.NewServeMux()
	api.Register(f.mux, api.Options{Version: "t", DB: st, Jobs: st, Slots: f.eng, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Authn: ownerAuthn{}})
	return f
}

func (f *fixture) do(method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	f.t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func (f *fixture) take(gate string, run int64, job string) {
	f.t.Helper()
	d, err := f.eng.Gate(context.Background(), scheduler.GateRequest{GateID: gate, MachineID: "m1", EventClass: 1,
		Job:    scheduler.JobRef{Repository: "o/r", RunID: run, RunAttempt: 1, Job: job, RunnerName: "r1"},
		Holder: scheduler.Holder{Machine: "m1", HolderPID: 5},
		Load:   scheduler.Load{MemoryPct: 10, MemoryKnown: true}})
	if err != nil || len(d.Go) == 0 {
		f.t.Fatalf("take %s: %+v %v", gate, d, err)
	}
	f.now = f.now.Add(time.Millisecond)
}

// R5.58 through the API: the same status the CLI and console read.
func TestAPIListSlots(t *testing.T) {
	f := newFixture(t)
	f.take("a", 1, "build")
	f.take("b", 2, "build")
	if d, _ := f.eng.Gate(context.Background(), scheduler.GateRequest{GateID: "c", MachineID: "m1", EventClass: 1,
		Job: scheduler.JobRef{Repository: "o/r", RunID: 3, RunAttempt: 1, Job: "build"}, Holder: scheduler.Holder{Machine: "m1", HolderPID: 6}}); len(d.Go) != 0 {
		t.Fatal("expected a wait: both slots are taken")
	}
	rec, body := f.do("GET", "/api/v1/machines/m1/slots", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if len(body["slots"].([]any)) != 2 {
		t.Fatalf("body %v", body)
	}
	tickets := body["tickets"].([]any)
	if len(tickets) != 1 || tickets[0].(map[string]any)["place"].(float64) != 1 || tickets[0].(map[string]any)["wait_reason"] != "free_slot" {
		t.Fatalf("tickets %v", tickets)
	}
	if rec, _ := f.do("GET", "/api/v1/machines/nope/slots", ""); rec.Code != 404 {
		t.Fatalf("unknown machine: %d", rec.Code)
	}
}

// R5.37 through the API: a live slot needs a yes that names the job; a stale one does not.
func TestAPIFreeStaleCommand(t *testing.T) {
	f := newFixture(t)
	f.take("a", 1, "build")
	rec, body := f.do("POST", "/api/v1/machines/m1/slots/free-stale", "")
	if rec.Code != 200 || len(body["items"].([]any)) != 0 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	live := `{"slots":[{"kind":"ci","number":1}]}`
	rec, body = f.do("POST", "/api/v1/machines/m1/slots/free-stale", live)
	if rec.Code != 409 {
		t.Fatalf("live slot without confirm: %d %s", rec.Code, rec.Body.String())
	}
	e := body["error"].(map[string]any)
	if e["code"] != "needs_confirmation" || !strings.Contains(e["message"].(string), "o/r run 1 job build") {
		t.Fatalf("error %v", e)
	}
	rec, body = f.do("POST", "/api/v1/machines/m1/slots/free-stale", `{"slots":[{"kind":"ci","number":1}],"confirm":true}`)
	items := body["items"].([]any)
	if rec.Code != 200 || len(items) != 1 || items[0].(map[string]any)["reason"] != "freed by hand" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	// a slot that is not held
	if rec, _ := f.do("POST", "/api/v1/machines/m1/slots/free-stale", live); rec.Code != 404 {
		t.Fatalf("free slot: %d", rec.Code)
	}
	// a stale slot is freed without a yes, and listed with its reason
	f.take("b", 2, "build")
	f.now = f.now.Add(3 * time.Hour)
	rec, body = f.do("POST", "/api/v1/machines/m1/slots/free-stale", "")
	items = body["items"].([]any)
	if rec.Code != 200 || len(items) != 1 || items[0].(map[string]any)["reason"] != "older than 120 min" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

// observability R5.7 and scheduling R5.52: listJobs and getJob over the GitHub job rows.
func TestAPIListAndGetJobs(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.st.SetRepoPolicyForTests(ctx, 7, "o/r", slots.Policy{MaxParallel: 5}); err != nil {
		t.Fatal(err)
	}
	t0 := f.now.Add(-time.Hour)
	for i := int64(1); i <= 3; i++ {
		j := slots.JobRecord{ID: 100 + i, RepositoryID: 7, RunID: 50 + i, RunAttempt: 1, JobName: "build", Labels: []string{"self-hosted"},
			CreatedAt: t0, StartedAt: t0.Add(30 * time.Second), CompletedAt: t0.Add(90 * time.Second), Conclusion: "success", Trigger: "push"}
		status := "completed"
		if i == 3 {
			status, j.Conclusion, j.StartedAt, j.CompletedAt = "queued", "", time.Time{}, time.Time{}
		}
		if err := f.st.UpsertJobForTests(ctx, j, status); err != nil {
			t.Fatal(err)
		}
	}
	// the queued job waits behind two taken slots
	f.take("a", 1, "build")
	f.take("b", 2, "build")
	d, _ := f.eng.Gate(ctx, scheduler.GateRequest{GateID: "w", MachineID: "m1", EventClass: 1,
		Job: scheduler.JobRef{Repository: "o/r", RunID: 53, RunAttempt: 1, Job: "build"}, Holder: scheduler.Holder{Machine: "m1", HolderPID: 9}})
	if len(d.Go) != 0 {
		t.Fatal("expected a wait")
	}
	rec, body := f.do("GET", "/api/v1/jobs?limit=2", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	items := body["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["id"].(float64) != 103 || body["next_cursor"] == nil {
		t.Fatalf("body %v", body)
	}
	q := items[0].(map[string]any)
	if q["state"] != "queued" || q["wait_reason"] != "free_slot" || q["repository"] != "o/r" {
		t.Fatalf("queued job %v", q)
	}
	// the next page
	rec, body = f.do("GET", "/api/v1/jobs?limit=2&cursor="+body["next_cursor"].(string), "")
	items = body["items"].([]any)
	if rec.Code != 200 || len(items) != 1 || items[0].(map[string]any)["id"].(float64) != 101 || body["next_cursor"] != nil {
		t.Fatalf("page two %d %v", rec.Code, body)
	}
	done := items[0].(map[string]any)
	if done["state"] != "done" || done["result"] != "success" || done["run_seconds"].(float64) != 60 || done["github_wait_seconds"].(float64) != 30 {
		t.Fatalf("done job %v", done)
	}
	_, body = f.do("GET", "/api/v1/jobs?state=queued", "")
	if len(body["items"].([]any)) != 1 {
		t.Fatalf("state filter: %v", body)
	}
	if rec, _ := f.do("GET", "/api/v1/jobs?state=bogus", ""); rec.Code != 400 {
		t.Fatalf("bad state: %d", rec.Code)
	}
	rec, body = f.do("GET", "/api/v1/jobs/102", "")
	if rec.Code != 200 || body["id"].(float64) != 102 {
		t.Fatalf("get job %d %v", rec.Code, body)
	}
	if rec, _ := f.do("GET", "/api/v1/jobs/999", ""); rec.Code != 404 {
		t.Fatalf("missing job: %d", rec.Code)
	}
}

// With nothing wired the routes still answer with the not-built envelope.
func TestAPINotWiredAnswers501(t *testing.T) {
	mux := http.NewServeMux()
	api.Register(mux, api.Options{Version: "t", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Authn: ownerAuthn{}})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/jobs", nil))
	if rec.Code != 501 {
		t.Fatalf("%d", rec.Code)
	}
}

// ownerAuthn signs every request in as an owner with a CLI-style token (no
// session, so no CSRF token or Origin): these tests are about jobs, not sign-in.
type ownerAuthn struct{}

func (ownerAuthn) Authenticate(context.Context, *http.Request) (auth.Principal, error) {
	return auth.Principal{UserID: 1, Login: "tester", Role: auth.RoleOwner, Source: "cli", TokenID: "t"}, nil
}
