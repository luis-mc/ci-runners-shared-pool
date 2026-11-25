package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/depsetup"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
)

type depsFakeLink struct {
	mu       sync.Mutex
	installs int
	checks   int
	undos    int
	restarts int
}

func (f *depsFakeLink) SendCheck(context.Context, string, string, string) (*agentv1.DepsReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
	return &agentv1.DepsReport{Os: "windows", Arch: "x64", Admin: true, DiskFreeBytes: 200 << 30,
		Items: []*agentv1.DepsItem{{Id: "wsl2", State: "ok"}}}, nil
}
func (f *depsFakeLink) SendInstall(context.Context, string, *agentv1.DepsInstall) error {
	f.mu.Lock()
	f.installs++
	f.mu.Unlock()
	return nil
}
func (f *depsFakeLink) SendUndo(context.Context, string, *agentv1.DepsUndo) error {
	f.mu.Lock()
	f.undos++
	f.mu.Unlock()
	return nil
}
func (f *depsFakeLink) SendRestart(context.Context, string, string) error {
	f.mu.Lock()
	f.restarts++
	f.mu.Unlock()
	return nil
}
func (f *depsFakeLink) SendStop(context.Context, string, string) error { return nil }

type depsFakeMachines struct{ info depsetup.MachineInfo }

func (f *depsFakeMachines) Machine(_ context.Context, id string) (depsetup.MachineInfo, error) {
	if id != f.info.ID {
		return depsetup.MachineInfo{}, machines.ErrNotFound
	}
	return f.info, nil
}

type depsAuditNop struct{}

func (depsAuditNop) Audit(context.Context, string, string, string, map[string]any) error { return nil }

func depsMux(t *testing.T, role auth.Role) (*http.ServeMux, *depsFakeLink, *depsFakeMachines, *depsetup.Service) {
	t.Helper()
	l := &depsFakeLink{}
	m := &depsFakeMachines{info: depsetup.MachineInfo{ID: "m1", Name: "pc-1", OS: "windows", Role: "agent", Online: true}}
	list := depsetup.List{Release: "t", Items: []depsetup.Pinned{
		{ID: "docker-in-wsl2", Name: "Docker Engine in WSL2", Version: "apt", OS: "windows", Source: "download.docker.com", DiskBytes: 1 << 30, Undo: "u", ManualSteps: []string{"s"}},
		{ID: "docker-desktop", Name: "Docker Desktop", Version: "4", OS: "windows", Source: "desktop.docker.com", DiskBytes: 3 << 30, NeedsAdmin: true, LicenceNote: "l", Undo: "u", ManualSteps: []string{"s"}},
	}}
	svc := depsetup.NewService(depsetup.Options{Link: l, Repo: depsetup.NewMemRepo(), Audit: depsAuditNop{}, Machines: m, List: list})
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Deps: svc, Authn: asRole(role)})
	return mux, l, m, svc
}

