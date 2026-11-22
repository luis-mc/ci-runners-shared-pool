package health

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
)

// The Linux collectors. They read /proc, /sys and systemd files through Env.

func linuxBoot(env Env) *agentv1.BootHealth {
	txt, err := env.ReadFile("/proc/uptime")
	if err != nil {
		return &agentv1.BootHealth{UnknownReason: "/proc/uptime cannot be read"}
	}
	f := strings.Fields(txt)
	up, ok := atof(firstOr(f, ""))
	if !ok {
		return &agentv1.BootHealth{UnknownReason: "/proc/uptime has no number"}
	}
	return bootFromUptime(env.Now(), time.Duration(up*float64(time.Second)))
}

func firstOr(f []string, d string) string {
	if len(f) == 0 {
		return d
	}
	return f[0]
}

func bootFromUptime(now time.Time, up time.Duration) *agentv1.BootHealth {
	return &agentv1.BootHealth{Known: true, UptimeSeconds: int64(up / time.Second), LastBootUnixMs: now.Add(-up).UnixMilli()}
}

func linuxSwap(env Env) *agentv1.SwapHealth {
	txt, err := env.ReadFile("/proc/meminfo")
	if err != nil {
		return &agentv1.SwapHealth{UnknownReason: "/proc/meminfo cannot be read"}
	}
	m := kv(txt, ":")
	total, ok1 := kib(m["SwapTotal"])
	free, ok2 := kib(m["SwapFree"])
	if !ok1 || !ok2 {
		return &agentv1.SwapHealth{UnknownReason: "/proc/meminfo has no swap lines"}
	}
	return &agentv1.SwapHealth{Known: true, TotalBytes: total, UsedBytes: total - free}
}

// OOMTarget is one cgroup that is read for oom_kill: scope is pool or job.
type OOMTarget struct {
	Scope string
	Name  string
	// Events is the path of the memory.events file.
	Events string
}

// oomCounts reads oom_kill of each target. Targets that cannot be read are skipped;
// ok is false when none can be read (no cgroup v2).
func linuxOOMCounts(env Env, targets []OOMTarget) (map[OOMTarget]int64, bool) {
	out := map[OOMTarget]int64{}
	for _, t := range targets {
		txt, err := env.ReadFile(t.Events)
		if err != nil {
			continue
		}
		if n, ok := atoi64(kv(txt, " ")["oom_kill"]); ok {
			out[t] = n
		}
	}
	return out, len(out) > 0
}

// DiscoverOOMTargets finds the crsp pool cgroup and its job cgroups: the files
// /sys/fs/cgroup/crsp*/memory.events (pool) and /sys/fs/cgroup/crsp*/*/memory.events (job).
func DiscoverOOMTargets(env Env) []OOMTarget {
	var out []OOMTarget
	for _, p := range env.Glob("/sys/fs/cgroup/crsp*/memory.events") {
		out = append(out, OOMTarget{Scope: "pool", Name: path.Base(path.Dir(p)), Events: p})
	}
	for _, p := range env.Glob("/sys/fs/cgroup/crsp*/*/memory.events") {
		out = append(out, OOMTarget{Scope: "job", Name: path.Base(path.Dir(p)), Events: p})
	}
	return out
}

