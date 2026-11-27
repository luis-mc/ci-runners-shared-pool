package health

import (
	"testing"
	"time"
)

func TestLinuxBootSwapLoad(t *testing.T) {
	f := newFake("linux")
	f.files["/proc/uptime"] = "3600.50 7000.00\n"
	f.files["/proc/meminfo"] = "SwapTotal:  2048 kB\nSwapFree:  1024 kB\n"
	f.files["/proc/loadavg"] = "0.50 1.25 2.00 1/200 999\n"
	b := linuxBoot(f)
	if !b.Known || b.UptimeSeconds != 3600 || b.LastBootUnixMs != f.now.Add(-3600500*time.Millisecond).UnixMilli() {
		t.Fatalf("boot %+v", b)
	}
	if s := linuxSwap(f); !s.Known || s.TotalBytes != 2048*1024 || s.UsedBytes != 1024*1024 {
		t.Fatalf("swap %+v", s)
	}
	l := linuxLoad(f, nil2(), 1.5, true, "")
	if !l.LoadKnown || l.Load5 != 1.25 || !l.StealKnown || l.StealPct != 1.5 {
		t.Fatalf("load %+v", l)
	}
}

func TestLinuxUnknownIsNotZero(t *testing.T) {
	f := newFake("linux")
	if b := linuxBoot(f); b.Known || b.UnknownReason == "" || b.UptimeSeconds != 0 {
		t.Fatalf("boot %+v", b)
	}
	if s := linuxSwap(f); s.Known || s.UnknownReason == "" {
		t.Fatalf("swap %+v", s)
	}
	if h, _ := linuxThermal(f, 0, false); h.Known || h.UnknownReason == "" {
		t.Fatalf("thermal %+v", h)
	}
	if l := linuxLoad(f, nil2(), 0, false, "no sample"); l.LoadKnown || l.LoadUnknownReason == "" || l.StealKnown || l.StealUnknownReason != "no sample" {
		t.Fatalf("load %+v", l)
	}
}

func TestLinuxOOMTargets(t *testing.T) {
	f := newFake("linux")
	f.globs["/sys/fs/cgroup/crsp*/memory.events"] = []string{"/sys/fs/cgroup/crsp.slice/memory.events"}
	f.globs["/sys/fs/cgroup/crsp*/*/memory.events"] = []string{"/sys/fs/cgroup/crsp.slice/job1/memory.events"}
	f.files["/sys/fs/cgroup/crsp.slice/memory.events"] = "low 0\nhigh 0\nmax 0\noom 2\noom_kill 2\n"
	f.files["/sys/fs/cgroup/crsp.slice/job1/memory.events"] = "oom_kill 1\n"
	ts := DiscoverOOMTargets(f)
	if len(ts) != 2 || ts[0].Scope != "pool" || ts[1].Scope != "job" || ts[1].Name != "job1" {
		t.Fatalf("targets %+v", ts)
	}
	c, ok := linuxOOMCounts(f, ts)
	if !ok || c[ts[0]] != 2 || c[ts[1]] != 1 {
		t.Fatalf("counts %v", c)
	}
	if _, ok := linuxOOMCounts(f, []OOMTarget{{Events: "/none"}}); ok {
		t.Fatal("a missing file must be not known")
	}
}

func TestLinuxThermalThrottle(t *testing.T) {
	f := newFake("linux")
	f.globs["/sys/class/thermal/thermal_zone*/temp"] = []string{"/z0", "/z1"}
	f.globs["/sys/devices/system/cpu/cpu*/thermal_throttle/core_throttle_count"] = []string{"/c0"}
	f.files["/z0"], f.files["/z1"], f.files["/c0"] = "55000", "71500", "4"
	h, cur := linuxThermal(f, 0, false)
	if !h.Known || h.MaxTempCelsius != 71.5 || h.Throttled || cur != 4 {
		t.Fatalf("first %+v", h)
	}
	f.files["/c0"] = "9"
	if h, _ = linuxThermal(f, cur, true); !h.Throttled || h.ThrottleDetail == "" {
		t.Fatalf("counter rise %+v", h)
	}
	f.files["/z1"] = "97000"
	if h, _ = linuxThermal(f, 9, true); !h.Throttled {
		t.Fatalf("hot %+v", h)
	}
}

