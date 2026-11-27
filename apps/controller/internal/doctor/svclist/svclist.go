// Package svclist finds runner services in the OS service list (doctor R5.11).
//
// The agent never has to read a runner folder to find a runner: Windows
// services named actions.runner.*, systemd units actions.runner.*.service and
// launchd jobs actions.runner.* name them (dev-standards learning 6). The
// parsers are pure functions over command output; List runs the command for
// the OS through a Runner so tests and other callers can fake it.
//
// Ported from dev-standards delivery_doctor.py service_rows and
// _windows_services; tests in svclist_test.go follow test_dl318_doctor_locked_runner.py
// test_service_rows.
package svclist

import (
	"context"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

// Prefix starts the name of every runner service.
const Prefix = "actions.runner."

// Runner runs a command and returns its output. The agent supplies one.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Commands per OS. Windows prints name|state|startmode; systemd prints its
// list of units, and its unit files give the enabled state.
var (
	WindowsCommand = []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		`Get-CimInstance Win32_Service -Filter "Name LIKE 'actions.runner.%'" | ForEach-Object { $_.Name + '|' + $_.State + '|' + $_.StartMode }`}
	SystemdUnitsCommand = []string{"systemctl", "list-units", "--type=service", "--all", "--no-legend", "--plain", "--no-pager", "actions.runner.*"}
	SystemdFilesCommand = []string{"systemctl", "list-unit-files", "--no-legend", "--no-pager", "actions.runner.*"}
	LaunchdCommand      = []string{"launchctl", "list"}
)

// List returns the runner services of this OS ("windows", "linux", "macos").
func List(ctx context.Context, osName string, run Runner) ([]doctor.OSService, error) {
	switch osName {
	case "windows":
		out, err := run(ctx, WindowsCommand[0], WindowsCommand[1:]...)
		if err != nil {
			return nil, err
		}
		return ParseWindows(string(out)), nil
	case "linux":
		units, err := run(ctx, SystemdUnitsCommand[0], SystemdUnitsCommand[1:]...)
		if err != nil {
			return nil, err
		}
		files, _ := run(ctx, SystemdFilesCommand[0], SystemdFilesCommand[1:]...) // enabled state is optional
		return ParseSystemd(string(units), string(files)), nil
	case "macos":
		out, err := run(ctx, LaunchdCommand[0], LaunchdCommand[1:]...)
		if err != nil {
			return nil, err
		}
		return ParseLaunchd(string(out)), nil
	}
	return nil, nil
}

// SplitName splits "actions.runner.<scope>.<runner>" into scope and runner
// name. The scope is "owner-repo" or an organization and has no dot, so the
// runner name is what follows the first dot. ok is false for other names.
func SplitName(service string) (scope, runner string, ok bool) {
	service = strings.TrimSuffix(service, ".service")
	rest, found := strings.CutPrefix(service, Prefix)
	if !found {
		return "", "", false
	}
	scope, runner, found = strings.Cut(rest, ".")
	if !found || scope == "" || runner == "" {
		return "", "", false
	}
	return scope, runner, true
}

func make1(name string) (doctor.OSService, bool) {
	scope, runner, ok := SplitName(name)
	if !ok {
		return doctor.OSService{}, false
	}
	return doctor.OSService{Name: strings.TrimSuffix(name, ".service"), RunnerName: runner, Scope: scope}, true
}

// ParseWindows reads lines of name|state|startmode. State is Running or
// Stopped; start mode is Auto, Manual or Disabled.
func ParseWindows(out string) []doctor.OSService {
	var res []doctor.OSService
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(strings.TrimSpace(line), "|")
		if len(parts) < 1 {
			continue
		}
		s, ok := make1(strings.TrimSpace(parts[0]))
		if !ok {
			continue
		}
		if len(parts) > 1 {
			s.Running = strings.EqualFold(strings.TrimSpace(parts[1]), "running")
		}
		if len(parts) > 2 {
			mode := strings.TrimSpace(parts[2])
			s.AutomaticKnown = mode != ""
			s.Automatic = strings.EqualFold(mode, "auto")
		}
		res = append(res, s)
	}
	return res
}

// ParseSystemd reads `systemctl list-units --plain --no-legend` (unit load
// active sub description) and `list-unit-files` (unit state), which may be empty.
func ParseSystemd(units, files string) []doctor.OSService {
	enabled := map[string]string{}
	for _, line := range strings.Split(files, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 {
			enabled[strings.TrimSuffix(f[0], ".service")] = f[1]
		}
	}
	var res []doctor.OSService
	for _, line := range strings.Split(units, "\n") {
		f := strings.Fields(strings.TrimLeft(line, "●* "))
		if len(f) < 1 {
			continue
		}
		s, ok := make1(f[0])
		if !ok {
			continue
		}
		if len(f) >= 4 {
			s.Running = f[2] == "active" && f[3] == "running"
		}
		if st, ok := enabled[s.Name]; ok {
			s.AutomaticKnown = true
			s.Automatic = st == "enabled" || st == "enabled-runtime"
		}
		res = append(res, s)
	}
	return res
}

// ParseLaunchd reads `launchctl list` (PID, last exit status, label). A job
// with a PID runs. launchd does not say whether a job starts at login, so
// AutomaticKnown stays false.
func ParseLaunchd(out string) []doctor.OSService {
	var res []doctor.OSService
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		label := f[len(f)-1]
		// A runner installed by svc.sh is labelled actions.runner.<scope>.<name>;
		// a user agent may carry a reverse-DNS prefix before it.
		if i := strings.Index(label, Prefix); i > 0 {
			label = label[i:]
		}
		s, ok := make1(label)
		if !ok {
			continue
		}
		s.Running = f[0] != "-" && f[0] != "0"
		res = append(res, s)
	}
	return res
}
