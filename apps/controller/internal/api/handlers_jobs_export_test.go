package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

// fakeJobs holds jobs newest first and pages them like the store: Limit+1 rows after the cursor.
type fakeJobs struct {
	all    []slots.JobRecord
	filter []slots.JobFilter
}

func (f *fakeJobs) ListJobs(_ context.Context, fl slots.JobFilter) ([]slots.JobRecord, error) {
	f.filter = append(f.filter, fl)
	var out []slots.JobRecord
	for _, j := range f.all {
		if fl.After != 0 && j.ID >= fl.After {
			continue
		}
		if fl.Result != "" && j.Conclusion != fl.Result {
			continue
		}
		out = append(out, j)
		if len(out) == fl.Limit+1 {
			break
		}
	}
	return out, nil
}
func (f *fakeJobs) GetJob(context.Context, int64) (slots.JobRecord, bool, error) {
	return slots.JobRecord{}, false, nil
}
func (f *fakeJobs) MachineExists(context.Context, string) (bool, error) { return true, nil }

func jobsMux(j slots.Jobs) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Jobs: j, Authn: asRole(auth.RoleOwner)})
	return mux
}

func getExport(t *testing.T, mux *http.ServeMux, query string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/exports/jobs"+query, nil))
	return rec
}

func TestExportJobsHoldsEveryRowNewestFirst(t *testing.T) {
	fj := &fakeJobs{}
	for id := int64(450); id >= 1; id-- { // more than two pages of 200
		fj.all = append(fj.all, slots.JobRecord{ID: id, Repository: "o/r", RunID: id, RunAttempt: 1, JobName: "test",
			State: slots.JobDone, Conclusion: "success", Labels: []string{"linux"}})
	}
	rec := getExport(t, jobsMux(fj), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/csv" {
		t.Fatalf("content type %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !regexp.MustCompile(`^attachment; filename="crsp-jobs-\d{4}-\d{2}-\d{2}-\d{4}\.csv"$`).MatchString(cd) {
		t.Fatalf("content disposition %q", cd)
	}
	rows, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 451 { // header + every job
		t.Fatalf("rows %d, want 451", len(rows))
	}
	if rows[1][0] != "450" || rows[451-1][0] != "1" {
		t.Fatalf("not newest first: first %s last %s", rows[1][0], rows[450][0])
	}
	if len(fj.filter) != 3 {
		t.Fatalf("pages read %d, want 3", len(fj.filter))
	}
}

func TestExportJobsPassesTheFilter(t *testing.T) {
	fj := &fakeJobs{all: []slots.JobRecord{
		{ID: 3, JobName: "a", Conclusion: "failure", State: slots.JobDone},
		{ID: 2, JobName: "b", Conclusion: "success", State: slots.JobDone},
	}}
	rec := getExport(t, jobsMux(fj), "?result=failure&repository_id=7&machine_id=m1&backend=docker&state=done&from=2026-11-01T00:00:00Z")
	rows, _ := csv.NewReader(rec.Body).ReadAll()
	if len(rows) != 2 || rows[1][0] != "3" {
		t.Fatalf("rows %v", rows)
	}
	f := fj.filter[0]
	if f.RepositoryID != 7 || f.MachineID != "m1" || f.Backend != "docker" || f.Result != "failure" || f.State != slots.JobDone ||
		!f.From.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("filter %+v", f)
	}
}

func TestExportJobsNeutralisesFormulas(t *testing.T) {
	fj := &fakeJobs{all: []slots.JobRecord{{ID: 1, JobName: `=HYPERLINK("http://x")`, Repository: "+o/r", Workflow: "@w", State: slots.JobDone}}}
	rec := getExport(t, jobsMux(fj), "")
	rows, _ := csv.NewReader(rec.Body).ReadAll()
	if rows[1][5] != `'=HYPERLINK("http://x")` || rows[1][1] != "'+o/r" || rows[1][4] != "'@w" {
		t.Fatalf("not neutralised: %v", rows[1])
	}
}

func TestExportJobsJSON(t *testing.T) {
	fj := &fakeJobs{all: []slots.JobRecord{{ID: 1, JobName: "a", Repository: "o/r", State: slots.JobDone, Conclusion: "success", Backend: "docker"}}}
	rec := getExport(t, jobsMux(fj), "?format=json")
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" || !strings.Contains(rec.Header().Get("Content-Disposition"), ".json") {
		t.Fatalf("headers %v", rec.Header())
	}
	var out []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out) != 1 || out[0]["backend"] != "docker" {
		t.Fatalf("body %s (%v)", rec.Body, err)
	}
}

func TestExportJobsEmptyAndErrors(t *testing.T) {
	rec := getExport(t, jobsMux(&fakeJobs{}), "")
	rows, _ := csv.NewReader(rec.Body).ReadAll()
	if rec.Code != 200 || len(rows) != 1 {
		t.Fatalf("empty export: %d %v", rec.Code, rows)
	}
	if rec := getExport(t, jobsMux(&fakeJobs{}), "?state=nope"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad state: %d", rec.Code)
	}
	if rec := getExport(t, jobsMux(nil), ""); rec.Code != http.StatusNotImplemented {
		t.Fatalf("unwired: %d", rec.Code)
	}
}

func TestListJobsPassesTheBackendFilterAndSendsBackendAndIsolation(t *testing.T) {
	fj := &fakeJobs{all: []slots.JobRecord{{ID: 1, JobName: "a", State: slots.JobDone, Backend: "docker", Isolation: "container"}}}
	rec := httptest.NewRecorder()
	jobsMux(fj).ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/jobs?backend=docker", nil))
	if rec.Code != http.StatusOK || fj.filter[0].Backend != "docker" {
		t.Fatalf("status %d filter %+v", rec.Code, fj.filter)
	}
	var out struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Items[0]["backend"] != "docker" || out.Items[0]["isolation"] != "container" {
		t.Fatalf("body %s (%v)", rec.Body, err)
	}
}
