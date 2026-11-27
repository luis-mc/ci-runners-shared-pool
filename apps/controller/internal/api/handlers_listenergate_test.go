package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/listenergate"
)

type gateStub struct{ enforce bool }

func (g *gateStub) View(context.Context) (listenergate.View, error) {
	return listenergate.View{Enforce: g.enforce, WakeEvery: listenergate.WakeEvery, Runners: []listenergate.RunnerStatus{{
		Runner: listenergate.Runner{Name: "box-1", MachineID: "m1", OS: "linux"}, Text: "gated: offline by design",
		NextWake: time.Date(2026, 11, 29, 10, 0, 0, 0, time.UTC), QueueText: "waiting for room on M1: needs 6 CPU, 5 free"}}}, nil
}
func (g *gateStub) Save(_ context.Context, on bool, _ string) (listenergate.Settings, error) {
	g.enforce = on
	return listenergate.Settings{Enforce: on}, nil
}

// runners R5.138: observe by default, the console words, admin saves.
func TestListenerGateRoutes(t *testing.T) {
	stub := &gateStub{}
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, ListenerGate: stub, Authn: asRole(auth.RoleOwner)})
	rec := do(mux, http.MethodGet, "/api/v1/listener-gate", "")
	for _, want := range []string{`"enforce":false`, `"wake_every_days":7`, `gated: offline by design`, `waiting for room on M1`, `"next_wake"`} {
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("get %d lacks %s: %s", rec.Code, want, rec.Body)
		}
	}
	if rec = do(mux, http.MethodPut, "/api/v1/listener-gate", `{"enforce":true}`); rec.Code != 200 || !stub.enforce {
		t.Fatalf("put %d %s", rec.Code, rec.Body)
	}
	viewer := http.NewServeMux()
	Register(viewer, Options{Version: "t", DB: fakeDB{}, ListenerGate: stub, Authn: asRole(auth.RoleViewer)})
	if rec = do(viewer, http.MethodPut, "/api/v1/listener-gate", `{"enforce":false}`); rec.Code != 403 {
		t.Errorf("a viewer saved: %d", rec.Code)
	}
	off := http.NewServeMux()
	Register(off, Options{Version: "t", DB: fakeDB{}, Authn: asRole(auth.RoleOwner)})
	if rec = do(off, http.MethodGet, "/api/v1/listener-gate", ""); rec.Code != 501 {
		t.Errorf("not wired: %d", rec.Code)
	}
}
