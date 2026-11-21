package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsize"
)

type sizeFake struct {
	views []jobsize.View
	set   []string
}

func (f *sizeFake) Views(_ context.Context, repo string) ([]jobsize.View, string, error) {
	return f.views, "", nil
}
func (f *sizeFake) SetOverride(_ context.Context, repo, pattern string, class jobsize.Class) error {
	if class != "" && !jobsize.ValidClass(class) {
		return context.DeadlineExceeded
	}
	f.set = append(f.set, repo+"|"+pattern+"|"+string(class))
	return nil
}
func (f *sizeFake) CostTable(context.Context) jobsize.CostTable { return jobsize.DefaultCostTable() }

func sizeMux(t *testing.T, role auth.Role, f JobSizeService) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), JobSizes: f, Authn: asRole(role)})
	return mux
}

// The contract of the job size routes (scheduling R5.79 to R5.87): the list
// carries the source, both override values, the copy line and the cost table;
// only an admin writes; an empty or bad request is a 400, never a 500.
func TestJobSizeRoutesContract(t *testing.T) {
	f := &sizeFake{views: []jobsize.View{{
		Repository: "o/r", Job: "integration-db", Size: jobsize.Size{Weight: jobsize.Weight{CPU: 4, Memory: 4}, Class: jobsize.Heavy, Source: jobsize.SourceOverride, Origin: jobsize.OriginConsole},
		Scan:    &jobsize.ScanRow{Confidence: jobsize.ConfLow, Tests: 12, Reasons: []string{"why"}, ResetNote: "Tests went from 3 to 12, so the size went back to the scan guess."},
		Console: "heavy", File: "normal",
	}}}
	mux := sizeMux(t, auth.RoleOwner, f)
	rec := do(mux, http.MethodGet, "/api/v1/job-sizes?repository=o/r", "")
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Repository string `json:"repository"`
		FileError  string `json:"file_error"`
		Items      []struct {
			Job, Class, Source string
			Weight             struct{ Cpu, Memory int }
			Confidence         string
			Tests              int
			OverrideConsole    string `json:"override_console"`
			OverrideFile       string `json:"override_file"`
			CopyLine           string `json:"copy_line"`
			ResetNote          string `json:"reset_note"`
			Reasons, Services  []string
		}
		CostTable []struct {
			Kind, Key, Class string
			Cpu, Gib         float64
			Assumption       bool
		} `json:"cost_table"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	it := out.Items[0]
	if out.Repository != "o/r" || it.Source != "override" || it.Class != "heavy" || it.Weight.Cpu != 4 || it.OverrideConsole != "heavy" || it.OverrideFile != "normal" ||
		it.CopyLine != `sizes: { "integration-db": heavy }` || it.Confidence != "low" || it.Tests != 12 || !strings.Contains(it.ResetNote, "3 to 12") || it.Services == nil {
		t.Fatalf("item = %+v", it)
	}
	seen := map[string]bool{}
	for _, r := range out.CostTable {
		seen[r.Kind+":"+r.Key] = r.Assumption
	}
	if !seen["framework:playwright"] || !seen["service:kafka"] || len(out.CostTable) < 10 {
		t.Fatalf("cost table = %+v", out.CostTable)
	}

	if rec = do(mux, http.MethodGet, "/api/v1/job-sizes", ""); rec.Code != 400 {
		t.Fatalf("no repository: %d", rec.Code)
	}
	if rec = do(mux, http.MethodGet, "/api/v1/job-sizes?repository=nope", ""); rec.Code != 400 {
		t.Fatalf("bad repository: %d", rec.Code)
	}
	rec = do(mux, http.MethodPut, "/api/v1/job-sizes", `{"repository":"o/r","pattern":"integration-*","class":"heavy"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `sizes: { \"integration-*\": heavy }`) || f.set[0] != "o/r|integration-*|heavy" {
		t.Fatalf("put: %d %s %v", rec.Code, rec.Body, f.set)
	}
	if rec = do(mux, http.MethodPut, "/api/v1/job-sizes", `{"repository":"o/r","pattern":"x","class":"huge"}`); rec.Code != 400 {
		t.Fatalf("bad class: %d", rec.Code)
	}
	if rec = do(mux, http.MethodDelete, "/api/v1/job-sizes?repository=o/r&pattern=integration-*", ""); rec.Code != 204 || f.set[1] != "o/r|integration-*|" {
		t.Fatalf("delete: %d %v", rec.Code, f.set)
	}
}

func TestJobSizeRoutesRolesAndUnbuilt(t *testing.T) {
	viewer := sizeMux(t, auth.RoleViewer, &sizeFake{})
	if rec := do(viewer, http.MethodGet, "/api/v1/job-sizes?repository=o/r", ""); rec.Code != 200 {
		t.Fatalf("a viewer reads: %d", rec.Code)
	}
	if rec := do(viewer, http.MethodPut, "/api/v1/job-sizes", `{"repository":"o/r","pattern":"x","class":"light"}`); rec.Code != 403 {
		t.Fatalf("a viewer writes: %d", rec.Code)
	}
	if rec := do(viewer, http.MethodDelete, "/api/v1/job-sizes?repository=o/r&pattern=x", ""); rec.Code != 403 {
		t.Fatalf("a viewer deletes: %d", rec.Code)
	}
	mux := sizeMux(t, auth.RoleOwner, nil)
	if rec := do(mux, http.MethodGet, "/api/v1/job-sizes?repository=o/r", ""); rec.Code != http.StatusNotImplemented {
		t.Fatalf("unbuilt: %d", rec.Code)
	}
}
