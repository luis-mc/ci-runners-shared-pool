//go:build windows

package toolstore

import (
	"context"
	"os/exec"
	"strconv"
)

// TaskkillTree kills a process and its children with taskkill /T /F.
type TaskkillTree struct{}

var _ TreeKiller = TaskkillTree{}

// KillTree implements TreeKiller.
func (TaskkillTree) KillTree(ctx context.Context, pid int) error {
	out, err := exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).CombinedOutput()
	if err != nil && !isGone(string(out)) {
		return &execError{err: err, out: string(out)}
	}
	return nil
}
