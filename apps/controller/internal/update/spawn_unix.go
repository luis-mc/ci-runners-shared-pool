//go:build !windows

package update

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// Spawn starts `crsp update-helper` so that it survives the service stopping.
// It runs a copy of the binary from the work folder, so replacing the real one
// never touches the running helper. On Linux the helper runs as a transient
// systemd unit, outside the service's cgroup (a restart kills the cgroup); on
// macOS it starts in its own session, which launchd's kickstart leaves alone.
// It returns the helper's pid when it knows it (0 under systemd-run).
func Spawn(ctx context.Context, exe string, p Plan, planPath string) (int, error) {
	copyPath := filepath.Join(p.Dir, "crsp-update-helper")
	if err := copyFile(exe, copyPath, 0o700); err != nil {
		return 0, fmt.Errorf("copy the helper: %w", err)
	}
	if runtime.GOOS == "linux" {
		if sr, err := exec.LookPath("systemd-run"); err == nil {
			unit := fmt.Sprintf("crsp-update-%d", time.Now().Unix())
			out, err := exec.CommandContext(ctx, sr, "--collect", "--quiet", "--unit", unit, copyPath, "update-helper", planPath).CombinedOutput() //nolint:gosec // our own copy
			if err == nil {
				return 0, nil
			}
			_ = out // fall through to a plain detached start
		}
	}
	cmd := exec.CommandContext(context.WithoutCancel(ctx), copyPath, "update-helper", planPath) //nolint:gosec // our own copy
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()
	return pid, nil
}

// ExecutablePath is the path of the running binary with links resolved.
func ExecutablePath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}
