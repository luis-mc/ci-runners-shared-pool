package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/gen"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
)

// runners R5.83, OPS86 over the API: the console saves the floor, the machine
// shows it with the floor in force, a value out of range is refused, and a
// floor that is off in config is refused with a 400.
func TestMachineDiskFloorThroughTheAPI(t *testing.T) {
	svc, mux, id := poolSvcMux(t)
	svc.HandleAccepting(context.Background(), id, &agentv1.AcceptingReport{Accepting: true, DiskFloorGib: 10})
	var m gen.Machine
	rec := doM(t, mux, "GET", "/api/v1/machines/"+id, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if m.Settings.DiskFloorGib == nil || *m.Settings.DiskFloorGib != 0 || m.Policy == nil || m.Policy.DiskFloorGib == nil || *m.Policy.DiskFloorGib != 10 {
		t.Fatalf("before: settings %v policy %+v", m.Settings.DiskFloorGib, m.Policy)
	}
	if rec = doM(t, mux, "PATCH", "/api/v1/machines/"+id, map[string]any{"disk_floor_gib": 150}); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	m = gen.Machine{}
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	if *m.Settings.DiskFloorGib != 150 {
		t.Fatalf("saved: %v", *m.Settings.DiskFloorGib)
	}
	for _, v := range []int{0, -1, 2001} {
		if rec = doM(t, mux, "PATCH", "/api/v1/machines/"+id, map[string]any{"disk_floor_gib": v}); rec.Code != 400 {
			t.Errorf("%d: %d %s", v, rec.Code, rec.Body)
		}
	}
	svc.HandleAccepting(context.Background(), id, &agentv1.AcceptingReport{Accepting: true, DiskFloorOff: true})
	if rec = doM(t, mux, "PATCH", "/api/v1/machines/"+id, map[string]any{"disk_floor_gib": 100}); rec.Code != 400 {
		t.Errorf("floor off in config: %d %s", rec.Code, rec.Body)
	}
}
