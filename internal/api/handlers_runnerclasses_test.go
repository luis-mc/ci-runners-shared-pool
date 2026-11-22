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
	"github.com/luis-mc/ci-runners-shared-pool/internal/runners"
)

type classMem struct{ c runners.ClassSizes }

func (m *classMem) ClassSizes(context.Context) runners.ClassSizes { return m.c }
func (m *classMem) SaveClassSizes(_ context.Context, c runners.ClassSizes, _ string) error {
	m.c = c
	return nil
}
func (m *classMem) ReserveLimits(context.Context) (int, int)                  { return 3, 10 }
func (m *classMem) SaveReserveLimits(context.Context, int, int, string) error { return nil }

func classMux(t *testing.T, role auth.Role, svc RunnerClassService) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), RunnerClasses: svc, Authn: asRole(role)})
	return mux
}

// The class routes (scheduling R5.89 to R5.93): the list has the four classes
// and the defaults; a bad save is a 400 in the owner's words; only an admin writes.
func TestRunnerClassRoutesContract(t *testing.T) {
	svc := &runners.ClassService{Store: &classMem{c: runners.TableSizes()}}
	mux := classMux(t, auth.RoleOwner, svc)
	rec := do(mux, http.MethodGet, "/api/v1/runner-classes", "")
	if rec.Code != 200 {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Preset  string `json:"preset"`
		Classes []struct {
			Class     string `json:"class"`
			Cpus      int    `json:"cpus"`
			MemoryGib int    `json:"memory_gib"`
		} `json:"classes"`
		Passes  int `json:"reserve_after_passes"`
		Minutes int `json:"reserve_after_minutes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Preset != "standard" || len(out.Classes) != 4 || out.Classes[3].Class != "xl" || out.Classes[3].MemoryGib != 32 || out.Passes != 3 || out.Minutes != 10 {
		t.Fatalf("body %s", rec.Body)
	}
	bad := `{"classes":[{"class":"small","cpus":2,"memory_gib":4},{"class":"medium","cpus":4,"memory_gib":8},{"class":"large","cpus":3,"memory_gib":16},{"class":"xl","cpus":16,"memory_gib":32}]}`
	rec = do(mux, http.MethodPut, "/api/v1/runner-classes", bad)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "Large has 3 CPUs; it must have more than medium's 4. Sizes grow from small to xl.") {
		t.Fatalf("bad save: %d %s", rec.Code, rec.Body)
	}
	if rec = do(mux, http.MethodPut, "/api/v1/runner-classes", `{"classes":[{"class":"small","cpus":1,"memory_gib":1}]}`); rec.Code != 400 {
		t.Fatalf("partial: %d", rec.Code)
	}
	if rec = do(mux, http.MethodPut, "/api/v1/runner-classes", `{"preset":"memory-heavy","reserve_after_minutes":15}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"memory-heavy"`) {
		t.Fatalf("preset: %d %s", rec.Code, rec.Body)
	}
	if rec = do(mux, http.MethodPut, "/api/v1/runner-classes", `{"reserve_after_passes":0}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), "Enter whole numbers above 0.") {
		t.Fatalf("zero: %d %s", rec.Code, rec.Body)
	}
}

func TestRunnerClassRoutesRolesAndUnbuilt(t *testing.T) {
	svc := &runners.ClassService{Store: &classMem{c: runners.TableSizes()}}
	if rec := do(classMux(t, auth.RoleViewer, svc), http.MethodPut, "/api/v1/runner-classes", `{"preset":"compact"}`); rec.Code != 403 {
		t.Fatalf("viewer write: %d", rec.Code)
	}
	if rec := do(classMux(t, auth.RoleViewer, svc), http.MethodGet, "/api/v1/runner-classes", ""); rec.Code != 200 {
		t.Fatalf("viewer read: %d", rec.Code)
	}
	if rec := do(classMux(t, auth.RoleOwner, nil), http.MethodGet, "/api/v1/runner-classes", ""); rec.Code != 501 {
		t.Fatalf("unbuilt: %d", rec.Code)
	}
}
