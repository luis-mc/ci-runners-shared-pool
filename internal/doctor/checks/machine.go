package checks

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runnercache"
)

// Thresholds of doctor 7.2.
const (
	// OfflineAfter is how old the agent's last message may be (machine.online).
	OfflineAfter = 90 * time.Second
	// LoadFreshFor is how old a load sample may be (machine.load.fresh).
	LoadFreshFor = 60 * time.Second
	// OverBudgetFor is how long the load may stay over budget (machine.load.budget).
	OverBudgetFor = 10 * time.Minute

	gib = int64(1) << 30
)

func scopedMachines(ctx context.Context, env doctor.Env, check string) ([]doctor.Machine, []doctor.Finding) {
	ms, err := env.Fleet.Machines(ctx)
	if err != nil {
		return nil, []doctor.Finding{skipFor(check, controllerSubject, "The machine list", err)}
	}
	var out []doctor.Machine
	for _, m := range ms {
		if env.InScopeMachine(m) && m.State != "removing" {
			out = append(out, m)
		}
	}
	return out, nil
}

func offlineSkip(check string, m doctor.Machine) doctor.Finding {
	return skip(check, machineSubject(m), m.Name+" is offline", "Its agent does not answer, so this was not checked. See machine.online.")
}

func isOnline(env doctor.Env, m doctor.Machine) bool {
	return m.Online && env.Now.Sub(m.LastSeen) <= OfflineAfter
}

// machineOnline: the agent's last message is older than the offline timeout.
// New check, no dev-standards source.
func machineOnline(ctx context.Context, env doctor.Env) []doctor.Finding {
	ms, errs := scopedMachines(ctx, env, "machine.online")
	out := errs
	for _, m := range ms {
		f := doctor.Finding{Check: "machine.online", Subject: machineSubject(m)}
		if isOnline(env, m) {
			f.Severity, f.Title = doctor.OK, m.Name+" is online"
		} else {
			f.Severity, f.Title = doctor.Fail, m.Name+" is offline"
			if m.LastSeen.IsZero() {
				f.Detail = "The agent has never been seen."
			} else {
				f.Detail = fmt.Sprintf("The agent was last seen %s ago (limit %s).", env.Now.Sub(m.LastSeen).Round(time.Second), OfflineAfter)
			}
			f.Fix = doctor.Fix{
				Text: "Start the agent service on that machine: Windows Services, `systemctl start` on Linux, `launchctl` on macOS. Jobs queue for its runners until it is back.",
				Link: machineLink(m),
			}
		}
		out = append(out, f)
	}
	return out
}

// machineLoadFresh: load figures older than 60 s. Ported from delivery_doctor.py
// machine_items (machine.hostload); tests in checks_test.go TestLoadFresh.
func machineLoadFresh(ctx context.Context, env doctor.Env) []doctor.Finding {
	ms, out := scopedMachines(ctx, env, "machine.load.fresh")
	for _, m := range ms {
		s := machineSubject(m)
		if !isOnline(env, m) {
			out = append(out, offlineSkip("machine.load.fresh", m))
			continue
		}
		f := doctor.Finding{Check: "machine.load.fresh", Subject: s}
		switch {
		case m.Load == nil:
			f.Severity, f.Title, f.Detail = doctor.Fail, m.Name+" has sent no load figures", "No sample exists yet."
		case env.Now.Sub(m.Load.SampledAt) > LoadFreshFor:
			f.Severity, f.Title = doctor.Fail, m.Name+" load figures are old"
			f.Detail = fmt.Sprintf("The newest sample is %s old (limit %s).", env.Now.Sub(m.Load.SampledAt).Round(time.Second), LoadFreshFor)
		default:
			f.Severity, f.Title = doctor.OK, m.Name+" load figures are fresh"
		}
		if f.Severity != doctor.OK {
			f.Fix = doctor.Fix{Text: "Restart the agent on that machine; it measures load every 15 seconds.", Link: machineLink(m)}
		}
		out = append(out, f)
	}
	return out
}

