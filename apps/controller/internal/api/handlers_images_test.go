package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backup"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/settings"
)

type imageMachines struct {
	MachinesService
	views []machines.View
}

func (f imageMachines) List(context.Context) ([]machines.View, error) { return f.views, nil }
func (f imageMachines) Get(_ context.Context, id string) (machines.View, error) {
	for _, v := range f.views {
		if v.Machine.ID == id {
			return v, nil
		}
	}
	return machines.View{}, machines.ErrNotFound
}

func imagesMux() *http.ServeMux { return imagesMuxWith(nil) }

func imagesMuxWith(set *settings.Service) *http.ServeMux {
	docker := []machines.BackendView{{Backend: machines.Backend{Name: "docker", Present: true, Usable: true}}}
	svc := imageMachines{views: []machines.View{
		{Machine: machines.Machine{ID: "m1", Name: "GameRoomPC", OS: "linux"}, Backends: docker},
		{Machine: machines.Machine{ID: "m2", Name: "Mac", OS: "macos"}, Backends: docker},
		{Machine: machines.Machine{ID: "m3", Name: "NoEngine", OS: "linux"}},
	}}
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Caches: &fakeCaches{}, Machines: svc, Settings: set, Authn: asRole(auth.RoleOwner)})
	return mux
}

// LOOK12: a machine that can run container jobs shows an image before the first
// build, with the base tools and every optional layer described.
func TestImageNotBuiltYetShowsContents(t *testing.T) {
	rec := do(imagesMux(), http.MethodGet, "/api/v1/images", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var list gen.ImageList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("want one image (linux machine with an engine), got %d", len(list.Items))
	}
	img := list.Items[0]
	if img.Id != "img_m1_linux" || img.State != gen.ImageStateNotBuilt || img.BuiltAt != nil {
		t.Errorf("image: %+v", img)
	}
	if len(img.BaseTools) == 0 || img.BaseTools[0].Name != "Ubuntu" || img.BaseTools[0].Version == nil || *img.BaseTools[0].Version != "24.04" {
		t.Errorf("base tools: %+v", img.BaseTools)
	}
	var node string
	for _, b := range img.BaseTools {
		if b.Name == "node" && b.Version != nil {
			node = *b.Version
		}
	}
	if node == "" {
		t.Errorf("node has no version: %+v", img.BaseTools)
	}
	if len(img.Layers) != 5 {
		t.Fatalf("layers: %d", len(img.Layers))
	}
	for _, l := range img.Layers {
		if l.Name == "" || l.Purpose == "" || len(l.Adds) == 0 {
			t.Errorf("layer %s lacks words: %+v", l.Id, l)
		}
	}
}

// Ticks and Save work before the first build; Rebuild does not.
func TestImageLayersSaveWithoutBuild(t *testing.T) {
	mux := imagesMux()
	rec := do(mux, http.MethodPost, "/api/v1/images/img_m1_linux/rebuild?confirm=true", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "not built yet") {
		t.Errorf("rebuild: %d %s", rec.Code, rec.Body)
	}
	rec = do(imagesMuxWith(testSettings(t)), http.MethodPut, "/api/v1/images/img_m1_linux/layers", `{"enabled_layers":["nope"]}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown layer: %d %s", rec.Code, rec.Body)
	}
	rec = do(mux, http.MethodGet, "/api/v1/images/img_m2_macos", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("macos: %d", rec.Code)
	}
}

// A saved choice shows ticked on the next read.
func TestImageLayersSaved(t *testing.T) {
	db, err := backup.OpenDBForTests(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mux := imagesMuxWith(settings.New(db))
	rec := do(mux, http.MethodPut, "/api/v1/images/img_m1_linux/layers", `{"enabled_layers":["kvm","llvm"]}`)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var img gen.Image
	if err := json.Unmarshal(rec.Body.Bytes(), &img); err != nil {
		t.Fatal(err)
	}
	var on []string
	for _, l := range img.Layers {
		if l.Enabled {
			on = append(on, l.Id)
		}
	}
	if strings.Join(on, ",") != "llvm,kvm" {
		t.Errorf("enabled: %v", on)
	}
}

func testSettings(t *testing.T) *settings.Service {
	t.Helper()
	db, err := backup.OpenDBForTests(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return settings.New(db)
}
