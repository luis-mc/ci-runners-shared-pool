package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// CRSP290 (audit F1, F2, F11): the list and detail handlers read a page in a fixed, small number of SQL statements,
// however many runs and jobs are open. These tests run the handlers over a real store that counts its statements, so an
// N+1 (one forecast, one run read, one timer read per row) cannot come back unseen.

const openRunsSeeded = 60

var testNow = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

type okDB struct{}

func (okDB) Ping(context.Context) error { return nil }

type ownerAuthn struct{}

func (ownerAuthn) Authenticate(context.Context, *http.Request) (auth.Principal, error) {
	return auth.Principal{UserID: 1, Login: "tester", Role: auth.RoleOwner, Source: "cli", TokenID: "t"}, nil
}

// oneMachine gives the slot count of one machine; the rest of the interface is never called.
type oneMachine struct{ api.MachinesService }

func (oneMachine) List(context.Context) ([]machines.View, error) {
	return []machines.View{{Machine: machines.Machine{ID: "m1"}, Settings: machines.Settings{JobSlots: 4}}}, nil
}

// seedOpenWork writes openRunsSeeded open runs of three jobs each (one done, one running, one queued) across 8
// repositories, with enough finished jobs per key to forecast from, and some finished jobs in the timeline window.
func seedOpenWork(t *testing.T, js jobsdata.Store) {
	t.Helper()
	ctx := context.Background()
	wf := ".github/workflows/ci.yml"
	id := int64(1000)
	job := func(repo int64, run int64, name string, state jobsdata.State, res jobsdata.Result, started, done time.Time) {
		id++
		jt, matrix := jobsdata.SplitName(name)
		j := jobsdata.Job{ID: id, RepositoryID: repo, Repository: fmt.Sprintf("o/r%d", repo), RunID: run, Attempt: 1, Workflow: wf, Name: name,
			JobType: jt, Matrix: matrix, JobKey: jobsdata.MakeKey(fmt.Sprintf("o/r%d", repo), wf, name), Branch: "main", State: state, Result: res,
			CreatedAt: testNow.Add(-3 * time.Hour), StartedAt: started, CompletedAt: done, MachineID: "m1"}
		if err := js.UpsertJob(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	// History: 12 finished jobs per (repository, name), over the last days.
	for repo := int64(1); repo <= 8; repo++ {
		for n, name := range []string{"build", "test", "lint"} {
			for i := 0; i < 12; i++ {
				end := testNow.Add(-time.Duration(26+i*5+n) * time.Hour)
				job(repo, 100+repo, name, jobsdata.Done, jobsdata.Result("success"), end.Add(-time.Duration(60+i)*time.Second), end)
			}
		}
	}
	for i := int64(0); i < openRunsSeeded; i++ {
		repo, run := i%8+1, 5000+i
		if err := js.UpsertRun(ctx, jobsdata.Run{RepositoryID: repo, Repository: fmt.Sprintf("o/r%d", repo), RunID: run, Attempt: 1, Workflow: wf,
			Branch: "main", CreatedAt: testNow.Add(-2 * time.Hour)}); err != nil {
			t.Fatal(err)
		}
		job(repo, run, "build", jobsdata.Done, jobsdata.Result("success"), testNow.Add(-110*time.Minute), testNow.Add(-100*time.Minute))
		job(repo, run, "test", jobsdata.Running, "", testNow.Add(-30*time.Minute), time.Time{})
		job(repo, run, "lint", jobsdata.Queued, "", time.Time{}, time.Time{})
	}
}

func countedMux(t *testing.T) (*http.ServeMux, *store.QueryCount) {
	t.Helper()
	db, qc, err := store.OpenCounted(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedOpenWork(t, db.Jobs())
	mux := http.NewServeMux()
	api.Register(mux, api.Options{Version: "t", DB: okDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), JobsData: db.Jobs(),
		Machines: oneMachine{}, Authn: ownerAuthn{}, Now: func() time.Time { return testNow }})
	return mux, qc
}

func queriesOf(t *testing.T, mux *http.ServeMux, qc *store.QueryCount, target string) int64 {
	t.Helper()
	qc.Reset()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: %d %s", target, rec.Code, rec.Body)
	}
	return qc.Load()
}

func TestListAndDetailQueryCountsDoNotGrowWithOpenWork(t *testing.T) {
	mux, qc := countedMux(t)
	cases := []struct {
		target string
		max    int64
	}{
		{"/api/v1/runs?limit=50", 10},
		{"/api/v1/runs?limit=50&state=running", 10},
		{"/api/v1/jobs?limit=50", 12},
		{"/api/v1/jobs?limit=50&state=queued", 12},
		{"/api/v1/jobs?limit=50&state=running", 12},
		{"/api/v1/runs/5000", 14},
	}
	for _, c := range cases {
		got := queriesOf(t, mux, qc, c.target)
		t.Logf("%s: %d queries", c.target, got)
		if got > c.max {
			t.Errorf("%s ran %d queries, want at most %d", c.target, got, c.max)
		}
	}
	// The detail of a queued job: its forecast and the queue place share the same reads.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/jobs?state=queued&limit=1", nil))
	page := decodeJobPage(t, rec)
	if len(page.Items) != 1 {
		t.Fatalf("queued jobs: %d", len(page.Items))
	}
	if got := queriesOf(t, mux, qc, fmt.Sprintf("/api/v1/jobs/%d", page.Items[0].Id)); got > 14 {
		t.Errorf("queued job detail ran %d queries, want at most 14", got)
	} else {
		t.Logf("queued job detail: %d queries", got)
	}
}

func decodeJobPage(t *testing.T, rec *httptest.ResponseRecorder) gen.JobPage {
	t.Helper()
	var p gen.JobPage
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}
