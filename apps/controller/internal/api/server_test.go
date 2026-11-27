package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

func newMux(db Pinger) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, Options{Version: "test", DB: db, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Authn: asRole(auth.RoleOwner)})
	return mux
}

func TestHealthOK(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux(fakeDB{}).ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct{ Status, Version string }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "ok" || body.Version != "test" {
		t.Fatalf("body = %+v", body)
	}
}

func TestHealthDatabaseDown(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux(fakeDB{err: errors.New("down")}).ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
	assertEnvelope(t, rec, CodeUnavailable)
}

func TestUnknownAPIRouteUsesEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux(fakeDB{}).ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	assertEnvelope(t, rec, CodeNotFound)
}

func TestRecoverUsesEnvelope(t *testing.T) {
	h := Recover(slog.New(slog.NewTextHandler(io.Discard, nil)), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	assertEnvelope(t, rec, CodeInternal)
}

func assertEnvelope(t *testing.T, rec *httptest.ResponseRecorder, code string) {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type = %q", ct)
	}
	var env ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != code || env.Error.Message == "" {
		t.Fatalf("envelope = %+v, want code %q", env, code)
	}
}
