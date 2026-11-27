package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/envsets"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/redact"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/regcreds"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

type layerFake map[string]envsets.Layers

func (l layerFake) Layers(_ context.Context, id string) (envsets.Layers, error) {
	v, ok := l[id]
	if !ok {
		return envsets.Layers{}, runners.ErrNotFound
	}
	return v, nil
}

const sensitiveValue = "tok-live-0123456789"

func envMux(t *testing.T, role auth.Role) (*http.ServeMux, *secrets.Memory) {
	t.Helper()
	sm := secrets.NewMemory()
	reg := &envsets.Registry{Meta: &envsets.MemoryMeta{}, Secrets: sm, Redactor: redact.New()}
	svc := &envsets.Service{Registry: reg, Source: layerFake{"r1": {
		Machine: []envsets.Var{{Name: "DOTNET_ROOT", Value: "/machine"}},
		Sets:    []string{"dotnet-8"},
		Runner:  []envsets.Var{{Name: "MINE", Value: "yes"}},
	}}}
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		EnvSets: svc, Registry: &regcreds.Store{Meta: &regcreds.MemoryMeta{}, Secrets: sm, Redactor: redact.New()}, Authn: asRole(role)})
	return mux, sm
}

func TestEnvSetRoutesMaskSensitiveAndMergeForARunner(t *testing.T) {
	mux, _ := envMux(t, auth.RoleOwner)
	body := `{"vars":[{"name":"DOTNET_ROOT","value":"/set"},{"name":"FEED_LOGIN","value":"` + sensitiveValue + `","sensitive":true}]}`
	rec := do(mux, http.MethodPut, "/api/v1/env-sets/dotnet-8", body)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), sensitiveValue) {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	if rec = do(mux, http.MethodGet, "/api/v1/env-sets", ""); rec.Code != 200 || strings.Contains(rec.Body.String(), sensitiveValue) || !strings.Contains(rec.Body.String(), "dotnet-8") {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	rec = do(mux, http.MethodGet, "/api/v1/runners/r1/env", "")
	if rec.Code != 200 || strings.Contains(rec.Body.String(), sensitiveValue) {
		t.Fatalf("env: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Vars []struct {
			Name, Value, Source string
			Sensitive           bool
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	got := map[string][2]string{}
	for _, v := range out.Vars {
		got[v.Name] = [2]string{v.Value, v.Source}
	}
	want := map[string][2]string{"DOTNET_ROOT": {"/set", "set:dotnet-8"}, "FEED_LOGIN": {"***", "set:dotnet-8"}, "MINE": {"yes", "runner"}}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %v, want %v", k, got[k], w)
		}
	}
	if rec = do(mux, http.MethodGet, "/api/v1/runners/nope/env", ""); rec.Code != 404 {
		t.Errorf("unknown runner: %d", rec.Code)
	}
	if rec = do(mux, http.MethodPut, "/api/v1/env-sets/x", `{"vars":[{"name":"GITHUB_ENV","value":"a"}]}`); rec.Code != 400 {
		t.Errorf("reserved: %d %s", rec.Code, rec.Body)
	}
	if rec = do(mux, http.MethodDelete, "/api/v1/env-sets/dotnet-8", ""); rec.Code != 204 {
		t.Errorf("delete: %d", rec.Code)
	}
}

func TestEnvSetWriteNeedsAdmin(t *testing.T) {
	mux, _ := envMux(t, auth.RoleViewer)
	if rec := do(mux, http.MethodPut, "/api/v1/env-sets/x", `{"vars":[]}`); rec.Code != http.StatusForbidden {
		t.Fatalf("put as user: %d", rec.Code)
	}
	if rec := do(mux, http.MethodGet, "/api/v1/registry", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("registry as user: %d", rec.Code)
	}
}

func TestRegistryRoutesNeverReturnThePassword(t *testing.T) {
	mux, sm := envMux(t, auth.RoleAdmin)
	rec := do(mux, http.MethodPut, "/api/v1/registry/logins", `{"kind":"container","host":"reg.example.com","username":"ci","password":"`+sensitiveValue+`"}`)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), sensitiveValue) || strings.Contains(rec.Body.String(), "credential") {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	if len(sm.Keys()) != 1 {
		t.Fatalf("keys %v", sm.Keys())
	}
	if rec = do(mux, http.MethodGet, "/api/v1/registry", ""); rec.Code != 200 || strings.Contains(rec.Body.String(), sensitiveValue) || !strings.Contains(rec.Body.String(), `"ca_bundle_set":false`) {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	if rec = do(mux, http.MethodPut, "/api/v1/registry/ca-bundle", `{"pem":"not a cert"}`); rec.Code != 400 {
		t.Fatalf("bad bundle: %d", rec.Code)
	}
	if rec = do(mux, http.MethodDelete, "/api/v1/registry/logins/container/reg.example.com", ""); rec.Code != 204 || len(sm.Keys()) != 0 {
		t.Fatalf("delete: %d %v", rec.Code, sm.Keys())
	}
	if rec = do(mux, http.MethodDelete, "/api/v1/registry/logins/container/reg.example.com", ""); rec.Code != 404 {
		t.Fatalf("delete again: %d", rec.Code)
	}
}

var _ = httptest.NewRecorder
