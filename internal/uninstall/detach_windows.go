//go:build windows

package uninstall

import (
	"context"
	"os/exec"
	"syscall"
)

// Process creation flags: the new process has no console, is its own process group and leaves the job
// object of its parent, so it survives the end of this program (and of a CI step's job).
const (
	detachedProcess     = 0x00000008
	createNewProcessGrp = 0x00000200
	createBreakawayJob  = 0x01000000
)

// StartDetached starts a program that outlives this process and does not wait for it.
func StartDetached(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(context.WithoutCancel(ctx), name, args...) //nolint:gosec // msiexec with a validated ProductCode
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGrp | createBreakawayJob}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