// linuxThermal reads thermal zones (millidegrees) and the per-core throttle
// counters; throttled when a counter rose since prev, or a zone is at or over
// hotCelsius. cur is the counter sum to hand to the next call.
func linuxThermal(env Env, prev int64, havePrev bool) (h *agentv1.ThermalHealth, cur int64) {
	zones := env.Glob("/sys/class/thermal/thermal_zone*/temp")
	counters := env.Glob("/sys/devices/system/cpu/cpu*/thermal_throttle/core_throttle_count")
	freqs := env.Glob("/sys/devices/system/cpu/cpu*/cpufreq/scaling_cur_freq")
	if len(zones) == 0 && len(counters) == 0 && len(freqs) == 0 {
		return &agentv1.ThermalHealth{UnknownReason: "this machine has no thermal zones or cpufreq files (a VM or container)"}, 0
	}
	h = &agentv1.ThermalHealth{Known: true}
	for _, z := range zones {
		txt, err := env.ReadFile(z)
		if err != nil {
			continue
		}
		if n, ok := atoi64(txt); ok && float64(n)/1000 > h.MaxTempCelsius {
			h.MaxTempCelsius = float64(n) / 1000
		}
	}
	for _, c := range counters {
		txt, err := env.ReadFile(c)
		if err != nil {
			continue
		}
		if n, ok := atoi64(txt); ok {
			cur += n
		}
	}
	switch {
	case havePrev && cur > prev:
		h.Throttled, h.ThrottleDetail = true, fmt.Sprintf("the CPU was throttled %d times since the last read", cur-prev)
	case h.MaxTempCelsius >= HotCelsius:
		h.Throttled, h.ThrottleDetail = true, fmt.Sprintf("a thermal zone reads %.0f °C", h.MaxTempCelsius)
	}
	return h, cur
}

// HotCelsius is the zone temperature counted as throttling.
const HotCelsius = 95.0

func linuxLoad(env Env, steal *agentv1.LoadAverageHealth, stealPct float64, stealOK bool, stealReason string) *agentv1.LoadAverageHealth {
	h := steal
	txt, err := env.ReadFile("/proc/loadavg")
	if err != nil {
		h.LoadUnknownReason = "/proc/loadavg cannot be read"
	} else if f := strings.Fields(txt); len(f) >= 3 {
		a, ok1 := atof(f[0])
		b, ok2 := atof(f[1])
		c, ok3 := atof(f[2])
		if ok1 && ok2 && ok3 {
			h.LoadKnown, h.Load1, h.Load5, h.Load15 = true, a, b, c
		} else {
			h.LoadUnknownReason = "/proc/loadavg has no numbers"
		}
	}
	h.StealKnown, h.StealPct, h.StealUnknownReason = stealOK, stealPct, stealReason
	return h
}

// cpuTimes is the first line of /proc/stat: the steal and total jiffies.
type cpuTimes struct{ steal, total int64 }

func linuxCPUTimes(env Env) (cpuTimes, bool) {
	txt, err := env.ReadFile("/proc/stat")
	if err != nil {
		return cpuTimes{}, false
	}
	line, _, _ := strings.Cut(txt, "\n")
	f := strings.Fields(line)
	if len(f) < 9 || f[0] != "cpu" {
		return cpuTimes{}, false
	}
	var total int64
	for _, s := range f[1:9] { // user nice system idle iowait irq softirq steal
		n, ok := atoi64(s)
		if !ok {
			return cpuTimes{}, false
		}
		total += n
	}
	st, _ := strconv.ParseInt(f[8], 10, 64)
	return cpuTimes{steal: st, total: total}, true
}

func linuxUpdates(env Env) *agentv1.UpdateHealth {
	u := &agentv1.UpdateHealth{Known: true}
	switch {
	case env.Exists("/var/run/reboot-required") || env.Exists("/run/reboot-required"):
		u.RestartPending = true
		pk, _ := env.ReadFile("/var/run/reboot-required.pkgs")
		for _, l := range strings.Fields(pk) {
			u.RestartReasons = append(u.RestartReasons, "restart needed for "+l)
		}
		if len(u.RestartReasons) == 0 {
			u.RestartReasons = []string{"/var/run/reboot-required exists"}
		}
	case env.Exists("/etc/debian_version"):
		// Debian and Ubuntu write the file only when a restart is needed.
	default:
		r, ok := env.Run([]string{"needs-restarting", "-r"})
		switch {
		case !ok:
			u.Known, u.UnknownReason = false, "needs-restarting is not installed (package yum-utils or dnf-utils)"
		case r.Code == 1:
			u.RestartPending, u.RestartReasons = true, []string{"needs-restarting -r says a restart is needed"}
		}
	}
	auto, _ := env.ReadFile("/etc/apt/apt.conf.d/20auto-upgrades")
	reboot, _ := env.ReadFile("/etc/apt/apt.conf.d/50unattended-upgrades")
	on := aptFlag(auto, "APT::Periodic::Unattended-Upgrade")
	rb := aptFlag(reboot, "Unattended-Upgrade::Automatic-Reboot")
	switch {
	case on && rb:
		u.AutoUpdateState, u.RestartAllowedNow, u.AutoRestartPossible = "unattended-upgrades on, automatic restart on", true, true
	case on:
		u.AutoUpdateState = "unattended-upgrades on, automatic restart off"
	case auto == "" && reboot == "":
		u.AutoUpdateState = "unattended-upgrades not set up"
	default:
		u.AutoUpdateState = "unattended-upgrades off"
	}
	return u
}

