package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

// CRSP75 over the API: the machine shows its mode, the battery switch and the CPU cap, and `accepting` with the reason.
func TestMachinePolicyThroughTheAPI(t *testing.T) {
	mux, id := poolMux(t)
	rec := doM(t, mux, "GET", "/api/v1/machines/"+id, nil)
	var m gen.Machine
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if m.Settings.Mode == nil || *m.Settings.Mode != gen.MachineModeAutomatic || m.Settings.PauseOnBattery == nil || !*m.Settings.PauseOnBattery ||
		m.Settings.CpuCapPct == nil || *m.Settings.CpuCapPct != gen.N100 {
		t.Fatalf("defaults: %+v", m.Settings)
	}
	if m.Accepting == nil || !*m.Accepting || m.PauseReason != nil {
		t.Fatalf("a machine that reported nothing accepts: %v %v", m.Accepting, m.PauseReason)
	}

	// The console saves Paused, a 50 % cap and the battery switch off.
	rec = doM(t, mux, "PATCH", "/api/v1/machines/"+id, map[string]any{"mode": "paused", "cpu_cap_pct": 50, "pause_on_battery": false})
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	if *m.Settings.Mode != gen.MachineModePaused || *m.Settings.CpuCapPct != gen.N50 || *m.Settings.PauseOnBattery {
		t.Fatalf("saved: %+v", m.Settings)
	}

	// A pause with a timer sets pause_until; none clears it.
	rec = doM(t, mux, "PATCH", "/api/v1/machines/"+id, map[string]any{"pause": "one_hour"})
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	if rec.Code != 200 || m.Settings.PauseUntil == nil {
		t.Fatalf("one hour: %d %s", rec.Code, rec.Body)
	}
	rec = doM(t, mux, "PATCH", "/api/v1/machines/"+id, map[string]any{"pause": "none"})
	m = gen.Machine{}
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	if rec.Code != 200 || m.Settings.PauseUntil != nil {
		t.Fatalf("none: %d %s", rec.Code, rec.Body)
	}

	// Wrong values are refused with 400.
	for _, body := range []map[string]any{{"mode": "sometimes"}, {"cpu_cap_pct": 60}, {"pause": "tomorrow"}} {
		if rec = doM(t, mux, "PATCH", "/api/v1/machines/"+id, body); rec.Code != 400 {
			t.Errorf("%v: %d %s", body, rec.Code, rec.Body)
		}
	}
}

// CRSP75: the agent's report reaches the API as accepting, the reason and the battery and cap facts.
func TestMachineStatusShowsTheAgentsReport(t *testing.T) {
	svc, mux, id := poolSvcMux(t)
	svc.HandleAccepting(context.Background(), id, &agentv1.AcceptingReport{
		Accepting: false, PauseReason: "on_battery", OnBattery: true, BatteryKnown: true,
		CpuCapPct: 50, CpuCapState: "not_supported", CpuCapDetail: "CPU cap is not supported on macOS yet",
	})
	rec := doM(t, mux, "GET", "/api/v1/machines/"+id, nil)
	var m gen.Machine
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if *m.Accepting || m.PauseReason == nil || *m.PauseReason != gen.PauseReasonOnBattery {
		t.Fatalf("accepting %v reason %v", *m.Accepting, m.PauseReason)
	}
	if m.Policy == nil || !m.Policy.OnBattery || !m.Policy.BatteryKnown || m.Policy.CpuCapState == nil || *m.Policy.CpuCapState != gen.CpuCapStateNotSupported ||
		m.Policy.CpuCapDetail == nil {
		t.Fatalf("policy %+v", m.Policy)
	}
}

// caches R5.51 to R5.54 over the API: the compact policy is saved with its defaults
// shown, wrong values give 400, and the agent's disk report shows as a status.
func TestMachineDockerCompactSettingsAndStatus(t *testing.T) {
	svc, mux, id := poolSvcMux(t)
	var m gen.Machine
	get := func() {
		t.Helper()
		m = gen.Machine{}
		rec := doM(t, mux, "GET", "/api/v1/machines/"+id, nil)
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &m) != nil {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
	}
	get()
	s := m.Settings
	if *s.DockerCompact != gen.DockerCompactModeAuto || *s.DockerCompactThresholdGib != 30 || *s.DockerCompactIdleMinutes != 30 || m.DockerCompactStatus != nil {
		t.Fatalf("defaults: %+v status %+v", s, m.DockerCompactStatus)
	}
	rec := doM(t, mux, "PATCH", "/api/v1/machines/"+id, map[string]any{
		"docker_compact": "on", "docker_compact_threshold_gib": 50, "docker_compact_idle_minutes": 45})
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	get()
	s = m.Settings
	if *s.DockerCompact != gen.DockerCompactModeOn || *s.DockerCompactThresholdGib != 50 || *s.DockerCompactIdleMinutes != 45 {
		t.Fatalf("saved: %+v", s)
	}
	if rec := doM(t, mux, "PATCH", "/api/v1/machines/"+id, map[string]any{"docker_compact": "auto"}); rec.Code != 200 {
		t.Fatalf("auto: %d %s", rec.Code, rec.Body)
	}
	for _, body := range []map[string]any{{"docker_compact": "sometimes"}, {"docker_compact_idle_minutes": 5000}} {
		if rec := doM(t, mux, "PATCH", "/api/v1/machines/"+id, body); rec.Code != 400 {
			t.Errorf("%v: %d %s", body, rec.Code, rec.Body)
		}
	}
	svc.HandleMachineReport(id, &agentv1.MachineReport{DockerDisk: &agentv1.DockerDiskReport{
		Present: true, FileBytes: 236 << 30, UsedKnown: true, UsedBytes: 71 << 30, CanCompact: false}})
	get()
	st := m.DockerCompactStatus
	if st == nil || !st.DiskPresent || st.CanCompact || *st.FileBytes != 236<<30 || *st.ReclaimableBytes != 165<<30 {
		t.Fatalf("status %+v", st)
	}
}