func TestLinuxCPUTimes(t *testing.T) {
	f := newFake("linux")
	f.files["/proc/stat"] = "cpu  100 0 50 800 10 0 5 35 0 0\ncpu0 1 2 3\n"
	c, ok := linuxCPUTimes(f)
	if !ok || c.steal != 35 || c.total != 1000 {
		t.Fatalf("%+v %v", c, ok)
	}
	f.files["/proc/stat"] = "intr 1\n"
	if _, ok := linuxCPUTimes(f); ok {
		t.Fatal("bad stat")
	}
}

func TestLinuxUpdatesDebian(t *testing.T) {
	f := newFake("linux")
	f.files["/etc/debian_version"] = "12"
	u := linuxUpdates(f)
	if !u.Known || u.RestartPending || u.AutoUpdateState != "unattended-upgrades not set up" {
		t.Fatalf("%+v", u)
	}
	f.files["/var/run/reboot-required"] = ""
	f.files["/var/run/reboot-required.pkgs"] = "linux-image-6.1\n"
	f.files["/etc/apt/apt.conf.d/20auto-upgrades"] = "// APT::Periodic::Unattended-Upgrade \"0\";\nAPT::Periodic::Unattended-Upgrade \"1\";\n"
	f.files["/etc/apt/apt.conf.d/50unattended-upgrades"] = "Unattended-Upgrade::Automatic-Reboot \"true\";\n"
	u = linuxUpdates(f)
	if !u.RestartPending || len(u.RestartReasons) != 1 || !u.RestartAllowedNow || !u.AutoRestartPossible {
		t.Fatalf("%+v", u)
	}
}

func TestLinuxUpdatesRHEL(t *testing.T) {
	f := newFake("linux")
	if u := linuxUpdates(f); u.Known || u.UnknownReason == "" {
		t.Fatalf("no tool: %+v", u)
	}
	f.runs["needs-restarting -r"] = Result{Code: 1}
	if u := linuxUpdates(f); !u.Known || !u.RestartPending {
		t.Fatalf("needed: %+v", u)
	}
	f.runs["needs-restarting -r"] = Result{Code: 0}
	if u := linuxUpdates(f); !u.Known || u.RestartPending {
		t.Fatalf("not needed: %+v", u)
	}
}

func TestLinuxPower(t *testing.T) {
	f := newFake("linux")
	f.globs["/sys/class/power_supply/*/type"] = []string{"/sys/class/power_supply/BAT0/type", "/sys/class/power_supply/AC/type"}
	f.files["/sys/class/power_supply/BAT0/type"] = "Battery\n"
	f.files["/sys/class/power_supply/BAT0/status"] = "Discharging\n"
	f.files["/sys/class/power_supply/AC/type"] = "Mains\n"
	f.files["/sys/class/power_supply/AC/online"] = "0\n"
	f.runs["systemctl is-enabled suspend.target hibernate.target hybrid-sleep.target suspend-then-hibernate.target"] = Result{Code: 1, Out: "static\nmasked\nmasked\nmasked\n"}
	p := linuxPower(f)
	if !p.OnBattery || !p.SleepKnown || !p.SleepEnabled || len(p.SleepModes) != 1 || p.WakeKnown {
		t.Fatalf("laptop %+v", p)
	}
	// A server: no battery, sleep targets masked.
	g := newFake("linux")
	g.runs["systemctl is-enabled suspend.target hibernate.target hybrid-sleep.target suspend-then-hibernate.target"] = Result{Code: 1, Out: "masked\nmasked\nmasked\nmasked\n"}
	if p := linuxPower(g); p.OnBattery || p.SleepEnabled || !p.SleepKnown {
		t.Fatalf("server %+v", p)
	}
	// No systemd.
	h := newFake("linux")
	if p := linuxPower(h); p.SleepKnown || p.SleepUnknownReason == "" {
		t.Fatalf("no systemd %+v", p)
	}
}
