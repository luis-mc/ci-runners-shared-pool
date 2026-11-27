package hooks

import (
	"os"
	"runtime"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

// HolderPID is the process whose death means the job is gone: CRSP_HOLDER_PID
// when set, else the hook's parent, skipping a cmd, powershell or pwsh shell
// on Windows (R5.31).
func HolderPID(getenv func(string) string) int64 {
	return slots.HolderPID(getenv("CRSP_HOLDER_PID"), int64(os.Getppid()), runtime.GOOS == "windows", processInfo)
}
