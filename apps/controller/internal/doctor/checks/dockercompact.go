package checks

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/dockercompact"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

// machineDockerCompact: the automatic compact of Docker's disk file needs the
// agent to run with administrator rights (diskpart and Optimize-VHD refuse
// otherwise). A machine whose policy is on and whose agent has no such rights
// would skip the compact every night, so the finding says so and offers the
// console action (caches R5.51 to R5.54). New check.
func machineDockerCompact(ctx context.Context, env doctor.Env) []doctor.Finding {
	ms, out := scopedMachines(ctx, env, "machine.docker.compact")
	for _, m := range ms {
		d := m.DockerCompact
		if d == nil || !d.Enabled {
			continue
		}
		if !isOnline(env, m) {
			out = append(out, offlineSkip("machine.docker.compact", m))
			continue
		}
		f := doctor.Finding{Check: "machine.docker.compact", Subject: machineSubject(m)}
		if d.CanCompact {
			f.Severity, f.Title = doctor.OK, "The agent on "+m.Name+" can make Docker's disk smaller"
			out = append(out, f)
			continue
		}
		f.Severity = doctor.Warn
		f.Title = "The agent on " + m.Name + " cannot make Docker's disk smaller"
		f.Detail = "Docker's disk file on " + m.Name + " is " + dockercompact.FormatBytes(d.FileBytes) + " on disk and the automatic compact is on, but the agent does not run with administrator rights, so the compact is skipped every night."
		f.Fix = doctor.Fix{
			Text:    "Open the Machine page and use \"Run the agent as administrator\" to see the steps, or turn the automatic compact off there.",
			Command: "crsp machine settings " + m.Name + " --docker-compact off",
			Link:    machineLink(m),
		}
		out = append(out, f)
	}
	return out
}
