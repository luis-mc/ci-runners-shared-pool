//go:build linux

package jobsample

import "os"

// NewPlatform returns the readers of Linux (OPS66 R5.62): the cgroup v2 files of a container or VM job and the
// /proc process tree of a persistent runner.
func NewPlatform() Platform {
	return Platform{Cgroup: NewCgroupSampler(os.ReadFile), ProcessTree: NewProcessTreeSampler(ProcFS{})}
}
