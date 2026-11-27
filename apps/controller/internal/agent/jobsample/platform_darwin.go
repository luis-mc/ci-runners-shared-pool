//go:build darwin

package jobsample

import (
	"context"
	"os/exec"
	"time"
)

// NewPlatform returns the reader of macOS (OPS66 R5.62): the process tree from ps. Containers do not run on
// macOS (the VM backend reads inside the VM), so there is no cgroup reader.
func NewPlatform() Platform {
	run := func() ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "ps", PSArgs...).Output()
	}
	return Platform{ProcessTree: NewProcessTreeSampler(PSTable{Run: run})}
}
