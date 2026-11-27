package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/compare"
)

type fakeCompare struct {
	owner, repo string
	days        int
	rep         compare.Report
	err         error
}

func (f *fakeCompare) Compare(_ context.Context, owner, repo string, days int) (compare.Report, error) {
	f.owner, f.repo, f.days = owner, repo, days
	return f.rep, f.err
}

func compareGet(t *testing.T, svc CompareService, target string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, Options{Version: "test", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Compare: svc, Authn: asRole(auth.RoleViewer)})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil))
	return rec
}

func TestSpeedComparisonJSON(t *testing.T) {
	f := &fakeCompare{rep: compare.Report{Repository: "o/r", Days: 7, Rows: []compare.Row{{Job: "build", MinutesSaved: 3, CostAvoided: 0.5}}, TotalMinutesSaved: 3, TotalCostAvoided: 0.5}}
	rec := compareGet(t, f, "/api/v1/compare?repository=o/r&days=7")
	if rec.Code != 200 || f.owner != "o" || f.repo != "r" || f.days != 7 {
		t.Fatalf("%d %s %+v", rec.Code, rec.Body, f)
	}
	var got struct {
		Repository string `json:"repository"`
		Rows       []struct {
			Job         string  `json:"job"`
			CostAvoided float64 `json:"cost_avoided_usd"`
		} `json:"rows"`
		Total float64 `json:"total_cost_avoided_usd"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Repository != "o/r" || len(got.Rows) != 1 || got.Rows[0].CostAvoided != 0.5 || got.Total != 0.5 {
		t.Fatalf("body: %s", rec.Body)
	}
}

func TestSpeedComparisonErrors(t *testing.T) {
	if rec := compareGet(t, nil, "/api/v1/compare?repository=o/r"); rec.Code != http.StatusNotImplemented {
		t.Errorf("unwired: %d", rec.Code)
	}
	for _, bad := range []string{"/api/v1/compare?repository=nope", "/api/v1/compare?repository=a/b/c", "/api/v1/compare?repository=/r"} {
		if rec := compareGet(t, &fakeCompare{}, bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, rec.Code)
		}
	}
	if rec := compareGet(t, &fakeCompare{err: compare.ErrNotFound}, "/api/v1/compare?repository=o/r"); rec.Code != http.StatusNotFound {
		t.Errorf("not found: %d", rec.Code)
	}
	if rec := compareGet(t, &fakeCompare{err: errors.New("down")}, "/api/v1/compare?repository=o/r"); rec.Code != http.StatusBadGateway {
		t.Errorf("upstream: %d", rec.Code)
	}
}

func TestSpeedComparisonIsReadOnly(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Options{Version: "test", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Compare: &fakeCompare{}, Authn: asRole(auth.RoleOwner)})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/compare?repository=o/r", nil))
	if rec.Code < 400 {
		t.Fatalf("POST answered %d", rec.Code)
	}
}
