//go:build windows

package update

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

const (
	detachedProcess        = 0x00000008
	createNewProcessGroup  = 0x00000200
	createBreakawayFromJob = 0x01000000
)

// Spawn starts `crsp update-helper` from a copy of the binary in the work
// folder, detached from the service (new process group, outside its job when
// the job allows breakaway). The MSI replaces crsp.exe in Program Files, so the
// helper must not run from there.
func Spawn(_ context.Context, exe string, p Plan, planPath string) (int, error) {
	copyPath := filepath.Join(p.Dir, "crsp-update-helper.exe")
	if err := copyFile(exe, copyPath, 0o700); err != nil {
		return 0, fmt.Errorf("copy the helper: %w", err)
	}
	start := func(flags uint32) (*exec.Cmd, error) {
		// Background, never the caller's context: the helper must outlive this process.
		cmd := exec.CommandContext(context.Background(), copyPath, "update-helper", planPath) //nolint:gosec // our own copy
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
		return cmd, cmd.Start()
	}
	cmd, err := start(detachedProcess | createNewProcessGroup | createBreakawayFromJob)
	if err != nil {
		// The service's job forbids breakaway: start inside it; the MSI stops the service last.
		cmd, err = start(detachedProcess | createNewProcessGroup)
	}
	if err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()
	return pid, nil
}

// ExecutablePath is the path of the running binary.
func ExecutablePath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}
