package checks

import (
	"context"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

// machineReset warns when a machine's settings or runners look reset: it joined
// again, it has default settings while an earlier machine of the same computer
// had chosen ones, or runner records belong to a machine that is not in the
// pool (doctor R5.61, machines R5.70). New check.
func machineReset(ctx context.Context, env doctor.Env) []doctor.Finding {
	ms, errs := scopedMachines(ctx, env, "machine.reset")
	if errs != nil {
		return errs
	}
	var out []doctor.Finding
	for _, m := range ms {
		f := doctor.Finding{Check: "machine.reset", Subject: machineSubject(m), Severity: doctor.OK, Title: m.Name + ": settings and runners kept"}
		if len(m.ResetSigns) > 0 {
			f.Severity = doctor.Warn
			f.Title = m.Name + " may have lost its settings or runners"
			f.Detail = strings.Join(m.ResetSigns, "; ") + "."
			f.Fix = doctor.Fix{Text: "Check the machine page: allowed backends, pool size and runners. Set them again if they went back to the defaults; the audit log has the machine.rejoin entry.", Link: machineLink(m)}
		}
		out = append(out, f)
	}
	return out
}
