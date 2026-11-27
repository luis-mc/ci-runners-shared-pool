package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backup"
	"github.com/luis-mc/ci-runners-shared-pool/internal/metrics"
	"github.com/luis-mc/ci-runners-shared-pool/internal/secrets"
)

func metricsMux(t *testing.T, role auth.Role) (*http.ServeMux, *metrics.Service) {
	t.Helper()
	db, err := backup.OpenDBForTests(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := &metrics.Service{
		Store: metrics.DBStore{DB: db}, Token: metrics.SecretToken{Secrets: secrets.NewMemory()},
		Source: metrics.SourceFunc(func(context.Context) (metrics.Snapshot, error) { return metrics.Snapshot{}, nil }),
		Start:  func(string, http.Handler) (*metrics.Server, error) { return nil, nil },
	}
	t.Cleanup(svc.Close)
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: okPinger{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Audit: audit.New(db), Metrics: svc, Authn: asRole(role)})
	return mux, svc
}

func metricsPut(mux *http.ServeMux, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "PUT", "/api/v1/settings/metrics", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	return rec
}

const metricsOff = `{"enabled":false,"port":9464,"expose":false,"key_required":false,"push_enabled":false,"push_endpoint":"","push_seconds":60}`

func TestMetricsSettingsAdminOnly(t *testing.T) {
	mux, _ := metricsMux(t, auth.RoleViewer)
	if rec := doReq(mux, "GET", "/api/v1/settings/metrics"); rec.Code != 403 {
		t.Fatalf("get: %d", rec.Code)
	}
	if rec := metricsPut(mux, metricsOff); rec.Code != 403 {
		t.Fatalf("put: %d", rec.Code)
	}
	if rec := doReq(mux, "POST", "/api/v1/settings/metrics/token"); rec.Code != 403 {
		t.Fatalf("token: %d", rec.Code)
	}
}

func TestMetricsSettingsOffByDefault(t *testing.T) {
	mux, _ := metricsMux(t, auth.RoleAdmin)
	rec := doReq(mux, "GET", "/api/v1/settings/metrics")
	b := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(b, `"enabled":false`) || !strings.Contains(b, `"expose":false`) ||
		!strings.Contains(b, `"key_made":false`) || !strings.Contains(b, "scrape_configs") {
		t.Fatalf("%d %s", rec.Code, b)
	}
}

func TestMetricsSettingsPutChecksAndKeeps(t *testing.T) {
	mux, _ := metricsMux(t, auth.RoleAdmin)
	if rec := metricsPut(mux, strings.Replace(metricsOff, `"port":9464`, `"port":22`, 1)); rec.Code != 400 {
		t.Fatalf("bad port: %d %s", rec.Code, rec.Body.String())
	}
	needsToken := strings.Replace(strings.Replace(metricsOff, `"enabled":false`, `"enabled":true`, 1), `"key_required":false`, `"key_required":true`, 1)
	if rec := metricsPut(mux, needsToken); rec.Code != 409 {
		t.Fatalf("no token yet: %d %s", rec.Code, rec.Body.String())
	}
	rec := doReq(mux, "POST", "/api/v1/settings/metrics/token")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"key":"`) {
		t.Fatalf("token: %d %s", rec.Code, rec.Body.String())
	}
	tok := rec.Body.String()
	if rec := metricsPut(mux, metricsOff); rec.Code != 200 {
		t.Fatalf("put: %d %s", rec.Code, rec.Body.String())
	}
	got := doReq(mux, "GET", "/api/v1/settings/metrics")
	if !strings.Contains(got.Body.String(), `"key_made":true`) || strings.Contains(got.Body.String(), strings.TrimSuffix(strings.TrimPrefix(tok, `{"key":"`), `"}`+"\n")) {
		t.Fatalf("the token must never come back: %s", got.Body.String())
	}
}
