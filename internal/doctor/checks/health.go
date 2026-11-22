package checks

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
)

// HealthChecks are the machine health checks of machines R5.70 to R5.77 and
// doctor R5.80 (OPS83). The controller registers them with the live doctor
// (wire.DoctorDeps.Extra), like the KVM checks. Each reads the last
// HealthReport of an agent through doctor.HealthFleet; the limits are the
// editable settings of doctor.HealthThresholds.
func HealthChecks() []doctor.Check {
	return []doctor.Check{
		doctor.NewCheck("machine.health.disk", doctor.GroupMachine, M1, false, healthDisk),
		doctor.NewCheck("machine.health.restart", doctor.GroupMachine, M1, false, healthRestart),
		doctor.NewCheck("machine.health.sleep", doctor.GroupMachine, M1, false, healthSleep),
		doctor.NewCheck("machine.health.battery", doctor.GroupMachine, M1, false, healthBattery),
		doctor.NewCheck("machine.health.clock", doctor.GroupMachine, M1, false, healthClock),
		doctor.NewCheck("machine.health.versions", doctor.GroupMachine, M1, false, healthVersions),
		doctor.NewCheck("machine.health.heartbeat", doctor.GroupMachine, M1, false, healthHeartbeat),
		doctor.NewCheck("machine.health.oom", doctor.GroupMachine, M1, false, healthOOM),
		doctor.NewCheck("machine.health.throttle", doctor.GroupMachine, M1, false, healthThrottle),
	}
}

// healthRow is one machine with its health, for the per-machine loops.
type healthRow struct {
	m doctor.Machine
	h doctor.MachineHealth
}

// healthRows joins the machines in scope with their health. The skip findings
// (no source, a machine that never reported, an offline machine) are returned
// in out; rows are the machines that have a fresh enough report to read.
// withOffline keeps offline machines in rows (the heartbeat check reads them).
func healthRows(ctx context.Context, env doctor.Env, check string, withOffline bool) (rows []healthRow, th doctor.HealthThresholds, out []doctor.Finding) {
	hf, ok := env.Fleet.(doctor.HealthFleet)
	if !ok {
		return nil, th, []doctor.Finding{skip(check, controllerSubject, "Machine health is not read", "This controller does not keep machine health reports.")}
	}
	ms, errs := scopedMachines(ctx, env, check)
	out = errs
	hs, err := hf.MachineHealths(ctx)
	if err != nil {
		return nil, th, append(out, skipFor(check, controllerSubject, "Machine health", err))
	}
	if th, err = hf.HealthThresholds(ctx); err != nil {
		return nil, th, append(out, skipFor(check, controllerSubject, "The health limits", err))
	}
	by := map[string]doctor.MachineHealth{}
	for _, h := range hs {
		by[h.MachineID] = h
	}
	for _, m := range ms {
		h, has := by[m.ID]
		switch {
		case !withOffline && !isOnline(env, m):
			out = append(out, offlineSkip(check, m))
		case !has || !h.Reported:
			out = append(out, skip(check, machineSubject(m), "No health report from "+m.Name+" yet", "The agent has not sent one; it does after its next minute."))
		default:
			rows = append(rows, healthRow{m, h})
		}
	}
	return rows, th, out
}

func healthFinding(check string, r healthRow, sev doctor.Severity, title, detail string, fix doctor.Fix) doctor.Finding {
	if fix.Link == "" {
		fix.Link = machineLink(r.m)
	}
	return doctor.Finding{Check: check, Subject: machineSubject(r.m), Severity: sev, Title: title, Detail: detail, Fix: fix}
}

func okFinding(check string, r healthRow, title string) doctor.Finding {
	return doctor.Finding{Check: check, Subject: machineSubject(r.m), Severity: doctor.OK, Title: title}
}

func fmtGiB(b int64) string { return fmt.Sprintf("%.1f GiB", float64(b)/float64(gib)) }

// healthDisk: a volume that fills in under N days, or has under N % free (default 3 days, 10 %).
func healthDisk(ctx context.Context, env doctor.Env) []doctor.Finding {
	const check = "machine.health.disk"
	rows, th, out := healthRows(ctx, env, check, false)
	for _, r := range rows {
		bad := 0
		for _, d := range r.h.Disks {
			if !d.Known {
				continue
			}
			var why []string
			if d.FreePct < float64(th.DiskFreePctUnder) {
				why = append(why, fmt.Sprintf("only %.0f%% is free (limit %d%%)", d.FreePct, th.DiskFreePctUnder))
			}
			if d.DaysUntilFull != nil && *d.DaysUntilFull < float64(th.DiskDaysUnder) {
				why = append(why, fmt.Sprintf("at the pace of the last day it is full in %.1f days (limit %d)", *d.DaysUntilFull, th.DiskDaysUnder))
			}
			if len(why) == 0 {
				continue
			}
			bad++
			out = append(out, healthFinding(check, r, doctor.Fail, fmt.Sprintf("The %s disk of %s is filling up", strings.ReplaceAll(d.Role, "_", " "), r.m.Name),
				fmt.Sprintf("%s (%s, %s free): %s.", d.Path, d.Role, fmtGiB(d.FreeBytes), strings.Join(why, "; ")),
				doctor.Fix{Text: "Free space on that volume: remove old images and build cache, or add disk. Jobs stop when it is full."}))
		}
		if bad == 0 {
			out = append(out, okFinding(check, r, "The disks of "+r.m.Name+" have room"))
		}
	}
	return out
}

