package health

import (
	"regexp"
	"strings"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

// The macOS collectors.

var secRe = regexp.MustCompile(`sec\s*=\s*(\d+)`)

func darwinBoot(env Env) *agentv1.BootHealth {
	out, ok := run(env, "sysctl", "-n", "kern.boottime")
	if !ok {
		return &agentv1.BootHealth{UnknownReason: "sysctl kern.boottime failed"}
	}
	m := secRe.FindStringSubmatch(out)
	if m == nil {
		return &agentv1.BootHealth{UnknownReason: "sysctl kern.boottime gave no time"}
	}
	sec, _ := atoi64(m[1])
	return bootFromUptime(env.Now(), env.Now().Sub(time.Unix(sec, 0)))
}

var swapRe = regexp.MustCompile(`(total|used)\s*=\s*([\d.]+)([MGK])`)

func darwinSwap(env Env) *agentv1.SwapHealth {
	out, ok := run(env, "sysctl", "-n", "vm.swapusage")
	if !ok {
		return &agentv1.SwapHealth{UnknownReason: "sysctl vm.swapusage failed"}
	}
	h := &agentv1.SwapHealth{}
	n := 0
	for _, m := range swapRe.FindAllStringSubmatch(out, -1) {
		f, _ := atof(m[2])
		mul := map[string]float64{"K": 1 << 10, "M": 1 << 20, "G": 1 << 30}[m[3]]
		if m[1] == "total" {
			h.TotalBytes = int64(f * mul)
		} else {
			h.UsedBytes = int64(f * mul)
		}
		n++
	}
	if n < 2 {
		return &agentv1.SwapHealth{UnknownReason: "sysctl vm.swapusage has no total and used"}
	}
	h.Known = true
	return h
}

func darwinLoad(env Env) *agentv1.LoadAverageHealth {
	h := &agentv1.LoadAverageHealth{StealUnknownReason: "macOS has no CPU steal figure"}
	out, ok := run(env, "sysctl", "-n", "vm.loadavg")
	f := strings.Fields(strings.Trim(strings.TrimSpace(out), "{}"))
	if !ok || len(f) < 3 {
		h.LoadUnknownReason = "sysctl vm.loadavg failed"
		return h
	}
	a, ok1 := atof(f[0])
	b, ok2 := atof(f[1])
	c, ok3 := atof(f[2])
	if !ok1 || !ok2 || !ok3 {
		h.LoadUnknownReason = "sysctl vm.loadavg has no numbers"
		return h
	}
	h.LoadKnown, h.Load1, h.Load5, h.Load15 = true, a, b, c
	return h
}

// darwinUpdates reads `softwareupdate --list`. It is slow, so the monitor calls
// it at most hourly. macOS has no restart-pending flag: an update that needs a
// restart is listed as the reason.
func darwinUpdates(env Env, timeout time.Duration) *agentv1.UpdateHealth {
	r, ok := env.RunTimeout([]string{"softwareupdate", "--list"}, timeout)
	if !ok || r.Code != 0 {
		return &agentv1.UpdateHealth{UnknownReason: "softwareupdate --list did not finish in " + timeout.String()}
	}
	u := &agentv1.UpdateHealth{Known: true, UpdateCountKnown: true}
	lines := strings.Split(r.Out, "\n")
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "* ") {
			continue
		}
		u.PendingUpdateCount++
		name := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(t, "* "), "Label:"))
		detail := ""
		if i+1 < len(lines) {
			detail = lines[i+1]
		}
		if strings.Contains(strings.ToLower(t+detail), "restart") {
			u.RestartReasons = append(u.RestartReasons, name+" needs a restart to install")
		}
	}
	def, dok := run(env, "defaults", "read", "/Library/Preferences/com.apple.SoftwareUpdate", "AutomaticallyInstallMacOSUpdates")
	switch {
	case dok && strings.TrimSpace(def) == "1":
		u.AutoUpdateState, u.AutoRestartPossible, u.RestartAllowedNow = "automatic macOS updates on", true, true
	case dok:
		u.AutoUpdateState = "automatic macOS updates off"
	default:
		u.AutoUpdateState = "automatic macOS updates not set"
	}
	return u
}

var pmsetRe = regexp.MustCompile(`^\s*(\w+)\s+(\d+)`)

func darwinPower(env Env) *agentv1.PowerHealth {
	p := &agentv1.PowerHealth{}
	if out, ok := run(env, "pmset", "-g", "batt"); ok {
		p.SourceKnown = true
		p.OnBattery = strings.Contains(out, "Battery Power")
	} else {
		p.SourceUnknownReason = "pmset -g batt failed"
	}
	if out, ok := run(env, "pmset", "-g"); ok {
		p.SleepKnown = true
		for _, l := range strings.Split(out, "\n") {
			m := pmsetRe.FindStringSubmatch(l)
			if m == nil || m[2] == "0" {
				continue
			}
			switch m[1] {
			case "sleep":
				p.SleepEnabled = true
				p.SleepModes = append(p.SleepModes, "sleep after "+m[2]+" min")
			case "hibernatemode":
				// hibernatemode only says how a sleep is kept; the sleep line decides.
			}
		}
	} else {
		p.SleepUnknownReason = "pmset -g failed"
	}
	if out, ok := run(env, "sysctl", "-n", "kern.waketime"); ok {
		if m := secRe.FindStringSubmatch(out); m != nil {
			sec, _ := atoi64(m[1])
			p.WakeKnown, p.LastWakeUnixMs = true, sec*1000
		}
	}
	return p
}
