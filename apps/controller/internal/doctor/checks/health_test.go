package checks

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

type healthFleet struct {
	*fleet
	hs  []doctor.MachineHealth
	th  doctor.HealthThresholds
	err error
}

func (f healthFleet) MachineHealths(context.Context) ([]doctor.MachineHealth, error) {
	return f.hs, f.err
}
func (f healthFleet) HealthThresholds(context.Context) (doctor.HealthThresholds, error) {
	return f.th, nil
}

var defaultHealthLimits = doctor.HealthThresholds{DiskDaysUnder: 3, DiskFreePctUnder: 10, NoHeartbeatSeconds: 120, ClockSkewSeconds: 30, OOMWindowMinutes: 60}

func runHealth(t *testing.T, env doctor.Env, check string) []doctor.Finding {
	t.Helper()
	reg := doctor.NewRegistry()
	for _, c := range HealthChecks() {
		reg.Register(c)
	}
	e := &doctor.Engine{Registry: reg, Store: doctor.NewMemStore(), Now: func() time.Time { return now },
		Env: func(context.Context) (doctor.Env, error) { return env, nil }}
	r, err := e.Run(context.Background(), doctor.Scope{Checks: []string{check}}, doctor.TriggerCLI, "test")
	if err != nil {
		t.Fatal(err)
	}
	return forCheck(r.Findings, check)
}

func hEnv(h doctor.MachineHealth) doctor.Env {
	h.MachineID, h.Reported = "m1", true
	return doctor.Env{Now: now, Fleet: healthFleet{fleet: &fleet{machines: []doctor.Machine{healthyMachine()}}, hs: []doctor.MachineHealth{h}, th: defaultHealthLimits}}
}

func oneHealth(t *testing.T, fs []doctor.Finding) doctor.Finding {
	t.Helper()
	if len(fs) != 1 {
		t.Fatalf("want one finding: %+v", fs)
	}
	return fs[0]
}

func TestHealthDisk(t *testing.T) {
	days := func(d float64) *float64 { return &d }
	for name, c := range map[string]struct {
		d    doctor.DiskHealth
		want doctor.Severity
	}{
		"roomy":            {doctor.DiskHealth{Role: "system", Known: true, FreePct: 50, DaysUntilFull: days(40)}, doctor.OK},
		"under 10 percent": {doctor.DiskHealth{Role: "system", Known: true, FreePct: 9.5}, doctor.Fail},
		"full in 2 days":   {doctor.DiskHealth{Role: "docker", Known: true, FreePct: 60, DaysUntilFull: days(2)}, doctor.Fail},
		"3 days is fine":   {doctor.DiskHealth{Role: "docker", Known: true, FreePct: 60, DaysUntilFull: days(3)}, doctor.OK},
		"unknown volume":   {doctor.DiskHealth{Role: "docker", Known: false}, doctor.OK},
	} {
		f := oneHealth(t, runHealth(t, hEnv(doctor.MachineHealth{Disks: []doctor.DiskHealth{c.d}}), "machine.health.disk"))
		if f.Severity != c.want || (c.want == doctor.Fail && (f.Fix.Text == "" || f.Fix.Link != "/machines/m1")) {
			t.Errorf("%s: %+v", name, f)
		}
	}
	// The limits are settings: 20 % free is a finding when the limit is 25.
	env := hEnv(doctor.MachineHealth{Disks: []doctor.DiskHealth{{Role: "system", Known: true, FreePct: 20}}})
	hf := env.Fleet.(healthFleet)
	hf.th.DiskFreePctUnder = 25
	env.Fleet = hf
	if f := oneHealth(t, runHealth(t, env, "machine.health.disk")); f.Severity != doctor.Fail {
		t.Fatalf("%+v", f)
	}
}

func TestHealthRestartSleepBattery(t *testing.T) {
	cases := []struct {
		check string
		h     doctor.MachineHealth
		want  doctor.Severity
	}{
		{"machine.health.restart", doctor.MachineHealth{RestartPending: true, RestartMayHitJobs: true, RestartReasons: []string{"RebootRequired"}}, doctor.Warn},
		{"machine.health.restart", doctor.MachineHealth{RestartPending: true}, doctor.OK},
		{"machine.health.restart", doctor.MachineHealth{}, doctor.OK},
		{"machine.health.sleep", doctor.MachineHealth{SleepKnown: true, SleepEnabled: true, SleepModes: []string{"sleep after 15 min"}}, doctor.Warn},
		{"machine.health.sleep", doctor.MachineHealth{SleepKnown: true}, doctor.OK},
		{"machine.health.sleep", doctor.MachineHealth{}, doctor.Skip},
		{"machine.health.battery", doctor.MachineHealth{BatteryKnown: true, OnBattery: true}, doctor.Warn},
		{"machine.health.battery", doctor.MachineHealth{BatteryKnown: true}, doctor.OK},
		{"machine.health.battery", doctor.MachineHealth{}, doctor.Skip},
	}
	for _, c := range cases {
		if f := oneHealth(t, runHealth(t, hEnv(c.h), c.check)); f.Severity != c.want {
			t.Errorf("%s %+v: %s", c.check, c.h, f.Severity)
		}
	}
}

