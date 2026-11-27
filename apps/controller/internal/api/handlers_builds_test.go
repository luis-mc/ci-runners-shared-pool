package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
)

type fakeBuilds struct {
	machine string
	items   []gen.ImageBuild
}

func (f *fakeBuilds) List(_ context.Context, machineID string, _ time.Time) ([]gen.ImageBuild, error) {
	f.machine = machineID
	return f.items, nil
}

func (f *fakeBuilds) Get(_ context.Context, id string, _ time.Time) (gen.ImageBuild, bool, error) {
	for _, b := range f.items {
		if b.Id == id {
			return b, true, nil
		}
	}
	return gen.ImageBuild{}, false, nil
}

func buildsMux(svc BuildsService) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Builds: svc, Authn: asRole(auth.RoleOwner)})
	return mux
}

func getJSON(t *testing.T, mux *http.ServeMux, path string, into any) int {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
	if into != nil && rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code
}

// R5.70: listImageBuilds and getImageBuild read the records.
func TestListAndGetImageBuild(t *testing.T) {
	left := float32(42)
	f := &fakeBuilds{items: []gen.ImageBuild{{Id: "bld_1", MachineId: "m1", Key: "abc", State: "building", PushState: "none", Step: 3, StepsTotal: 9, SecondsLeft: &left}}}
	mux := buildsMux(f)

	var list gen.ImageBuildList
	if code := getJSON(t, mux, "/api/v1/registry/builds?machine_id=m1", &list); code != 200 || len(list.Items) != 1 || f.machine != "m1" {
		t.Fatalf("list: %d %+v machine=%q", code, list, f.machine)
	}
	var one gen.ImageBuild
	if code := getJSON(t, mux, "/api/v1/registry/builds/bld_1", &one); code != 200 || one.Step != 3 || one.SecondsLeft == nil || *one.SecondsLeft != 42 {
		t.Fatalf("get: %d %+v", code, one)
	}
	if code := getJSON(t, mux, "/api/v1/registry/builds/bld_nope", nil); code != http.StatusNotFound {
		t.Fatalf("missing build: %d", code)
	}
}

func TestBuildsWithoutServiceAnswers501(t *testing.T) {
	mux := buildsMux(nil)
	if code := getJSON(t, mux, "/api/v1/registry/builds", nil); code != http.StatusNotImplemented {
		t.Fatalf("list: %d", code)
	}
	if code := getJSON(t, mux, "/api/v1/registry/builds/bld_1", nil); code != http.StatusNotImplemented {
		t.Fatalf("get: %d", code)
	}
}
