//go:build !windows

package hooks

import (
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnerprocs"
	"io/fs"
	"os"
	"strconv"
	"syscall"
)

// NewCleaner is the real cleaner of a runner account on Linux or macOS.
func NewCleaner(getenv func(string) string, phase string, log func(string)) *Cleaner {
	return &Cleaner{
		Getenv: getenv, Phase: phase, Self: strconv.Itoa(os.Getuid()), Pid: int64(os.Getpid()),
		Procs: runnerprocs.List, Owner: fileOwner, Shared: []string{"/tmp"}, Log: log,
		End: runnerprocs.End,
	}
}

func fileOwner(fi fs.FileInfo) string {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return strconv.FormatUint(uint64(st.Uid), 10)
	}
	return ""
}
