package main

import (
	"testing"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machinehealth"
)

// The Doctor reads the health through doctorHealth: unknown stays unknown, and a
// machine that never reported gives no values.
func TestDoctorHealthMapping(t *testing.T) {
	if h := doctorHealth(machinehealth.View{MachineID: "m"}); h.Reported || len(h.Disks) != 0 {
		t.Fatalf("%+v", h)
	}
	days := 1.5
	v := machinehealth.View{MachineID: "m", Reported: true, AgentOutOfDate: true, RestartMayHitJobs: true, OOMRecent: true,
		Disks: []machinehealth.Disk{{Role: "system", Path: "/", Known: true, FreePct: 8, DaysUntilFull: &days}},
		Report: &agentv1.HealthReport{
			Updates:      &agentv1.UpdateHealth{Known: true, RestartPending: true},
			Power:        &agentv1.PowerHealth{SourceKnown: true, OnBattery: true},
			Connectivity: &agentv1.ConnectivityHealth{SkewKnown: true, ClockSkewSeconds: -40},
			Thermal:      &agentv1.ThermalHealth{UnknownReason: "needs admin"},
		}}
	h := doctorHealth(v)
	if !h.Reported || !h.RestartPending || !h.RestartMayHitJobs || !h.BatteryKnown || !h.OnBattery || h.SkewSeconds != -40 || !h.AgentOutOfDate || !h.OOMRecent {
		t.Fatalf("%+v", h)
	}
	if h.ThrottleKnown || h.SleepKnown || len(h.Disks) != 1 || *h.Disks[0].DaysUntilFull != 1.5 {
		t.Fatalf("unknown values must stay unknown: %+v", h)
	}
}
