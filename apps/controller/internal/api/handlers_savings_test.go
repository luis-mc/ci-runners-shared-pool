package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/history"
)

type fakeSavings struct{ name string }

func (f *fakeSavings) Report(_ context.Context, name string, _, _ time.Time) (history.Report, error) {
	f.name = name
	return history.Report{Range: "today", Total: history.Row{Key: "all", Jobs: 1, Minutes: 3, Money: 0.024},
		SourceURL: history.ShippedSourceURL, CopiedOn: history.ShippedCopiedOn, Note: "Estimate."}, nil
}

type fakeRates struct {
	t     history.Table
	reset bool
}

func (f *fakeRates) Get(context.Context) (history.Table, error) { return f.t, nil }
func (f *fakeRates) Set(_ context.Context, _ audit.Actor, t history.Table) (history.Table, error) {
	f.t = t
	return t, nil
}
func (f *fakeRates) Reset(context.Context, audit.Actor) (history.Table, error) {
	f.reset = true
	return history.Shipped(), nil
}

func savingsCall(t *testing.T, role auth.Role, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, Options{Version: "test", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Savings: &fakeSavings{}, SavingsRates: &fakeRates{t: history.Shipped()}, Authn: asRole(role)})
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequestWithContext(context.Background(), method, target, rd)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestSavingsReportJSON(t *testing.T) {
	rec := savingsCall(t, auth.RoleViewer, http.MethodGet, "/api/v1/savings?range=today", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var out struct {
		Total     struct{ Minutes int }
		SourceURL string `json:"source_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Total.Minutes != 3 || out.SourceURL == "" {
		t.Fatalf("%v %s", err, rec.Body)
	}
}

func TestSavingsRatesAreOwnerOnlyAndValidated(t *testing.T) {
	if rec := savingsCall(t, auth.RoleViewer, http.MethodGet, "/api/v1/savings/rates", nil); rec.Code != http.StatusOK {
		t.Fatalf("viewer read: %d", rec.Code)
	}
	good := map[string]any{"source_url": "x", "copied_on": "x", "changed": false, "machine_sizes": map[string]string{},
		"rates": []map[string]any{{"os": "linux", "size": "standard", "per_minute": 0.01}}}
	if rec := savingsCall(t, auth.RoleViewer, http.MethodPut, "/api/v1/savings/rates", good); rec.Code == http.StatusOK {
		t.Fatal("a viewer must not change rates")
	}
	if rec := savingsCall(t, auth.RoleOwner, http.MethodPut, "/api/v1/savings/rates", good); rec.Code != http.StatusOK {
		t.Fatalf("owner change: %d %s", rec.Code, rec.Body)
	}
	bad := map[string]any{"source_url": "x", "copied_on": "x", "changed": false, "machine_sizes": map[string]string{},
		"rates": []map[string]any{{"os": "linux", "size": "standard", "per_minute": -1}}}
	if rec := savingsCall(t, auth.RoleOwner, http.MethodPut, "/api/v1/savings/rates", bad); rec.Code != http.StatusBadRequest {
		t.Fatalf("negative rate: %d", rec.Code)
	}
	if rec := savingsCall(t, auth.RoleOwner, http.MethodDelete, "/api/v1/savings/rates", nil); rec.Code != http.StatusOK {
		t.Fatalf("reset: %d", rec.Code)
	}
}
