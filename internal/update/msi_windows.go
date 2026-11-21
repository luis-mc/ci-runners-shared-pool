//go:build windows

package update

import (
	"context"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// StartMSIDetached starts msiexec detached from the service: the upgrade stops
// this very process, and msiexec must go on.
func StartMSIDetached(_ context.Context, msi, log string) (int, error) {
	start := func(flags uint32) (*exec.Cmd, error) {
		// Background, never the caller's context: msiexec must outlive this process.
		cmd := exec.CommandContext(context.Background(), "msiexec", MSIArgs(msi, log)...) //nolint:gosec // fixed program, our own file
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
		return cmd, cmd.Start()
	}
	cmd, err := start(detachedProcess | createNewProcessGroup | createBreakawayFromJob)
	if err != nil {
		cmd, err = start(detachedProcess | createNewProcessGroup)
	}
	if err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()
	return pid, nil
}

// InstallerBusy says whether another install runs: the Global\_MSIExecute mutex can be
// opened only while one does. The name comes from the command lines of the msiexec processes.
func InstallerBusy() (bool, string) {
	name, err := windows.UTF16PtrFromString(`Global\_MSIExecute`)
	if err != nil {
		return false, ""
	}
	h, err := windows.OpenMutex(windows.SYNCHRONIZE, false, name)
	if err != nil {
		return false, ""
	}
	_ = windows.CloseHandle(h)
	return true, otherInstallerName()
}

// otherInstallerName asks Windows for the command lines of the running msiexec processes.
func otherInstallerName() string {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", //nolint:gosec // fixed command
		"(Get-CimInstance Win32_Process -Filter \"Name='msiexec.exe'\").CommandLine")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return InstallerName(strings.Split(string(out), "\n"))
}