// machineLoadMeasured: the machine can read its load. The gate holds every job
// of a machine whose load it cannot measure (scheduling R5.22, decision D53),
// so a machine that cannot measure is a fail, not a hint. New check.
func machineLoadMeasured(ctx context.Context, env doctor.Env) []doctor.Finding {
	ms, out := scopedMachines(ctx, env, "machine.load.measured")
	for _, m := range ms {
		if !isOnline(env, m) {
			out = append(out, offlineSkip("machine.load.measured", m))
			continue
		}
		if m.Load == nil {
			out = append(out, skip("machine.load.measured", machineSubject(m), m.Name+" has sent no load figures", "machine.load.fresh says so too."))
			continue
		}
		f := doctor.Finding{Check: "machine.load.measured", Subject: machineSubject(m)}
		if !m.Load.MemoryKnown && !m.Load.CPUKnown {
			f.Severity, f.Title = doctor.Fail, m.Name+" cannot measure its load"
			f.Detail = "Neither memory nor CPU could be read, so the gate holds every job on this machine with the reason \"load not measured\"."
			f.Fix = doctor.Fix{Text: "Restart the agent on that machine and check that it may read memory and CPU figures (on Linux, /proc; on Windows, the performance counters). Jobs go on as soon as one figure is read.", Link: machineLink(m)}
		} else {
			f.Severity, f.Title = doctor.OK, m.Name+" measures its load"
		}
		out = append(out, f)
	}
	return out
}

// machineLoadBudget: memory or CPU over the budget for more than 10 minutes. New check.
func machineLoadBudget(ctx context.Context, env doctor.Env) []doctor.Finding {
	ms, out := scopedMachines(ctx, env, "machine.load.budget")
	for _, m := range ms {
		if !isOnline(env, m) {
			out = append(out, offlineSkip("machine.load.budget", m))
			continue
		}
		if m.Load == nil {
			out = append(out, skip("machine.load.budget", machineSubject(m), m.Name+" has no load figures", "Nothing to compare with the budget."))
			continue
		}
		f := doctor.Finding{Check: "machine.load.budget", Subject: machineSubject(m)}
		if !m.LoadWarnSince.IsZero() {
			// Scheduling R5.77: over budget with jobs running warns at once, not after 10 minutes.
			f.Severity, f.Title = doctor.Warn, m.Name+" is over its load budget while jobs run"
			f.Detail = fmt.Sprintf("Memory %.0f%% of %d%%, CPU %.0f%% of %d%%. Running: %s. Running jobs are not stopped; new jobs wait until the load is under the budget.",
				m.Load.MemoryPct, m.BudgetMemoryPct, m.Load.CPUPct, m.BudgetCPUPct, runningText(m.RunningJobs))
			f.Fix = doctor.Fix{Text: "Lower the job slots or raise the budget on the Machines screen.", Link: machineLink(m)}
		} else if !m.OverBudgetSince.IsZero() && env.Now.Sub(m.OverBudgetSince) > OverBudgetFor {
			f.Severity, f.Title = doctor.Warn, m.Name+" has been over its load budget"
			f.Detail = fmt.Sprintf("For %s. Memory %.0f%% of %d%%, CPU %.0f%% of %d%%.",
				env.Now.Sub(m.OverBudgetSince).Round(time.Minute), m.Load.MemoryPct, m.BudgetMemoryPct, m.Load.CPUPct, m.BudgetCPUPct)
			f.Fix = doctor.Fix{Text: "Lower the job slots or raise the budget on the Machines screen.", Link: machineLink(m)}
		} else {
			f.Severity, f.Title = doctor.OK, m.Name+" is within its load budget"
		}
		out = append(out, f)
	}
	return out
}

// machineDisk: free space on the work volume. Under 15% or 10 GB warns; under
// 5% or 2 GB fails. Ported from delivery_doctor.py machine_items (machine.disk).
func machineDisk(ctx context.Context, env doctor.Env) []doctor.Finding {
	ms, out := scopedMachines(ctx, env, "machine.disk")
	for _, m := range ms {
		s := machineSubject(m)
		if !isOnline(env, m) {
			out = append(out, offlineSkip("machine.disk", m))
			continue
		}
		if m.DiskFreeBytes == nil || m.DiskTotalBytes == nil || *m.DiskTotalBytes <= 0 {
			out = append(out, skip("machine.disk", s, m.Name+" has not reported its disk", "The agent sent no free-space figure."))
			continue
		}
		free, total := *m.DiskFreeBytes, *m.DiskTotalBytes
		pct := float64(free) * 100 / float64(total)
		f := doctor.Finding{Check: "machine.disk", Subject: s,
			Detail: fmt.Sprintf("%.1f GB free of %.1f GB (%.0f%%).", float64(free)/float64(gib), float64(total)/float64(gib), pct)}
		switch {
		case pct < 5 || free < 2*gib:
			f.Severity, f.Title = doctor.Fail, m.Name+" is almost out of disk space"
		case pct < 15 || free < 10*gib:
			f.Severity, f.Title = doctor.Warn, m.Name+" is low on disk space"
		default:
			f.Severity, f.Title = doctor.OK, m.Name+" has enough disk space"
		}
		if f.Severity != doctor.OK {
			f.Fix = doctor.Fix{
				Text:    "Clean the work folders of idle runners.",
				Command: "crsp runners clean-work --machine " + m.Name,
				Link:    machineLink(m),
			}
		}
		out = append(out, f)
	}
	return out
}

