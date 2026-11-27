//go:build darwin

package tray

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var lastExit = regexp.MustCompile(`last exit code = (-?\d+)`)

// ServiceState reads the stop state from launchd (R5.12).
func ServiceState(ctx context.Context) (Status, bool) {
	out, err := exec.CommandContext(ctx, "launchctl", "print", "system/"+ServiceName).Output()
	if err != nil {
		return Status{}, false
	}
	return parseLaunchctl(string(out))
}

func parseLaunchctl(out string) (Status, bool) {
	if strings.Contains(out, "state = running") {
		return Status{}, false
	}
	code := 0
	if m := lastExit.FindStringSubmatch(out); m != nil {
		code, _ = strconv.Atoi(m[1])
	}
	return Status{State: StateStopped, ExitCode: code}, true
}
