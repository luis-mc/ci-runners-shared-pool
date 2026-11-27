package checks

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/logs"
)

// CrashFileCheck lists the crash files in dir that were not reported yet
// (observability R5.31): a process crashed and nobody has seen it. The check
// only reads. It is not in Register because it needs the data folder; the
// controller adds it through DoctorDeps.Extra together with logs.ShowOnce.
func CrashFileCheck(dir string) doctor.Check {
	const id = "controller.crash_files"
	return doctor.NewCheck(id, doctor.GroupController, M1, false, func(context.Context, doctor.Env) []doctor.Finding {
		files, err := logs.Unreported(dir)
		if err != nil {
			return []doctor.Finding{skip(id, controllerSubject, "Crash files could not be read", err.Error())}
		}
		f := doctor.Finding{Check: id, Subject: controllerSubject}
		if len(files) == 0 {
			f.Severity, f.Title = doctor.OK, "No unreported crash files"
			return []doctor.Finding{f}
		}
		f.Severity = doctor.Warn
		f.Title = fmt.Sprintf("%d crash file(s) not reported yet", len(files))
		f.Detail = "A crsp process stopped on a panic or a fatal error. Each file holds the version, the system, the redacted message and the stack."
		f.Fix = doctor.Fix{Text: "Open the newest file in the crash folder and send it with a bug report."}
		for _, p := range files {
			f.Items = append(f.Items, doctor.Item{Kind: "crash_file", Name: filepath.Base(p), Path: p})
		}
		return []doctor.Finding{f}
	})
}
