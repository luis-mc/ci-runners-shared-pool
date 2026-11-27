//go:build !windows

package tray

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type osProcs struct{ proc string }

func newProcs() procLister { return osProcs{proc: "/proc"} }

// Trays reads /proc (Linux); other Unix systems have none, and the tray there
// is ended by SIGTERM from the package scripts.
func (o osProcs) Trays(dir string) ([]int, error) {
	entries, err := os.ReadDir(o.proc)
	if err != nil {
		return nil, nil //nolint:nilerr // no /proc: no trays to find
	}
	var out []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(o.proc, e.Name(), "cmdline")) //nolint:gosec // /proc entry
		if err != nil {
			continue
		}
		if isTrayCmdline(strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00"), dir) {
			out = append(out, pid)
		}
	}
	return out, nil
}

func (osProcs) Ask(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }
func (osProcs) Alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
func (osProcs) Kill(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }
