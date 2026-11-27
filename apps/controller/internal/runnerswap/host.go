package runnerswap

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// ServiceHost stops and starts a runner's service with the tool of the OS:
// systemctl, PowerShell or launchctl (runners R5.21, R5.22, R5.23). Nothing
// goes through a shell; the service name is one argument.
type ServiceHost struct {
	// OS is linux, windows or macos; empty takes this machine's.
	OS string
	// Run is a seam for tests; nil runs the program.
	Run func(ctx context.Context, name string, args ...string) (string, error)
}

var _ Host = ServiceHost{}

func (h ServiceHost) osName() string {
	if h.OS != "" {
		return h.OS
	}
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "darwin":
		return "macos"
	}
	return "linux"
}

// command is the program and arguments of one action (stop or start).
func (h ServiceHost) command(action, service string) (string, []string) {
	switch h.osName() {
	case "windows":
		verb := "Stop-Service"
		if action == "start" {
			verb = "Start-Service"
		}
		return "powershell.exe", []string{"-NoProfile", "-NonInteractive", "-Command", verb + " -Name $args[0] -ErrorAction Stop", service}
	case "macos":
		return "launchctl", []string{action, service}
	}
	return "systemctl", []string{action, service}
}

func (h ServiceHost) do(ctx context.Context, action, service string) error {
	if strings.TrimSpace(service) == "" || strings.HasPrefix(service, "-") {
		return fmt.Errorf("%q is not a service name", service)
	}
	name, args := h.command(action, service)
	var out string
	var err error
	if h.Run != nil {
		out, err = h.Run(ctx, name, args...)
	} else {
		var b []byte
		b, err = exec.CommandContext(ctx, name, args...).CombinedOutput() //nolint:gosec // fixed programs; the service name is one checked argument
		out = string(b)
	}
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, action, err, firstLine(out))
	}
	return nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// Stop stops the service.
func (h ServiceHost) Stop(ctx context.Context, service string) error {
	return h.do(ctx, "stop", service)
}

// Start starts the service.
func (h ServiceHost) Start(ctx context.Context, service string) error {
	return h.do(ctx, "start", service)
}