// aptFlag reads `Key "1";` or `Key "true";` from an apt.conf file; a commented line does not count.
func aptFlag(txt, key string) bool {
	for _, l := range strings.Split(txt, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "//") || !strings.HasPrefix(l, key) {
			continue
		}
		v := strings.Trim(strings.TrimSpace(strings.TrimPrefix(l, key)), `";`)
		return v == "1" || v == "true"
	}
	return false
}

func linuxPower(env Env) *agentv1.PowerHealth {
	p := &agentv1.PowerHealth{}
	supplies := env.Glob("/sys/class/power_supply/*/type")
	battery, mainsOnline, mainsSeen, discharging := false, false, false, false
	for _, f := range supplies {
		typ, _ := env.ReadFile(f)
		dir := path.Dir(f)
		switch strings.TrimSpace(typ) {
		case "Battery":
			battery = true
			st, _ := env.ReadFile(dir + "/status")
			discharging = discharging || strings.TrimSpace(st) == "Discharging"
		case "Mains":
			mainsSeen = true
			on, _ := env.ReadFile(dir + "/online")
			mainsOnline = mainsOnline || strings.TrimSpace(on) == "1"
		}
	}
	p.SourceKnown = true
	p.OnBattery = battery && (discharging || (mainsSeen && !mainsOnline))
	// Sleep: a systemd sleep target that is not masked can suspend the machine
	// when logind idles out or, on a laptop, the lid closes.
	res, ok := env.Run([]string{"systemctl", "is-enabled", "suspend.target", "hibernate.target", "hybrid-sleep.target", "suspend-then-hibernate.target"})
	if out := res.Out; !ok || out == "" {
		p.SleepUnknownReason = "systemctl is not available (this machine does not run systemd)"
	} else {
		p.SleepKnown = true
		names := []string{"suspend", "hibernate", "hybrid-sleep", "suspend-then-hibernate"}
		for i, l := range strings.Fields(res.Out) {
			if i < len(names) && l != "masked" {
				p.SleepModes = append(p.SleepModes, names[i])
			}
		}
		conf, _ := env.ReadFile("/etc/systemd/logind.conf")
		idle := logindValue(conf, "IdleAction")
		idleSleeps := strings.Contains(idle, "suspend") || strings.Contains(idle, "hibernate") || strings.Contains(idle, "hybrid")
		lid := logindValue(conf, "HandleLidSwitch")
		lidSleeps := battery && lid != "ignore" && lid != "lock" && lid != "poweroff"
		p.SleepEnabled = len(p.SleepModes) > 0 && (idleSleeps || lidSleeps)
		if p.SleepEnabled && idleSleeps {
			p.SleepModes = append(p.SleepModes, "logind IdleAction="+idle)
		}
	}
	// The last wake is not known: Linux keeps no wake time that can be read without the journal.
	return p
}

func logindValue(conf, key string) string {
	v := ""
	for _, l := range strings.Split(conf, "\n") {
		l = strings.TrimSpace(l)
		if k, val, ok := strings.Cut(l, "="); ok && strings.TrimSpace(k) == key {
			v = strings.TrimSpace(val)
		}
	}
	return v
}
