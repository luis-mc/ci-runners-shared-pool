package checks

import (
	"context"
	"fmt"

	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
)

// Days behind the newest runner release at which the doctor warns and fails
// (operations R5.20: crsp moves the pin within 7 days; GitHub stops a runner
// that does not update itself 30 days after a newer release).
const (
	runnerPinWarnDays = 21
	runnerPinFailDays = 28
)

// runnerPinAge says how far the pinned GitHub runner version is behind the
// newest release (doctor R5.67, operations R5.20). New check.
func runnerPinAge(_ context.Context, env doctor.Env) []doctor.Finding {
	if env.RunnerPin == nil {
		return []doctor.Finding{skip("runner.pin_age", controllerSubject, "The runner version check is not running", "Nothing was checked.")}
	}
	st := env.RunnerPin.RunnerPin()
	if !st.Known {
		return []doctor.Finding{skip("runner.pin_age", controllerSubject, "The newest runner version is not known yet", "crsp reads it once a day.")}
	}
	f := doctor.Finding{Check: "runner.pin_age", Subject: controllerSubject, Severity: doctor.OK,
		Title: "The pinned runner version " + st.Pinned + " is the newest, or newer than a week"}
	if st.BehindDays >= runnerPinWarnDays {
		f.Severity = doctor.Warn
		if st.BehindDays >= runnerPinFailDays {
			f.Severity = doctor.Fail
		}
		f.Title = fmt.Sprintf("The pinned runner version %s is %d days behind %s", st.Pinned, st.BehindDays, st.Latest)
		f.Detail = "GitHub stops a runner that does not update itself 30 days after a newer release. crsp moves the pin by itself once a release is 7 days old; it did not, so look at why."
		f.Fix = doctor.Fix{Text: "Check that this PC can reach api.github.com, then open the audit log for runner.pin.move.", Link: "/settings/audit"}
	}
	return []doctor.Finding{f}
}
