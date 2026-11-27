//go:build !windows

package uninstall

import (
	"context"
	"os/exec"
	"syscall"
)

// StartDetached starts a program that outlives this process and does not wait for it.
func StartDetached(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(context.WithoutCancel(ctx), name, args...) //nolint:gosec // the caller names the program
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