func depsDo(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequestWithContext(machines.WithActor(context.Background(), "tester"), method, path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

const startBody = `{"runner_os":"linux","runner_kind":"persistent","choice_id":"docker-in-wsl2","confirmed":%s}`

func TestDependenciesCheckAndStart(t *testing.T) {
	mux, link, _, _ := depsMux(t, auth.RoleAdmin)
	rec := depsDo(mux, "POST", "/api/v1/machines/m1/dependencies/check", `{"runner_os":"linux","runner_kind":"persistent"}`)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var c map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &c)
	ch := c["choices"].([]any)
	if c["ready"] != false || len(ch) != 2 || ch[0].(map[string]any)["recommended"] != true || ch[1].(map[string]any)["requires_licence_tick"] != true {
		t.Fatalf("%v", c)
	}
	// DEP4: without confirmed the answer is 409 and nothing is sent.
	rec = depsDo(mux, "POST", "/api/v1/machines/m1/dependencies/jobs", strings.Replace(startBody, "%s", "false", 1))
	if rec.Code != 409 || link.installs != 0 || link.checks != 1 {
		t.Fatalf("unconfirmed: %d %s installs %d checks %d", rec.Code, rec.Body, link.installs, link.checks)
	}
	rec = depsDo(mux, "POST", "/api/v1/machines/m1/dependencies/jobs", strings.Replace(startBody, "%s", "true", 1))
	if rec.Code != 202 || link.installs != 1 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	var j map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &j)
	if j["state"] != "consented" || j["actor"] == nil {
		t.Fatalf("%v", j)
	}
	id := j["id"].(string)
	if rec = depsDo(mux, "POST", "/api/v1/machines/m1/dependencies/jobs", strings.Replace(startBody, "%s", "true", 1)); rec.Code != 409 {
		t.Fatalf("second job: %d %s", rec.Code, rec.Body)
	}
	if rec = depsDo(mux, "GET", "/api/v1/machines/m1/dependencies/jobs/"+id, ""); rec.Code != 200 {
		t.Fatalf("get: %d", rec.Code)
	}
	rec = depsDo(mux, "GET", "/api/v1/machines/m1/dependencies/jobs", "")
	var list []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if rec.Code != 200 || len(list) != 1 || list[0]["id"] != id {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	if rec = depsDo(mux, "GET", "/api/v1/machines/m1/dependencies/jobs/nope", ""); rec.Code != 404 {
		t.Fatalf("missing: %d", rec.Code)
	}
}

func TestDependenciesViewerReadsButDoesNotChange(t *testing.T) {
	mux, link, _, svc := depsMux(t, auth.RoleViewer)
	if rec := depsDo(mux, "GET", "/api/v1/machines/m1/dependencies/jobs", ""); rec.Code != 200 {
		t.Fatalf("viewer list: %d %s", rec.Code, rec.Body)
	}
	for _, r := range [][2]string{
		{"/api/v1/machines/m1/dependencies/check", `{"runner_os":"linux","runner_kind":"persistent"}`},
		{"/api/v1/machines/m1/dependencies/jobs", strings.Replace(startBody, "%s", "true", 1)},
		{"/api/v1/machines/m1/dependencies/jobs/x/stop", ""},
		{"/api/v1/machines/m1/dependencies/jobs/x/undo?confirmed=true", ""},
		{"/api/v1/machines/m1/dependencies/jobs/x/restart-host?confirmed=true", ""},
	} {
		if rec := depsDo(mux, "POST", r[0], r[1]); rec.Code != 403 {
			t.Errorf("viewer %s: %d", r[0], rec.Code)
		}
	}
	if link.checks+link.installs+link.undos+link.restarts != 0 {
		t.Fatalf("a viewer reached the agent")
	}
	_ = svc
}

func TestDependenciesOfflineMachineAnswersConflict(t *testing.T) {
	mux, link, m, _ := depsMux(t, auth.RoleAdmin)
	m.info.Online = false
	for _, r := range [][2]string{
		{"/api/v1/machines/m1/dependencies/check", `{"runner_os":"linux","runner_kind":"persistent"}`},
		{"/api/v1/machines/m1/dependencies/jobs", strings.Replace(startBody, "%s", "true", 1)},
	} {
		rec := depsDo(mux, "POST", r[0], r[1])
		if rec.Code != 409 || !strings.Contains(rec.Body.String(), "crsp cannot reach pc-1") {
			t.Errorf("%s: %d %s", r[0], rec.Code, rec.Body)
		}
	}
	if link.checks+link.installs != 0 {
		t.Fatal("an offline machine was called")
	}
	if rec := depsDo(mux, "POST", "/api/v1/machines/zz/dependencies/check", `{"runner_os":"linux","runner_kind":"persistent"}`); rec.Code != 404 {
		t.Fatalf("unknown machine: %d", rec.Code)
	}
}

func TestDependenciesUndoAndRestartNeedYes(t *testing.T) {
	mux, link, m, svc := depsMux(t, auth.RoleAdmin)
	rec := depsDo(mux, "POST", "/api/v1/machines/m1/dependencies/jobs", strings.Replace(startBody, "%s", "true", 1))
	var j map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &j)
	id := j["id"].(string)
	svc.HandleEvent(context.Background(), "m1", &agentv1.DepsEvent{JobId: id, Phase: "install", Final: true, Outcome: "paused_restart",
		Added: []*agentv1.DepsAdded{{ItemId: "wsl2", Kind: "wsl-feature", Ref: "VirtualMachinePlatform", Version: "inbox"}}})
	base := "/api/v1/machines/m1/dependencies/jobs/" + id
	if rec = depsDo(mux, "POST", base+"/restart-host", ""); rec.Code != 409 || link.restarts != 0 {
		t.Fatalf("restart without yes: %d %s", rec.Code, rec.Body)
	}
	m.info.RunningJobs = 1
	if rec = depsDo(mux, "POST", base+"/restart-host?confirmed=true", ""); rec.Code != 409 || link.restarts != 0 {
		t.Fatalf("restart with a running job: %d %s", rec.Code, rec.Body)
	}
	m.info.RunningJobs = 0
	if rec = depsDo(mux, "POST", base+"/restart-host?confirmed=true", ""); rec.Code != 202 || link.restarts != 1 {
		t.Fatalf("restart: %d %s", rec.Code, rec.Body)
	}
	if rec = depsDo(mux, "POST", base+"/undo", ""); rec.Code != 409 || !strings.Contains(rec.Body.String(), "VirtualMachinePlatform") || link.undos != 0 {
		t.Fatalf("undo without yes: %d %s", rec.Code, rec.Body)
	}
	if rec = depsDo(mux, "POST", base+"/undo?confirmed=true", ""); rec.Code != 202 || link.undos != 1 {
		t.Fatalf("undo: %d %s", rec.Code, rec.Body)
	}
}

func TestDependenciesOffWithoutService(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Authn: asRole(auth.RoleAdmin)})
	if rec := depsDo(mux, "GET", "/api/v1/machines/m1/dependencies/jobs", ""); rec.Code != 501 {
		t.Fatalf("%d", rec.Code)
	}
}
