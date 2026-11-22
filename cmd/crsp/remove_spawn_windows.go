//go:build windows

package main

import (
	"context"
	"os/exec"
	"syscall"
)

const (
	rmDetachedProcess        = 0x00000008
	rmCreateNewProcessGroup  = 0x00000200
	rmCreateBreakawayFromJob = 0x01000000
)

// startDetached runs crsp outside the service: removing crsp stops the service that asked.
func startDetached(exe string, args []string) error {
	run := func(flags uint32) error {
		cmd := exec.CommandContext(context.Background(), exe, args...) //nolint:gosec // our own binary
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
		return detach(cmd)
	}
	if err := run(rmDetachedProcess | rmCreateNewProcessGroup | rmCreateBreakawayFromJob); err != nil {
		// The service's job forbids breakaway: start inside it; the installer stops the service last.
		return run(rmDetachedProcess | rmCreateNewProcessGroup)
	}
	return nil
}
