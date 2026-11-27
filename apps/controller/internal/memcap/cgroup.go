package memcap

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// CgroupEnforcer caps a job with a child cgroup (v2) of the pool container's
// own cgroup: memory.max is the cap, memory.swap.max 0 so the cap is the cap,
// and memory.oom.group 1 so the kernel kills the whole tree, not one process.
// Root is the cgroup directory of the pool container, normally
// /sys/fs/cgroup inside it. It is plain files, so tests use a temp directory.
type CgroupEnforcer struct{ Root string }

var _ Enforcer = CgroupEnforcer{}

// Name implements Enforcer.
func (CgroupEnforcer) Name() string { return "cgroup" }

// Start implements Enforcer.
func (c CgroupEnforcer) Start(j Job, limit int64) (Guard, error) {
	if limit <= 0 {
		return nil, ErrNoLimit
	}
	if j.ID == "" || strings.ContainsAny(j.ID, `/\`) || j.ID == "." || j.ID == ".." {
		return nil, fmt.Errorf("memcap: bad job id %q", j.ID)
	}
	dir := filepath.Join(c.Root, "crsp-job-"+j.ID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("memcap: make cgroup: %w", err)
	}
	for _, kv := range [][2]string{
		{"memory.max", strconv.FormatInt(limit, 10)},
		{"memory.swap.max", "0"},
		{"memory.oom.group", "1"},
		{"cgroup.procs", strconv.Itoa(j.PID)},
	} {
		if err := os.WriteFile(filepath.Join(dir, kv[0]), []byte(kv[1]), 0o600); err != nil {
			return nil, fmt.Errorf("memcap: write %s: %w", kv[0], err)
		}
	}
	return &cgroupGuard{dir: dir}, nil
}

type cgroupGuard struct{ dir string }

// Exceeded is true when memory.events counts an oom_kill in the job's cgroup.
func (g *cgroupGuard) Exceeded() bool {
	b, err := os.ReadFile(filepath.Join(g.dir, "memory.events"))
	if err != nil {
		return false
	}
	return OOMKills(string(b)) > 0
}

// Release removes the job's cgroup; the kernel refuses while a process is in
// it, which is reported.
func (g *cgroupGuard) Release() error {
	if err := os.Remove(g.dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("memcap: remove cgroup: %w", err)
	}
	return nil
}

// OOMKills reads the oom_kill count of a memory.events file.
func OOMKills(events string) int {
	for _, line := range strings.Split(events, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok && k == "oom_kill" {
			n, _ := strconv.Atoi(strings.TrimSpace(v))
			return n
		}
	}
	return 0
}
