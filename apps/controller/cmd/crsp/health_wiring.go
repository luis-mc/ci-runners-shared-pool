package main

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machinehealth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// newHealthService makes the machine health service over the store and the
// machines service (machines R5.72 to R5.79, OPS83).
func newHealthService(db *store.Store, svc *machines.Service, version string) *machinehealth.Service {
	return &machinehealth.Service{
		Store: db, ControllerVersion: version, LatestRunner: latestRunnerVersion,
		Jobs: func(ctx context.Context, id string) (int, bool) {
			v, err := svc.Get(ctx, id)
			if err != nil {
				return 0, false
			}
			return len(v.RunningJobs), v.Machine.State == "active" && v.Machine.Accepting
		},
		Slots: func(ctx context.Context, id string) int {
			v, err := svc.Get(ctx, id)
			if err != nil {
				return 0
			}
			return v.Settings.JobSlots
		},
	}
}

// latestRunnerVersion is the newest runner app version crsp pins (runners R5.37).
func latestRunnerVersion() string {
	best := ""
	for _, os := range []string{"linux", "windows", "osx"} {
		for _, arch := range []string{"x64", "arm64"} {
			if p, err := runners.PinFor(os, arch); err == nil && (best == "" || machinehealth.OutOfDate(best, p.Version)) {
				best = p.Version
			}
		}
	}
	return best
}

var _ doctor.HealthFleet = doctorFleet{}

// MachineHealths gives the Doctor the health of every machine that reported (doctor R5.66).
func (f doctorFleet) MachineHealths(ctx context.Context) ([]doctor.MachineHealth, error) {
	if f.health == nil {
		return nil, nil
	}
	views, err := f.health.Views(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]doctor.MachineHealth, 0, len(views))
	for _, v := range views {
		out = append(out, doctorHealth(v))
	}
	return out, nil
}

// HealthThresholds gives the Doctor the editable limits.
func (f doctorFleet) HealthThresholds(ctx context.Context) (doctor.HealthThresholds, error) {
	t := machinehealth.DefaultThresholds
	if f.health != nil {
		var err error
		if t, err = f.health.Thresholds(ctx); err != nil {
			return doctor.HealthThresholds{}, err
		}
	}
	return doctor.HealthThresholds{DiskDaysUnder: t.DiskDaysUnder, DiskFreePctUnder: t.DiskFreePctUnder, NoHeartbeatSeconds: t.NoHeartbeatSeconds,
		ClockSkewSeconds: t.ClockSkewSeconds, OOMWindowMinutes: t.OOMWindowMinutes}, nil
}

func doctorHealth(v machinehealth.View) doctor.MachineHealth {
	h := doctor.MachineHealth{MachineID: v.MachineID, Reported: v.Reported, ReceivedAt: v.ReceivedAt}
	if !v.Reported {
		return h
	}
	for _, d := range v.Disks {
		h.Disks = append(h.Disks, doctor.DiskHealth{Role: d.Role, Path: d.Path, Known: d.Known, FreePct: d.FreePct, FreeBytes: d.Free, DaysUntilFull: d.DaysUntilFull})
	}
	r := v.Report
	u := r.GetUpdates()
	h.RestartPending, h.RestartMayHitJobs, h.RestartReasons = u.GetKnown() && u.GetRestartPending(), v.RestartMayHitJobs, u.GetRestartReasons()
	p := r.GetPower()
	h.SleepKnown, h.SleepEnabled, h.SleepModes = p.GetSleepKnown(), p.GetSleepEnabled(), p.GetSleepModes()
	h.BatteryKnown, h.OnBattery = p.GetSourceKnown(), p.GetOnBattery()
	c := r.GetConnectivity()
	h.SkewKnown, h.SkewSeconds = c.GetSkewKnown(), c.GetClockSkewSeconds()
	vs := r.GetVersions()
	h.AgentVersion, h.ControllerVersion, h.AgentOutOfDate = vs.GetAgentVersion(), v.ControllerVersion, v.AgentOutOfDate
	h.RunnerVersion, h.LatestRunner, h.RunnerOutOfDate = vs.GetRunnerAppVersion(), v.LatestRunner, v.RunnerOutOfDate
	h.OOMRecent, h.OOMLastAt = v.OOMRecent, v.OOMLastAt
	t := r.GetThermal()
	h.ThrottleKnown, h.Throttled, h.ThrottleDetail = t.GetKnown(), t.GetThrottled(), t.GetThrottleDetail()
	return h
}