func TestHealthClockVersionsOOMThrottle(t *testing.T) {
	cases := []struct {
		check string
		h     doctor.MachineHealth
		want  doctor.Severity
	}{
		{"machine.health.clock", doctor.MachineHealth{SkewKnown: true, SkewSeconds: 31}, doctor.Warn},
		{"machine.health.clock", doctor.MachineHealth{SkewKnown: true, SkewSeconds: -45}, doctor.Warn},
		{"machine.health.clock", doctor.MachineHealth{SkewKnown: true, SkewSeconds: 30}, doctor.OK},
		{"machine.health.clock", doctor.MachineHealth{}, doctor.Skip},
		{"machine.health.versions", doctor.MachineHealth{AgentOutOfDate: true, AgentVersion: "1.3", ControllerVersion: "1.4"}, doctor.Warn},
		{"machine.health.versions", doctor.MachineHealth{RunnerOutOfDate: true, RunnerVersion: "2.1", LatestRunner: "2.2"}, doctor.Warn},
		{"machine.health.versions", doctor.MachineHealth{}, doctor.OK},
		{"machine.health.oom", doctor.MachineHealth{OOMRecent: true, OOMLastAt: now}, doctor.Warn},
		{"machine.health.oom", doctor.MachineHealth{}, doctor.OK},
		{"machine.health.throttle", doctor.MachineHealth{ThrottleKnown: true, Throttled: true, ThrottleDetail: "a zone reads 97 °C"}, doctor.Warn},
		{"machine.health.throttle", doctor.MachineHealth{ThrottleKnown: true}, doctor.OK},
		{"machine.health.throttle", doctor.MachineHealth{}, doctor.Skip},
	}
	for _, c := range cases {
		if f := oneHealth(t, runHealth(t, hEnv(c.h), c.check)); f.Severity != c.want {
			t.Errorf("%s %+v: %s", c.check, c.h, f.Severity)
		}
	}
}

func TestHealthHeartbeat(t *testing.T) {
	mk := func(age time.Duration) doctor.Env {
		m := healthyMachine()
		m.LastSeen = now.Add(-age)
		return doctor.Env{Now: now, Fleet: healthFleet{fleet: &fleet{machines: []doctor.Machine{m}}, th: defaultHealthLimits}}
	}
	if f := oneHealth(t, runHealth(t, mk(100*time.Second), "machine.health.heartbeat")); f.Severity != doctor.OK {
		t.Fatalf("%+v", f)
	}
	f := oneHealth(t, runHealth(t, mk(3*time.Minute), "machine.health.heartbeat"))
	if f.Severity != doctor.Fail || f.Detail == "" {
		t.Fatalf("%+v", f)
	}
	m := healthyMachine()
	m.LastSeen = time.Time{}
	env := doctor.Env{Now: now, Fleet: healthFleet{fleet: &fleet{machines: []doctor.Machine{m}}, th: defaultHealthLimits}}
	if f := oneHealth(t, runHealth(t, env, "machine.health.heartbeat")); f.Severity != doctor.Skip {
		t.Fatalf("never seen: %+v", f)
	}
}

func TestHealthSkips(t *testing.T) {
	// A fleet without health, a machine that never reported, an offline machine and a failing source all skip.
	plain := doctor.Env{Now: now, Fleet: &fleet{machines: []doctor.Machine{healthyMachine()}}}
	if f := oneHealth(t, runHealth(t, plain, "machine.health.disk")); f.Severity != doctor.Skip {
		t.Fatalf("no source: %+v", f)
	}
	never := doctor.Env{Now: now, Fleet: healthFleet{fleet: &fleet{machines: []doctor.Machine{healthyMachine()}}, th: defaultHealthLimits}}
	if f := oneHealth(t, runHealth(t, never, "machine.health.disk")); f.Severity != doctor.Skip {
		t.Fatalf("never reported: %+v", f)
	}
	off := healthyMachine()
	off.Online = false
	offline := doctor.Env{Now: now, Fleet: healthFleet{fleet: &fleet{machines: []doctor.Machine{off}}, hs: []doctor.MachineHealth{{MachineID: "m1", Reported: true}}, th: defaultHealthLimits}}
	if f := oneHealth(t, runHealth(t, offline, "machine.health.oom")); f.Severity != doctor.Skip {
		t.Fatalf("offline: %+v", f)
	}
	bad := doctor.Env{Now: now, Fleet: healthFleet{fleet: &fleet{machines: []doctor.Machine{healthyMachine()}}, err: errors.New("db down"), th: defaultHealthLimits}}
	if f := oneHealth(t, runHealth(t, bad, "machine.health.oom")); f.Severity != doctor.Skip {
		t.Fatalf("source error: %+v", f)
	}
}

func TestHealthChecksAreNineAndNotInRegister(t *testing.T) {
	if n := len(HealthChecks()); n != 9 {
		t.Fatalf("%d health checks", n)
	}
	reg := doctor.NewRegistry()
	Register(reg)
	for _, c := range HealthChecks() {
		for _, id := range reg.SortedIDs() {
			if id == c.ID() {
				t.Errorf("%s is registered twice", id)
			}
		}
	}
}