// machineDiskGuard: reports a machine whose volume has under the guard limit
// (10% free), where the job-started hook holds new jobs (caches R5.38). The
// held jobs wait; this finding says why.
func machineDiskGuard(ctx context.Context, env doctor.Env) []doctor.Finding {
	ms, out := scopedMachines(ctx, env, "machine.guard.disk")
	for _, m := range ms {
		s := machineSubject(m)
		if !isOnline(env, m) {
			out = append(out, offlineSkip("machine.guard.disk", m))
			continue
		}
		if m.DiskFreeBytes == nil || m.DiskTotalBytes == nil || *m.DiskTotalBytes <= 0 {
			out = append(out, skip("machine.guard.disk", s, m.Name+" has not reported its disk", "The agent sent no free-space figure."))
			continue
		}
		d := runnercache.Disk{Free: *m.DiskFreeBytes, Total: *m.DiskTotalBytes}
		f := doctor.Finding{Check: "machine.guard.disk", Subject: s}
		if d.FreePct() < runnercache.DefaultMinFreePct {
			f.Severity, f.Title = doctor.Fail, "New jobs on "+m.Name+" are held: the disk is nearly full"
			f.Detail = fmt.Sprintf("%.1f%% free, under the %d%% limit. Jobs wait and go on when %d%% is free again.",
				d.FreePct(), runnercache.DefaultMinFreePct, runnercache.DefaultMinFreePct+runnercache.ResumeMarginPct)
			f.Fix = doctor.Fix{
				Text:    "Free space on the volume, or lower the cache quotas of the runners on " + m.Name + ".",
				Command: "crsp runners clean-work --machine " + m.Name,
				Link:    machineLink(m),
			}
		} else {
			f.Severity, f.Title = doctor.OK, "New jobs on "+m.Name+" are not held for disk space"
			f.Detail = fmt.Sprintf("%.1f%% free.", d.FreePct())
		}
		out = append(out, f)
	}
	return out
}

// machineDiskFloor: a machine paused because its free disk is under the floor
// (runners R5.83). It names what to clean, in the order that usually frees the
// most. The doctor deletes nothing (R5.7).
func machineDiskFloor(ctx context.Context, env doctor.Env) []doctor.Finding {
	ms, out := scopedMachines(ctx, env, "machine.floor.disk")
	for _, m := range ms {
		s := machineSubject(m)
		if !isOnline(env, m) {
			out = append(out, offlineSkip("machine.floor.disk", m))
			continue
		}
		f := doctor.Finding{Check: "machine.floor.disk", Subject: s}
		if m.PauseReason != machines.PauseReasonDiskLow {
			f.Severity, f.Title = doctor.OK, m.Name+" is not paused for disk space"
			out = append(out, f)
			continue
		}
		f.Severity, f.Title = doctor.Fail, "Jobs and VMs on "+m.Name+" are paused: free disk is under the floor"
		floor := m.DiskFloorGiB
		if floor <= 0 {
			floor = runnercache.DefaultDiskFloorGiB
		}
		f.Detail = fmt.Sprintf("The machine starts no job and no VM until it has the floor of %d GiB plus 1 GiB free (the floor is set per machine in the console).", floor)
		if m.DiskFreeBytes != nil {
			f.Detail = fmt.Sprintf("%.1f GiB free. %s", float64(*m.DiskFreeBytes)/float64(gib), f.Detail)
		}
		f.Fix = doctor.Fix{
			Text: "Free space on " + m.Name + ", biggest first: container images and build cache (`docker system df`, then `docker image prune -a` and `docker builder prune`); " +
				"runner caches (`crsp runners clean-work --machine " + m.Name + "`); job folders left by failed runs; VM disks and checkpoints of finished jobs (Hyper-V differencing disks, macOS clones). " +
				"Or lower the floor under Machines, \"Free disk to keep\" in the console.",
			Command: "crsp runners clean-work --machine " + m.Name,
			Link:    machineLink(m),
		}
		out = append(out, f)
	}
	return out
}

// runningText lists the running jobs of a machine for a finding.
func runningText(jobs []string) string {
	if len(jobs) == 0 {
		return "none listed"
	}
	return strings.Join(jobs, ", ")
}
