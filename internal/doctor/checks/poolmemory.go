package checks

import (
	"context"
	"fmt"

	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
)

const giBytes = float64(1 << 30)

// machinePoolMemory: the configured pool memory and the `.wslconfig` cap must
// both fit in what is left of the PC after its own use, total memory less the
// measured 7-day p95 of everything that is not the pool (machines R5.68,
// OPS61). Nothing is said while fewer than 24 hours are measured, or when the
// machine has neither a pool size nor a cap.
func machinePoolMemory(ctx context.Context, env doctor.Env) []doctor.Finding {
	ms, out := scopedMachines(ctx, env, "machine.pool.memory")
	for _, m := range ms {
		p := m.PoolMemory
		if p.TotalBytes <= 0 || !p.HostReady || (p.PoolGiB <= 0 && p.WSLCapBytes <= 0) {
			continue
		}
		if !isOnline(env, m) {
			out = append(out, offlineSkip("machine.pool.memory", m))
			continue
		}
		room := (float64(p.TotalBytes) - float64(p.HostP95Bytes)) / giBytes
		hostGiB := float64(p.HostP95Bytes) / giBytes
		totalGiB := float64(p.TotalBytes) / giBytes
		f := doctor.Finding{Check: "machine.pool.memory", Subject: machineSubject(m)}
		var over []string
		if p.PoolGiB > room {
			over = append(over, fmt.Sprintf("the pool is %.1f GiB", p.PoolGiB))
		}
		if capGiB := float64(p.WSLCapBytes) / giBytes; p.WSLCapBytes > 0 && capGiB > room {
			over = append(over, fmt.Sprintf("the WSL memory cap in .wslconfig is %.1f GiB", capGiB))
		}
		if len(over) == 0 {
			f.Severity, f.Title = doctor.OK, "The pool on "+m.Name+" leaves room for the PC"
			out = append(out, f)
			continue
		}
		f.Severity = doctor.Warn
		f.Title = "The pool on " + m.Name + " is bigger than the PC can spare"
		f.Detail = fmt.Sprintf("On %s, %s. Windows and the programs of the PC use up to %.1f GiB (p95, 7 days) of its %.1f GiB, so the pool and the WSL cap should stay at or below %.1f GiB. A change to .wslconfig applies after WSL restarts.",
			m.Name, joinAnd(over), hostGiB, totalGiB, room)
		f.Fix = doctor.Fix{
			Text: "Open the Machine page: the Pool size section shows the recommended size and the .wslconfig memory to match.",
			Link: machineLink(m),
		}
		out = append(out, f)
	}
	return out
}

func joinAnd(xs []string) string {
	if len(xs) == 2 {
		return xs[0] + " and " + xs[1]
	}
	return xs[0]
}
