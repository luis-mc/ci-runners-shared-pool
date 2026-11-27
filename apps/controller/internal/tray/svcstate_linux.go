//go:build linux

package tray

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ServiceState reads the stop state from systemd (R5.12). No admin needed.
func ServiceState(ctx context.Context) (Status, bool) {
	out, err := exec.CommandContext(ctx, "systemctl", "show", ServiceName,
		"-p", "ActiveState,ExecMainStatus,ExecMainExitTimestamp").Output()
	if err != nil {
		return Status{}, false
	}
	return parseSystemctl(string(out))
}

func parseSystemctl(out string) (Status, bool) {
	kv := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(l), "="); ok {
			kv[k] = v
		}
	}
	if kv["ActiveState"] == "" || kv["ActiveState"] == "active" {
		return Status{}, false
	}
	code, _ := strconv.Atoi(kv["ExecMainStatus"])
	st := Status{State: StateStopped, ExitCode: code}
	if ts := kv["ExecMainExitTimestamp"]; ts != "" {
		// "Sat 2026-11-14 14:30:00 UTC"
		f := strings.Fields(ts)
		if len(f) >= 3 {
			if t, err := time.Parse("2006-01-02 15:04:05", f[1]+" "+f[2]); err == nil {
				st.StoppedAt = t
			}
		}
	}
	return st, true
}
