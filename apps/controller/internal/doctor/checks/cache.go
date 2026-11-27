package checks

import (
	"context"
	"fmt"
	"sort"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/caches"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
)

// cacheUsages reads the measured caches, or a skip finding when the fleet
// cannot list them or the controller has none to read yet.
func cacheUsages(ctx context.Context, env doctor.Env, check string) ([]doctor.CacheUsage, []doctor.Finding) {
	cf, ok := env.Fleet.(doctor.CacheFleet)
	if !ok {
		return nil, []doctor.Finding{skip(check, controllerSubject, "The cache sizes are not available", "This controller cannot list the caches the agents measured.")}
	}
	us, err := cf.Caches(ctx)
	if err != nil {
		return nil, []doctor.Finding{skipFor(check, controllerSubject, "The cache sizes", err)}
	}
	return us, nil
}

// cacheCap: a runner's cache is over its declared cap (caches R5.36). The
// eviction runs between jobs, so a cache can be over for a while while a job
// runs; the finding is a warning, never a failure, and the doctor deletes
// nothing (R5.7).
func cacheCap(ctx context.Context, env doctor.Env) []doctor.Finding {
	ms, out := scopedMachines(ctx, env, "cache.cap")
	if len(out) > 0 {
		return out
	}
	us, errs := cacheUsages(ctx, env, "cache.cap")
	if len(errs) > 0 {
		return errs
	}
	inScope := map[string]doctor.Machine{}
	for _, m := range ms {
		inScope[m.ID] = m
	}
	sort.Slice(us, func(i, j int) bool {
		if us[i].MachineID != us[j].MachineID {
			return us[i].MachineID < us[j].MachineID
		}
		if us[i].RunnerName != us[j].RunnerName {
			return us[i].RunnerName < us[j].RunnerName
		}
		return us[i].Kind < us[j].Kind
	})
	for _, u := range us {
		m, ok := inScope[u.MachineID]
		if !ok || u.Cap <= 0 {
			continue
		}
		name := u.RunnerName + " " + caches.KindName(u.Kind)
		f := doctor.Finding{Check: "cache.cap", Subject: doctor.Subject{Kind: "runner", ID: u.MachineID + "/" + u.RunnerName + "/" + u.Kind, Name: name}}
		if u.Size > u.Cap {
			f.Severity, f.Title = doctor.Warn, "The "+name+" on "+m.Name+" is over its cap"
			f.Detail = fmt.Sprintf("%s held, cap %s. It is brought under the cap before and after the next job of this runner.",
				caches.FormatBytes(u.Size), caches.FormatBytes(u.Cap))
			f.Fix = doctor.Fix{
				Text: "Wait for the next job of this runner, or lower the cache keys in the settings, or wipe this cache on the Images and caches screen.",
				Link: machineLink(m),
			}
		} else {
			f.Severity, f.Title = doctor.OK, "The "+name+" on "+m.Name+" is within its cap"
			f.Detail = fmt.Sprintf("%s held, cap %s.", caches.FormatBytes(u.Size), caches.FormatBytes(u.Cap))
		}
		out = append(out, f)
	}
	return out
}

// cacheDisk: free disk on a drive that holds runner caches is under the guard
// limit (caches R5.38). The guard itself fails machine.guard.disk at the limit;
// this one warns first on the machines that hold caches, naming the caches as
// the first thing to look at.
func cacheDisk(ctx context.Context, env doctor.Env) []doctor.Finding {
	ms, out := scopedMachines(ctx, env, "cache.disk")
	if len(out) > 0 {
		return out
	}
	us, errs := cacheUsages(ctx, env, "cache.disk")
	if len(errs) > 0 {
		return errs
	}
	held := map[string]int64{}
	for _, u := range us {
		held[u.MachineID] += u.Size
	}
	for _, m := range ms {
		total, has := held[m.ID]
		if !has {
			continue
		}
		s := machineSubject(m)
		if !isOnline(env, m) {
			out = append(out, offlineSkip("cache.disk", m))
			continue
		}
		if m.DiskFreeBytes == nil || m.DiskTotalBytes == nil || *m.DiskTotalBytes <= 0 {
			out = append(out, skip("cache.disk", s, m.Name+" has not reported its disk", "The agent sent no free-space figure."))
			continue
		}
		d := runnercache.Disk{Free: *m.DiskFreeBytes, Total: *m.DiskTotalBytes}
		f := doctor.Finding{Check: "cache.disk", Subject: s}
		if d.FreePct() < runnercache.DefaultMinFreePct {
			f.Severity, f.Title = doctor.Warn, "The disk of "+m.Name+" is low and it holds caches"
			f.Detail = fmt.Sprintf("%.1f%% free, under %d%%. The runner caches on it hold %s.",
				d.FreePct(), runnercache.DefaultMinFreePct, caches.FormatBytes(total))
			f.Fix = doctor.Fix{Text: "Lower the cache budget or cap in the settings, or wipe a cache on the Images and caches screen.", Link: machineLink(m)}
		} else {
			f.Severity, f.Title = doctor.OK, "The disk of "+m.Name+" has room for its caches"
			f.Detail = fmt.Sprintf("%.1f%% free; the runner caches hold %s.", d.FreePct(), caches.FormatBytes(total))
		}
		out = append(out, f)
	}
	return out
}