// healthRestart: a restart is pending and the OS may do it while jobs run.
func healthRestart(ctx context.Context, env doctor.Env) []doctor.Finding {
	const check = "machine.health.restart"
	rows, _, out := healthRows(ctx, env, check, false)
	for _, r := range rows {
		if !r.h.RestartPending || !r.h.RestartMayHitJobs {
			out = append(out, okFinding(check, r, "No restart on "+r.m.Name+" can hit a job"))
			continue
		}
		out = append(out, healthFinding(check, r, doctor.Warn, "A restart on "+r.m.Name+" is pending and may start while jobs run",
			strings.Join(r.h.RestartReasons, "; ")+". The OS may restart by itself now, and a job on this machine would be cut off.",
			doctor.Fix{Text: "Drain the machine and restart it yourself, or set the OS active hours or automatic restart so it cannot restart during jobs."}))
	}
	return out
}

// healthSleep: a setting can suspend the machine.
func healthSleep(ctx context.Context, env doctor.Env) []doctor.Finding {
	const check = "machine.health.sleep"
	rows, _, out := healthRows(ctx, env, check, false)
	for _, r := range rows {
		if !r.h.SleepKnown {
			out = append(out, skip(check, machineSubject(r.m), "The sleep settings of "+r.m.Name+" could not be read", "The agent could not read them on this OS."))
			continue
		}
		if !r.h.SleepEnabled {
			out = append(out, okFinding(check, r, r.m.Name+" will not go to sleep"))
			continue
		}
		out = append(out, healthFinding(check, r, doctor.Warn, r.m.Name+" can go to sleep",
			"A sleep or hibernate setting is on ("+strings.Join(r.h.SleepModes, ", ")+"). A sleeping machine takes no jobs and drops the ones it runs.",
			doctor.Fix{Text: "Turn sleep and hibernate off on this machine: Windows power plan, `pmset -a sleep 0` on macOS, mask the systemd sleep targets on Linux."}))
	}
	return out
}

// healthBattery: the machine runs on battery.
func healthBattery(ctx context.Context, env doctor.Env) []doctor.Finding {
	const check = "machine.health.battery"
	rows, _, out := healthRows(ctx, env, check, false)
	for _, r := range rows {
		switch {
		case !r.h.BatteryKnown:
			out = append(out, skip(check, machineSubject(r.m), "The power source of "+r.m.Name+" could not be read", "The agent could not read it on this OS."))
		case r.h.OnBattery:
			out = append(out, healthFinding(check, r, doctor.Warn, r.m.Name+" is on battery",
				"The machine runs on battery, so it may shut down or slow down in the middle of a job.",
				doctor.Fix{Text: "Plug the machine in. The machine setting to pause on battery stops new jobs meanwhile."}))
		default:
			out = append(out, okFinding(check, r, r.m.Name+" is on mains power"))
		}
	}
	return out
}

// healthClock: the agent's clock differs from the controller's by more than N seconds (default 30).
func healthClock(ctx context.Context, env doctor.Env) []doctor.Finding {
	const check = "machine.health.clock"
	rows, th, out := healthRows(ctx, env, check, false)
	for _, r := range rows {
		if !r.h.SkewKnown {
			out = append(out, skip(check, machineSubject(r.m), "The clock of "+r.m.Name+" was not compared yet", "The first heartbeat answer has not come."))
			continue
		}
		abs := r.h.SkewSeconds
		if abs < 0 {
			abs = -abs
		}
		if abs <= float64(th.ClockSkewSeconds) {
			out = append(out, okFinding(check, r, fmt.Sprintf("The clock of %s is within %d s of the controller", r.m.Name, th.ClockSkewSeconds)))
			continue
		}
		dir := "ahead of"
		if r.h.SkewSeconds < 0 {
			dir = "behind"
		}
		out = append(out, healthFinding(check, r, doctor.Warn, fmt.Sprintf("The clock of %s is %.0f s off", r.m.Name, abs),
			fmt.Sprintf("It is %.0f s %s the controller (limit %d s). Certificates, tokens and job times go wrong with a clock like this.", abs, dir, th.ClockSkewSeconds),
			doctor.Fix{Text: "Turn on automatic time on that machine (Windows Time, chrony or systemd-timesyncd, macOS Set time automatically)."}))
	}
	return out
}

