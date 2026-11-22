package checks

import (
	"context"
	"fmt"

	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler/slots"
)

// runnerSlots: a machine has no more runners for one repository than job
// slots, and the longest the job-started hook holds a job is within the 2 minute
// hold cap (scheduling R5.75). A job that a runner takes and then holds for a slot loses that time
// from its timeout-minutes (scheduling R5.73, R5.75). New check.
func runnerSlots(ctx context.Context, env doctor.Env) []doctor.Finding {
	ms, errs := scopedMachines(ctx, env, "runner.slots")
	if errs != nil {
		return errs
	}
	var out []doctor.Finding
	for _, m := range ms {
		if m.JobSlots <= 0 {
			continue
		}
		f := doctor.Finding{Check: "runner.slots", Subject: machineSubject(m), Severity: doctor.OK,
			Title: fmt.Sprintf("%s: %d runners for the busiest repository, %d job slots, hold at most %d min", m.Name, m.RunnerPeak, m.JobSlots, m.GateMaxWaitMin)}
		switch {
		case m.RunnerPeak > m.JobSlots:
			who := "the pool"
			if m.RunnerPeakRepo != "" {
				who = m.RunnerPeakRepo
			}
			f.Severity = doctor.Warn
			f.Title = fmt.Sprintf("%s has %d runners for %s and only %d job slots", m.Name, m.RunnerPeak, who, m.JobSlots)
			f.Detail = "A runner with no free slot still takes a job from GitHub and holds it before checkout. That wait counts against the job's timeout-minutes, so a job can be cancelled after little real work, and the extra runners add no parallel jobs."
			f.Fix = doctor.Fix{Text: fmt.Sprintf("On the machine page remove runners from %s until there are at most %d, or raise Job slots (Pool size sets how many fit).", m.Name, m.JobSlots), Link: machineLink(m)}
		case m.GateMaxWaitMin > slots.MaxHoldMin:
			f.Severity = doctor.Warn
			f.Title = fmt.Sprintf("%s holds a job up to %d min, above the %d min hold cap", m.Name, m.GateMaxWaitMin, slots.MaxHoldMin)
			f.Detail = fmt.Sprintf("The hold counts against the job's timeout-minutes. The hook cuts any wait to %d min, but the saved setting should say so.", slots.MaxHoldMin)
			f.Fix = doctor.Fix{Text: fmt.Sprintf("On the machine page set Gate maximum wait to %d minutes or less.", slots.MaxHoldMin), Link: machineLink(m)}
		}
		out = append(out, f)
	}
	return out
}
