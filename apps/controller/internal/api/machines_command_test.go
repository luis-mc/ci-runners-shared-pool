package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// cli R5.29: the command route refuses a machine that is not drained (409), a
// build without a host runner (501) and an empty command (400); it never
// reaches an agent in any of those cases.
func TestRunMachineCommandRefusals(t *testing.T) {
	mux, _, id := machinesMux(t)
	body := map[string]any{"command": []string{"docker-compact"}}
	// No host runner wired: 501.
	if rec := doM(t, mux, "POST", "/api/v1/machines/"+id+"/command", body); rec.Code != 501 {
		t.Fatalf("no runner: %d %s", rec.Code, rec.Body)
	}
	if rec := doM(t, mux, "POST", "/api/v1/machines/m_nope/command", body); rec.Code != 404 && rec.Code != 501 {
		t.Fatalf("unknown machine: %d %s", rec.Code, rec.Body)
	}
	if rec := doM(t, mux, "POST", "/api/v1/machines/"+id+"/command", map[string]any{"command": []string{}}); rec.Code != 400 {
		t.Fatalf("empty command: %d %s", rec.Code, rec.Body)
	}
}

type recordingHost struct{ calls [][]string }

func (h *recordingHost) RunHostCommand(_ context.Context, _ string, argv []string, _ time.Duration) (machines.HostOutcome, error) {
	h.calls = append(h.calls, argv)
	return machines.HostOutcome{OK: true, Output: "done\n"}, nil
}

func TestRunMachineCommandNeedsADrainedMachine(t *testing.T) {
	repo := machines.NewMemRepo()
	svc := machines.New(machines.Options{Repo: repo, ControllerAddress: "pool.lan", AgentPort: 8461})
	h := &recordingHost{}
	svc.SetHostRunner(h)
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
	path := "/api/v1/machines/" + res.MachineID + "/command"
	body := map[string]any{"command": []string{"docker-compact", "--all"}, "timeout_seconds": 60}
	setState := func(state string) {
		m, err := repo.GetMachine(context.Background(), res.MachineID)
		if err != nil {
			t.Fatal(err)
		}
		m.State = state
		if err := repo.UpdateMachine(context.Background(), m); err != nil {
			t.Fatal(err)
		}
	}
	// Active and draining: refused before anything is sent.
	for _, state := range []string{machines.StateActive, machines.StateDraining} {
		setState(state)
		if rec := doM(t, mux, "POST", path, body); rec.Code != 409 || len(h.calls) != 0 {
			t.Fatalf("%s: %d %s calls %v", state, rec.Code, rec.Body, h.calls)
		}
	}
	// Drained: the argv goes as sent, with its arguments, and the answer comes back.
	setState(machines.StateDrained)
	rec := doM(t, mux, "POST", path, body)
	if rec.Code != 200 || len(h.calls) != 1 || len(h.calls[0]) != 2 || h.calls[0][1] != "--all" || !strings.Contains(rec.Body.String(), `"output":"done\n"`) {
		t.Fatalf("drained: %d %s calls %v", rec.Code, rec.Body, h.calls)
	}
}

// caches R5.58: Compact now on a machine with no reported Docker disk file is
// refused with 400 before anything is held, and an unknown machine is 404; the
// route needs the admin role (roles_test covers the matrix).
func TestStartDockerCompactRefusals(t *testing.T) {
	mux, _, id := machinesMux(t)
	if rec := doM(t, mux, "POST", "/api/v1/machines/"+id+"/docker-compact?confirm=true", nil); rec.Code != 409 && rec.Code != 400 {
		t.Fatalf("no disk file: %d %s", rec.Code, rec.Body)
	}
	if rec := doM(t, mux, "POST", "/api/v1/machines/m_nope/docker-compact", nil); rec.Code != 404 {
		t.Fatalf("unknown machine: %d %s", rec.Code, rec.Body)
	}
}
