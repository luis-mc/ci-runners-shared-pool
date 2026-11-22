//go:build windows

package uninstall

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
)

// Process creation flags: the new process has no console and is its own process group; it leaves the job
// object of its parent when the job allows it, so it survives the end of this program (and of a CI step's job).
const (
	detachedProcess     = 0x00000008
	createNewProcessGrp = 0x00000200
	createBreakawayJob  = 0x01000000
)

// errorAccessDenied is what CreateProcess returns for CREATE_BREAKAWAY_FROM_JOB inside a job that forbids it.
const errorAccessDenied syscall.Errno = 5

// StartDetached starts a program that outlives this process and does not wait for it.
func StartDetached(ctx context.Context, name string, args ...string) error {
	return startDetachedWith(ctx, realStart, name, args...)
}

// startFunc starts name with the given process creation flags.
type startFunc func(ctx context.Context, flags uint32, name string, args ...string) error

func realStart(ctx context.Context, flags uint32, name string, args ...string) error {
	cmd := exec.CommandContext(context.WithoutCancel(ctx), name, args...) //nolint:gosec // msiexec with a validated ProductCode
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// startDetachedWith asks for job breakaway first; a job object that forbids breakaway answers access denied,
// and then the program is started without it (still detached, in its own process group).
func startDetachedWith(ctx context.Context, start startFunc, name string, args ...string) error {
	err := start(ctx, detachedProcess|createNewProcessGrp|createBreakawayJob, name, args...)
	if err != nil && errors.Is(err, errorAccessDenied) {
		return start(ctx, detachedProcess|createNewProcessGrp, name, args...)
	}
	return err
}
