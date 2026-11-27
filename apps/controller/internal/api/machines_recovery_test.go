package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// operations R5.44: the machine shows `recovery` in the API once its agent has
// been asked to update for an hour and still runs the old version, and the one
// action arms it; a healthy machine is refused with a conflict.
func TestMachineRecoveryStateAndAction(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	clock := &now
	svc := machines.New(machines.Options{Repo: machines.NewMemRepo(), ControllerAddress: "pool.lan", AgentPort: 8461,
		Fingerprint: strings.Repeat("ab", 32), Now: func() time.Time { return *clock }})
	svc.SetHooks(machines.Hooks{ControllerVersion: "1.2.0"})
	code, err := svc.CreateJoinCode(context.Background(), "owner", 0)
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Join(context.Background(), machines.JoinInput{Code: code.Code, AgentVersion: "1.0.0",
		Facts: machines.Facts{HostName: "box-1", OS: "linux", Arch: "x64", Threads: 8, MemoryBytes: 32 << 30}})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Register(mux, Options{Version: "test", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Machines: svc, CA: fakeCA{}, Authn: asRole(auth.RoleOwner)})
	id := res.MachineID
	get := func() gen.Machine {
		rec := doM(t, mux, "GET", "/api/v1/machines/"+id, nil)
		if rec.Code != 200 {
			t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
		}
		var m gen.Machine
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if rec := doM(t, mux, "POST", "/api/v1/machines/"+id+"/recovery", nil); rec.Code != 409 {
		t.Fatalf("recover a healthy machine: %d", rec.Code)
	}
	_ = svc.AskUpdate(id, "1.2.0") // offline: the ask is not delivered, but it is noted
	if get().Recovery != nil {
		t.Fatal("recovery shown before the hour")
	}
	*clock = now.Add(61 * time.Minute)
	m := get()
	if m.Recovery == nil || m.Recovery.State != gen.NeedsRecovery || m.Recovery.Reason != gen.Stuck || m.Recovery.Has != "1.0.0" || m.Recovery.Needs != "1.2.0" || m.Recovery.Armed {
		t.Fatalf("recovery = %+v", m.Recovery)
	}
	if rec := doM(t, mux, "POST", "/api/v1/machines/"+id+"/recovery", nil); rec.Code != 202 {
		t.Fatalf("recover: %d %s", rec.Code, rec.Body.String())
	}
	if m := get(); m.Recovery == nil || !m.Recovery.Armed {
		t.Fatalf("not armed: %+v", m.Recovery)
	}
}
