//go:build linux

package cpucap

import (
	"context"
	"runtime"
)

type osApplier struct{}

func newOS() Applier { return osApplier{} }

// Apply implements Applier with cgroup v2 cpu.max (R5.64).
func (osApplier) Apply(_ context.Context, pct int) Result {
	return ApplyCgroups("/sys/fs/cgroup", runtime.NumCPU(), pct)
}
