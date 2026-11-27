package cpucap

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// cpuPeriodUS is the cgroup scheduling period.
const cpuPeriodUS = 100000

// CPUMax returns the cpu.max line for pct of ncpu cores: "max 100000" for 100
// and "<quota> 100000" otherwise. The quota is the microseconds of CPU time all
// the cgroup's processes together may use in one period, so 50% of 8 cores is
// 400000.
func CPUMax(pct, ncpu int) string {
	if pct >= 100 || ncpu < 1 {
		return "max " + strconv.Itoa(cpuPeriodUS)
	}
	quota := pct * ncpu * cpuPeriodUS / 100
	if quota < 1000 { // the kernel's minimum quota is 1 ms
		quota = 1000
	}
	return strconv.Itoa(quota) + " " + strconv.Itoa(cpuPeriodUS)
}

// ApplyCgroups writes cpu.max in the cgroup folder of every runner service
// under root (the cgroup v2 mount, normally /sys/fs/cgroup). crsp owns these
// folders: they are the system.slice cgroups of the runner services it set up.
// A missing cgroup v2 mount, or a folder it may not write, is reported and
// leaves the other runners as they were.
func ApplyCgroups(root string, ncpu, pct int) Result {
	if _, err := os.Stat(filepath.Join(root, "cgroup.controllers")); err != nil {
		return Result{State: NotSupported, Detail: "cgroup v2 is not mounted on this machine"}
	}
	dirs, err := filepath.Glob(filepath.Join(root, "system.slice", ServicePrefix+"*.service"))
	if err != nil {
		return Result{State: Failed, Detail: err.Error()}
	}
	line := CPUMax(pct, ncpu)
	var failed []string
	done := 0
	for _, d := range dirs {
		if err := os.WriteFile(filepath.Join(d, "cpu.max"), []byte(line), 0o644); err != nil { //nolint:gosec // a cgroup control file, mode is the kernel's
			failed = append(failed, filepath.Base(d)+": "+err.Error())
			continue
		}
		done++
	}
	if len(failed) > 0 {
		return Result{State: Failed, Targets: done, Detail: fmt.Sprintf("%d of %d runner services could not be capped (%s)", len(failed), len(dirs), failed[0])}
	}
	if len(dirs) == 0 {
		return Result{State: Applied, Detail: "no runner service yet; the cap is set when one starts"}
	}
	return Result{State: Applied, Targets: done}
}
