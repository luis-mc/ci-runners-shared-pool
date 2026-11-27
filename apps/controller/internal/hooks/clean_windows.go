//go:build windows

package hooks

import (
	"io/fs"
	"os"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnerprocs"
)

// NewCleaner is the real cleaner of a runner account on Windows. The temp
// folder of the service account is private to it, so every entry there is the
// account's own.
func NewCleaner(getenv func(string) string, phase string, log func(string)) *Cleaner {
	self := currentSID()
	return &Cleaner{
		Getenv: getenv, Phase: phase, Self: self, Pid: int64(os.Getpid()),
		Procs: runnerprocs.List, Log: log,
		Owner: func(fs.FileInfo) string { return self },
		End:   runnerprocs.End,
	}
}

func currentSID() string { return runnerprocs.Self() }
