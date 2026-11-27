package checks

import (
	"context"
	"fmt"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/logs"
)

// LogStoreCheck reports the failures of the log database (observability R6.9):
// logs fail open, so a full or broken logs.db never stops a job, and this check
// is where the owner learns lines were lost. Like CrashFileCheck it is not in
// Register; the controller adds it through DoctorDeps.Extra with
// (*logs.FailOpen).Stats.
func LogStoreCheck(stats func() logs.Stats) doctor.Check {
	const id = "controller.log_store"
	return doctor.NewCheck(id, doctor.GroupController, M1, false, func(context.Context, doctor.Env) []doctor.Finding {
		st := stats()
		f := doctor.Finding{Check: id, Subject: controllerSubject}
		if st.Failures == 0 && st.Dropped == 0 {
			f.Severity, f.Title = doctor.OK, "The log database writes without failures"
			return []doctor.Finding{f}
		}
		f.Severity = doctor.Warn
		f.Title = fmt.Sprintf("%d log line(s) were lost (%d failed write(s))", st.Dropped, st.Failures)
		f.Detail = "Jobs and runners were not affected. Last error: " + st.LastError
		f.Fix = doctor.Fix{Text: "Free disk space in the data folder, or check that logs.db is writable."}
		return []doctor.Finding{f}
	})
}