// healthVersions: the agent is older than the controller, or a runner app is older than the latest known.
func healthVersions(ctx context.Context, env doctor.Env) []doctor.Finding {
	const check = "machine.health.versions"
	rows, _, out := healthRows(ctx, env, check, false)
	for _, r := range rows {
		var why []string
		if r.h.AgentOutOfDate {
			why = append(why, fmt.Sprintf("the agent is %s and the controller is %s", r.h.AgentVersion, r.h.ControllerVersion))
		}
		if r.h.RunnerOutOfDate {
			why = append(why, fmt.Sprintf("the runner app is %s and the latest known is %s", r.h.RunnerVersion, r.h.LatestRunner))
		}
		if len(why) == 0 {
			out = append(out, okFinding(check, r, r.m.Name+" is up to date"))
			continue
		}
		out = append(out, healthFinding(check, r, doctor.Warn, "Something on "+r.m.Name+" is out of date",
			strings.Join(why, "; ")+".", doctor.Fix{Text: "Update the agent from the Machine page; runners take the new runner app when they are replaced."}))
	}
	return out
}

// healthHeartbeat: no heartbeat for N seconds (default 120). It reads the last message of the agent, so it
// works for an offline machine.
func healthHeartbeat(ctx context.Context, env doctor.Env) []doctor.Finding {
	const check = "machine.health.heartbeat"
	hf, ok := env.Fleet.(doctor.HealthFleet)
	if !ok {
		return []doctor.Finding{skip(check, controllerSubject, "Machine health is not read", "This controller does not keep machine health reports.")}
	}
	ms, out := scopedMachines(ctx, env, check)
	th, err := hf.HealthThresholds(ctx)
	if err != nil {
		return append(out, skipFor(check, controllerSubject, "The health limits", err))
	}
	limit := time.Duration(th.NoHeartbeatSeconds) * time.Second
	for _, m := range ms {
		s := machineSubject(m)
		if m.LastSeen.IsZero() {
			out = append(out, skip(check, s, m.Name+" has not sent a heartbeat yet", "The agent has never been seen."))
			continue
		}
		age := env.Now.Sub(m.LastSeen)
		if age <= limit {
			out = append(out, doctor.Finding{Check: check, Subject: s, Severity: doctor.OK, Title: "The agent on " + m.Name + " is answering"})
			continue
		}
		out = append(out, doctor.Finding{Check: check, Subject: s, Severity: doctor.Fail, Title: "No heartbeat from " + m.Name,
			Detail: fmt.Sprintf("The last heartbeat was %s ago (limit %s).", age.Round(time.Second), limit),
			Fix:    doctor.Fix{Text: "Check that the machine is on and the agent service runs; see machine.online.", Link: machineLink(m)}})
	}
	return out
}

// healthOOM: a job or the pool was killed for memory in the last hour (default window 60 minutes).
func healthOOM(ctx context.Context, env doctor.Env) []doctor.Finding {
	const check = "machine.health.oom"
	rows, th, out := healthRows(ctx, env, check, false)
	for _, r := range rows {
		if !r.h.OOMRecent {
			out = append(out, okFinding(check, r, "No job on "+r.m.Name+" was killed for memory lately"))
			continue
		}
		out = append(out, healthFinding(check, r, doctor.Warn, "Memory ran out on "+r.m.Name,
			fmt.Sprintf("The OS killed a process for memory at %s (inside the last %d minutes).", r.h.OOMLastAt.UTC().Format("15:04 MST"), th.OOMWindowMinutes),
			doctor.Fix{Text: "Lower the pool size or the slots of this machine, or give the heavy job a bigger size class."}))
	}
	return out
}

// healthThrottle: the CPU is throttled by heat.
func healthThrottle(ctx context.Context, env doctor.Env) []doctor.Finding {
	const check = "machine.health.throttle"
	rows, _, out := healthRows(ctx, env, check, false)
	for _, r := range rows {
		switch {
		case !r.h.ThrottleKnown:
			out = append(out, skip(check, machineSubject(r.m), "The CPU temperature of "+r.m.Name+" is not read", "This OS does not give it without more rights, or the machine has no sensors."))
		case r.h.Throttled:
			out = append(out, healthFinding(check, r, doctor.Warn, "The CPU of "+r.m.Name+" is throttled", r.h.ThrottleDetail+". Jobs run slower and may time out.",
				doctor.Fix{Text: "Clean the fans and give the machine air, or lower its CPU cap."}))
		default:
			out = append(out, okFinding(check, r, "The CPU of "+r.m.Name+" is not throttled"))
		}
	}
	return out
}
