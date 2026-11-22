//go:build !windows

package main

import (
	"context"
	"os/exec"
	"syscall"
)

// startDetached runs crsp in its own session, so it outlives the service that asked.
func startDetached(exe string, args []string) error {
	cmd := exec.CommandContext(context.Background(), exe, args...) //nolint:gosec // our own binary
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return detach(cmd)
}
