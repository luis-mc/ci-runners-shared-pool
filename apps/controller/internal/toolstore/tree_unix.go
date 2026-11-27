//go:build !windows

package toolstore

import (
	"context"
	"errors"
	"syscall"
)

// GroupTree kills the process group of a job: the job is started as a group leader (Setpgid), so the group
// holds every process it started.
type GroupTree struct{}

var _ TreeKiller = GroupTree{}

// KillTree implements TreeKiller.
func (GroupTree) KillTree(_ context.Context, pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
