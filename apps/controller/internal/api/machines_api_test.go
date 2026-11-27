package api

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

type fakeCA struct{}

func (fakeCA) CAPEM() []byte {
	return []byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n")
}

func machinesMux(t *testing.T) (*http.ServeMux, *machines.MemRepo, string) {
	mux, repo, id, _ := machinesMuxSvc(t)
	return mux, repo, id
}

func machinesMuxSvc(t *testing.T) (*http.ServeMux, *machines.MemRepo, string, *machines.Service) {
	t.Helper()
	repo := machines.NewMemRepo()
	svc := machines.New(machines.Options{Repo: repo, ControllerAddress: "pool.lan", AgentPort: 8461, Fingerprint: strings.Repeat("ab", 32)})
	// A machine that joined: through the service, as the agent link does.
	code, err := svc.CreateJoinCode(context.Background(), "owner", 0)
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Join(context.Background(), machines.JoinInput{Code: code.Code, Facts: machines.Facts{HostName: "box-1", OS: "linux", Arch: "x64", Threads: 8, MemoryBytes: 32 << 30}})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Register(mux, Options{Version: "test", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Machines: svc, CA: fakeCA{}, Authn: asRole(auth.RoleOwner)})
	return mux, repo, res.MachineID, svc
}

func doM(t *testing.T, mux *http.ServeMux, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequestWithContext(machines.WithActor(context.Background(), "tester"), method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestListAndGetMachine(t *testing.T) {
	mux, _, id := machinesMux(t)
	rec := doM(t, mux, "GET", "/api/v1/machines", nil)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var list gen.MachineList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Items) != 1 {
		t.Fatalf("%v %s", err, rec.Body)
	}
	m := list.Items[0]
	if m.Name != "box-1" || m.Os != "linux" || m.State != gen.MachineStateActive || m.Settings.BudgetMemoryPct != 85 || m.Settings.BudgetCpuPct != 60 ||
		len(m.Settings.AllowedBackends) != 1 || m.Online {
		t.Fatalf("machine = %+v", m)
	}
	if rec := doM(t, mux, "GET", "/api/v1/machines/"+id, nil); rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	rec = doM(t, mux, "GET", "/api/v1/machines/m_nope", nil)
	if rec.Code != 404 {
		t.Fatalf("%d", rec.Code)
	}
	assertEnvelope(t, rec, CodeNotFound)
}

func TestUpdateDrainResumeAndConfirmation(t *testing.T) {
	mux, repo, id := machinesMux(t)
	// A budget change saves at once.
	rec := doM(t, mux, "PATCH", "/api/v1/machines/"+id, map[string]any{"budget_memory_pct": 70, "job_slots": 3})
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var m gen.Machine
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	if m.Settings.BudgetMemoryPct != 70 || m.Settings.JobSlots != 3 {
		t.Fatalf("settings = %+v", m.Settings)
	}
	// Out of range: 400.
	rec = doM(t, mux, "PATCH", "/api/v1/machines/"+id, map[string]any{"budget_cpu_pct": 99})
	if rec.Code != 400 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	assertEnvelope(t, rec, CodeBadRequest)
	// Drain answers 202 with the change.
	rec = doM(t, mux, "POST", "/api/v1/machines/"+id+"/drain", map[string]any{"reason": "maintenance"})
	if rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var ch gen.MachineChange
	_ = json.Unmarshal(rec.Body.Bytes(), &ch)
	if ch.Kind != gen.MachineChangeKindDrain || ch.State != gen.MachineChangeStateWaiting || ch.WaitingFor == nil {
		t.Fatalf("change = %+v", ch)
	}
	if rec := doM(t, mux, "GET", "/api/v1/machines/"+id+"/changes/"+ch.Id, nil); rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	// Resume needs a yes while the change waits: 409 needs_confirmation with details.
	rec = doM(t, mux, "POST", "/api/v1/machines/"+id+"/resume", nil)
	if rec.Code != 409 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var env gen.ErrorEnvelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != CodeNeedsConfirmation || env.Error.Details == nil || len(*env.Error.Details) == 0 {
		t.Fatalf("envelope = %+v", env)
	}
	rec = doM(t, mux, "POST", "/api/v1/machines/"+id+"/resume?confirm=true", nil)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	// The audit log has who did it (the actor of the request).
	var actors []string
	for _, a := range repo.AuditLog() {
		actors = append(actors, a.Actor+":"+a.Action)
	}
	if !strings.Contains(strings.Join(actors, ","), "tester") {
		t.Fatalf("audit actors = %v", actors)
	}
	// An offline machine cannot be asked to check again.
	if rec := doM(t, mux, "POST", "/api/v1/machines/"+id+"/redetect", nil); rec.Code != 409 {
		t.Fatalf("redetect offline: %d", rec.Code)
	}
}

func TestRemoveNamesWhatGoesThenRemoves(t *testing.T) {
	mux, _, id := machinesMux(t)
	rec := doM(t, mux, "DELETE", "/api/v1/machines/"+id, nil)
	if rec.Code != 409 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var env gen.ErrorEnvelope
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != CodeNeedsConfirmation || env.Error.Details == nil || !strings.Contains((*env.Error.Details)[0].Text, "box-1") {
		t.Fatalf("envelope = %+v", env)
	}
	rec = doM(t, mux, "DELETE", "/api/v1/machines/"+id+"?confirm=true", nil)
	if rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var ch gen.MachineChange
	_ = json.Unmarshal(rec.Body.Bytes(), &ch)
	if ch.Kind != gen.MachineChangeKindRemove || ch.State != gen.MachineChangeStateDone {
		t.Fatalf("an offline machine is removed at once: %+v", ch)
	}
	if rec := doM(t, mux, "GET", "/api/v1/machines/"+id, nil); rec.Code != 404 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestJoinCodeRoutes(t *testing.T) {
	mux, _, _ := machinesMux(t)
	rec := doM(t, mux, "POST", "/api/v1/join-codes", map[string]any{"ttl_minutes": 30})
	if rec.Code != 201 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var created gen.JoinCodeCreated
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Code == "" || created.JoinCode.State != gen.JoinCodeStateWaiting || created.JoinCode.ExpiresAt.Sub(created.JoinCode.CreatedAt) != 30*time.Minute {
		t.Fatalf("created = %+v", created)
	}
	if len(created.Commands.Items) != 3 || created.Commands.AgentPort != 8461 || created.Commands.ControllerAddress != "pool.lan" {
		t.Fatalf("commands = %+v", created.Commands)
	}
	for _, c := range created.Commands.Items {
		if !strings.Contains(c.Command, created.Code) || !strings.Contains(c.Command, created.Commands.CertSha256) {
			t.Fatalf("command %q lacks the code or the fingerprint", c.Command)
		}
	}
	// The state route never returns the code.
	rec = doM(t, mux, "GET", "/api/v1/join-codes/"+created.JoinCode.Id, nil)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), created.Code) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := doM(t, mux, "DELETE", "/api/v1/join-codes/"+created.JoinCode.Id, nil); rec.Code != 204 {
		t.Fatalf("revoke: %d", rec.Code)
	}
	var st gen.JoinCode
	rec = doM(t, mux, "GET", "/api/v1/join-codes/"+created.JoinCode.Id, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if st.State != gen.JoinCodeStateRevoked {
		t.Fatalf("state = %v", st.State)
	}
	if rec := doM(t, mux, "DELETE", "/api/v1/join-codes/"+created.JoinCode.Id, nil); rec.Code != 404 {
		t.Fatalf("revoke twice: %d", rec.Code)
	}
	if rec := doM(t, mux, "POST", "/api/v1/join-codes", map[string]any{"ttl_minutes": 2000}); rec.Code != 400 {
		t.Fatalf("ttl above 24 hours: %d", rec.Code)
	}
}

// R5.3: the install command per OS, without a join code.
func TestInstallCommandAndCADownload(t *testing.T) {
	mux, _, _ := machinesMux(t)
	rec := doM(t, mux, "GET", "/api/v1/install-command", nil)
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	var ic gen.InstallCommands
	_ = json.Unmarshal(rec.Body.Bytes(), &ic)
	if len(ic.Items) != 3 || !strings.Contains(ic.Items[0].Command, "<join code>") || ic.CertSha256 == "" {
		t.Fatalf("%+v", ic)
	}
	rec = doM(t, mux, "GET", "/api/v1/ca.pem", nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/x-pem-file" || !strings.HasPrefix(rec.Body.String(), "-----BEGIN CERTIFICATE-----") {
		t.Fatalf("%d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
}

func TestMachineRoutesWithoutAServiceAnswerNotImplemented(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux(fakeDB{}).ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/machines", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("%d", rec.Code)
	}
	assertEnvelope(t, rec, CodeNotImplemented)
}

func TestLoadHistoryRoute(t *testing.T) {
	mux, _, id := machinesMux(t)
	rec := doM(t, mux, "GET", "/api/v1/machines/"+id+"/load", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := doM(t, mux, "GET", "/api/v1/machines/m_none/load", nil); rec.Code != 404 {
		t.Fatalf("%d", rec.Code)
	}
}

// CRSP127: the machine says which OS its containers run once the agent reported
// a usable engine; before that, and when the engine is down, the field is absent.
func TestMachineShowsContainerOS(t *testing.T) {
	mux, _, id, svc := machinesMuxSvc(t)
	get := func() gen.Machine {
		rec := doM(t, mux, "GET", "/api/v1/machines/"+id, nil)
		var m gen.Machine
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if get().ContainerOs != nil {
		t.Fatal("container_os before any report")
	}
	svc.HandleMachineReport(id, &agentv1.MachineReport{Engine: &agentv1.EngineReport{Kind: "docker-desktop", Usable: true, ContainerOs: "linux"}})
	if m := get(); m.ContainerOs == nil || *m.ContainerOs != gen.OS("linux") {
		t.Fatalf("container_os = %v", m.ContainerOs)
	}
	svc.HandleMachineReport(id, &agentv1.MachineReport{Engine: &agentv1.EngineReport{Kind: "docker-desktop"}})
	if get().ContainerOs != nil {
		t.Fatal("container_os with the engine down")
	}
}

// CRSP235: the console saves Gate maximum wait (1 or 2 minutes), and Restart the
// service refuses what it cannot do with a conflict, never with a cut job.
func TestGateMaxWaitSavesWithinTheCapAndRestartRefusesOffline(t *testing.T) {
	mux, _, id := machinesMux(t)
	rec := doM(t, mux, "PATCH", "/api/v1/machines/"+id, map[string]any{"gate_max_wait_minutes": 1})
	var m gen.Machine
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &m) != nil || m.Settings.GateMaxWaitMinutes == nil || *m.Settings.GateMaxWaitMinutes != 1 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	for _, bad := range []int{0, 3, 30} {
		if rec := doM(t, mux, "PATCH", "/api/v1/machines/"+id, map[string]any{"gate_max_wait_minutes": bad}); rec.Code != 400 {
			t.Fatalf("%d: %d %s", bad, rec.Code, rec.Body)
		}
	}
	// The agent is not connected: nothing is started.
	rec = doM(t, mux, "POST", "/api/v1/machines/"+id+"/restart?confirmed=true", nil)
	if rec.Code != 409 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := doM(t, mux, "POST", "/api/v1/machines/m_nope/restart?confirmed=true", nil); rec.Code != 404 {
		t.Fatalf("%d", rec.Code)
	}
}
