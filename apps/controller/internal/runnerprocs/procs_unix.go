//go:build !windows

package runnerprocs

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Self is the account this process runs as.
func Self() string { return strconv.Itoa(os.Getuid()) }

// End ends one process at once.
func End(pid int64) error { return syscall.Kill(int(pid), syscall.SIGKILL) }

func owner(fi fs.FileInfo) string {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return strconv.FormatUint(uint64(st.Uid), 10)
	}
	return ""
}

// List reads /proc where it exists, else asks ps.
func List() ([]Proc, error) {
	if _, err := os.Stat("/proc/self/stat"); err == nil {
		return procFS("/proc")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,uid=,comm=").Output()
	if err != nil {
		return nil, err
	}
	return parsePS(string(out)), nil
}

func parsePS(out string) []Proc {
	var res []Proc
	for _, ln := range strings.Split(out, "\n") {
		f := strings.Fields(ln)
		if len(f) < 4 {
			continue
		}
		pid, e1 := strconv.ParseInt(f[0], 10, 64)
		ppid, e2 := strconv.ParseInt(f[1], 10, 64)
		if e1 != nil || e2 != nil {
			continue
		}
		res = append(res, Proc{PID: pid, PPID: ppid, Owner: f[2], Name: filepath.Base(strings.Join(f[3:], " "))})
	}
	return res
}

func procFS(root string) ([]Proc, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var res []Proc
	for _, e := range entries {
		pid, err := strconv.ParseInt(e.Name(), 10, 64)
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, e.Name(), "stat")) //nolint:gosec // /proc
		if err != nil {
			continue
		}
		i, j := bytes.IndexByte(b, '('), bytes.LastIndexByte(b, ')')
		if i < 0 || j < i {
			continue
		}
		rest := strings.Fields(string(b[j+1:]))
		if len(rest) < 2 {
			continue
		}
		ppid, _ := strconv.ParseInt(rest[1], 10, 64)
		info, err := e.Info()
		if err != nil {
			continue
		}
		res = append(res, Proc{PID: pid, PPID: ppid, Owner: owner(info), Name: string(b[i+1 : j])})
	}
	return res, nil
}
