package checks

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
)

// timersMode tells the owner when crsp's timers only report (doctor R5.64,
// scheduling R5.99). Observe mode is the default, so this is information, never
// a warning: jobs over their limit are reported and not stopped until the
// owner switches enforcement on in Settings. New check.
func timersMode(ctx context.Context, env doctor.Env) []doctor.Finding {
	if env.Timers == nil {
		return []doctor.Finding{skip("timers.mode", controllerSubject, "The timers are not running", "Nothing was checked.")}
	}
	on, err := env.Timers.TimersEnforced(ctx)
	if err != nil {
		return []doctor.Finding{skipFor("timers.mode", controllerSubject, "The timer settings", err)}
	}
	f := doctor.Finding{Check: "timers.mode", Subject: controllerSubject, Severity: doctor.OK, Title: "Timers stop jobs and cancel runs that run over"}
	if !on {
		f.Severity = doctor.Info
		f.Title = "Timers are in observe mode: jobs over the limit are reported, not stopped"
		f.Detail = "The countdowns run and the audit log shows what crsp would have stopped or cancelled. Nothing is stopped until you switch enforcement on."
		f.Fix = doctor.Fix{Text: "When the reports look right, switch on Enforce timers in Settings.", Link: "/settings"}
	}
	return []doctor.Finding{f}
}
