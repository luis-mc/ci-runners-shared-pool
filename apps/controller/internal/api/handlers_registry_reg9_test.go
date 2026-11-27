package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/mirror"
)

// memMirrorStore is the settings store in memory, with the defaults of migration 00019.
type memMirrorStore struct{ s registry.MirrorSettings }

func (f *memMirrorStore) MirrorSettings(context.Context) (registry.MirrorSettings, error) {
	return f.s, nil
}

func (f *memMirrorStore) PutMirrorSettings(_ context.Context, s registry.MirrorSettings, _ string) error {
	f.s = s
	return nil
}

func mirrorRig(t *testing.T, role auth.Role) (*http.ServeMux, *mirror.Mirror, string) {
	t.Helper()
	dir := t.TempDir()
	db := &memMirrorStore{s: registry.MirrorSettings{Enabled: true, Upstreams: mirror.DefaultUpstreams, TagRecheck: mirror.DefaultTagRecheck, BudgetBytes: 20e9}}
	m := mirror.New(mirror.Options{DataDir: dir})
	t.Cleanup(func() { _ = m.Close() })
	svc := &mirror.Service{Store: db, Mirror: m, DataDir: dir}
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), RegistryMirror: svc, Authn: asRole(role)})
	return mux, m, dir
}

type mirrorBody struct {
	Enabled           bool     `json:"enabled"`
	Upstreams         []string `json:"upstreams"`
	Folder            string   `json:"folder"`
	BudgetBytes       int64    `json:"budget_bytes"`
	TagRecheckMinutes int      `json:"tag_recheck_minutes"`
	UsedBytes         int64    `json:"used_bytes"`
	Path              string   `json:"path"`
}

// Registry R5.63, R5.64, R5.66: the defaults, then a change that round-trips and
// is applied to the running mirror.
func TestRegistryMirrorSettingsRoundTrip(t *testing.T) {
	mux, m, dir := mirrorRig(t, auth.RoleOwner)
	rec := do(mux, http.MethodGet, "/api/v1/registry/mirror", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got mirrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || strings.Join(got.Upstreams, ",") != "docker.io,ghcr.io,mcr.microsoft.com,quay.io" ||
		got.TagRecheckMinutes != 60 || got.Folder != filepath.Join(dir, "registry-mirror") || got.Path != "/mirror" {
		t.Fatalf("defaults: %+v", got)
	}
	put := `{"enabled":true,"upstreams":["Docker.io","registry.example.com:5000","docker.io"],"budget_bytes":1000000,"tag_recheck_minutes":15}`
	rec = do(mux, http.MethodPut, "/api/v1/registry/mirror", put)
	if rec.Code != 200 {
		t.Fatalf("put %d %s", rec.Code, rec.Body)
	}
	rec = do(mux, http.MethodGet, "/api/v1/registry/mirror", "")
	got = mirrorBody{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Upstreams, ",") != "docker.io,registry.example.com:5000" || got.BudgetBytes != 1000000 || got.TagRecheckMinutes != 15 {
		t.Fatalf("saved: %+v", got)
	}
	if s := m.Settings(); len(s.Upstreams) != 2 || s.TagRecheck.Minutes() != 15 {
		t.Fatalf("not applied: %+v", s)
	}
	// The storage folder of an upstream is its own, under the mirror folder (R5.64).
	if _, err := os.Stat(filepath.Join(dir, "registry-mirror", "registry.example.com_5000")); err != nil {
		t.Fatal(err)
	}
	// Off stops serving.
	if rec = do(mux, http.MethodPut, "/api/v1/registry/mirror", `{"enabled":false,"upstreams":[],"budget_bytes":0,"tag_recheck_minutes":60}`); rec.Code != 200 {
		t.Fatalf("off %d %s", rec.Code, rec.Body)
	}
	if m.Settings().Enabled {
		t.Fatal("still enabled")
	}
}

func TestRegistryMirrorRefusesWhatCannotRun(t *testing.T) {
	mux, _, _ := mirrorRig(t, auth.RoleOwner)
	for _, body := range []string{
		`{"enabled":true,"upstreams":[],"budget_bytes":0,"tag_recheck_minutes":60}`,
		`{"enabled":true,"upstreams":["https://docker.io/x"],"budget_bytes":0,"tag_recheck_minutes":60}`,
		`{"enabled":true,"upstreams":["docker.io"],"budget_bytes":0,"tag_recheck_minutes":0}`,
	} {
		if rec := do(mux, http.MethodPut, "/api/v1/registry/mirror", body); rec.Code != 400 {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	// Nothing was saved.
	rec := do(mux, http.MethodGet, "/api/v1/registry/mirror", "")
	if !strings.Contains(rec.Body.String(), `"docker.io"`) {
		t.Fatalf("settings changed: %s", rec.Body)
	}
}

func TestRegistryMirrorSettingsNeedAnAdminToChange(t *testing.T) {
	mux, _, _ := mirrorRig(t, auth.RoleViewer)
	if rec := do(mux, http.MethodGet, "/api/v1/registry/mirror", ""); rec.Code != 200 {
		t.Fatalf("viewer read %d", rec.Code)
	}
	if rec := do(mux, http.MethodPut, "/api/v1/registry/mirror", `{"enabled":false,"upstreams":[],"budget_bytes":0,"tag_recheck_minutes":60}`); rec.Code != 403 {
		t.Fatalf("viewer change %d %s", rec.Code, rec.Body)
	}
}

func TestRegistryMirrorWithoutServiceAnswers501(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Authn: asRole(auth.RoleOwner)})
	if rec := do(mux, http.MethodGet, "/api/v1/registry/mirror", ""); rec.Code != 501 {
		t.Fatalf("%d", rec.Code)
	}
}
