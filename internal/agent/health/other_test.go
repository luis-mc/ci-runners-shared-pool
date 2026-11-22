package health

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWindowsBootSwapOOM(t *testing.T) {
	f := newFake("windows")
	if b := windowsBoot(f); b.Known || b.UnknownReason == "" {
		t.Fatalf("no tick: %+v", b)
	}
	f.tick, f.tickErr = 90*time.Minute, nil
	if b := windowsBoot(f); !b.Known || b.UptimeSeconds != 5400 {
		t.Fatalf("boot %+v", b)
	}
	f.runs[psKey(swapScript)] = Result{Out: `{"n":1,"total":4096,"used":1024}` + "\n"}
	if s := windowsSwap(f); !s.Known || s.TotalBytes != 4096<<20 || s.UsedBytes != 1024<<20 {
		t.Fatalf("swap %+v", s)
	}
	f.runs[psKey(swapScript)] = Result{Out: `{"n":0,"total":null,"used":null}`}
	if s := windowsSwap(f); s.Known || s.UnknownReason == "" {
		t.Fatalf("no page file %+v", s)
	}
	f.runs[psKey(oomScript)] = Result{Out: `{"n":2,"last":1700000000000}`}
	if o := windowsOOM(f); !o.Known || o.TotalKills != 2 || o.LastKillUnixMs != 1700000000000 {
		t.Fatalf("oom %+v", o)
	}
	if o := windowsOOM(newFake("windows")); o.Known {
		t.Fatalf("oom %+v", o)
	}
}

func TestParseWindowsUpdates(t *testing.T) {
	noon := time.Date(2026, 11, 22, 12, 0, 0, 0, time.Local)
	u := parseWindowsUpdates(`{"rr":true,"cbs":false,"pfro":true,"count":3,"start":8,"end":17,"noauto":null}`, noon)
	if !u.RestartPending || len(u.RestartReasons) != 2 || u.PendingUpdateCount != 3 || !u.ActiveHoursKnown {
		t.Fatalf("%+v", u)
	}
	if u.RestartAllowedNow {
		t.Fatal("noon is inside the active hours 8 to 17")
	}
	night := time.Date(2026, 11, 22, 3, 0, 0, 0, time.Local)
	if u = parseWindowsUpdates(`{"rr":true,"cbs":false,"pfro":false,"count":null,"start":8,"end":17,"noauto":null}`, night); !u.RestartAllowedNow || u.UpdateCountKnown {
		t.Fatalf("night %+v", u)
	}
	if u = parseWindowsUpdates(`{"rr":false,"cbs":false,"pfro":false,"count":0,"start":null,"end":null,"noauto":1}`, night); u.RestartPending || u.AutoRestartPossible || u.RestartAllowedNow || !u.UpdateCountKnown {
		t.Fatalf("policy %+v", u)
	}
	if u = parseWindowsUpdates("garbage", night); u.Known || u.UnknownReason == "" {
		t.Fatalf("garbage %+v", u)
	}
}

func TestHourInRange(t *testing.T) {
	for _, c := range []struct {
		h, s, e int32
		want    bool
	}{{12, 8, 17, true}, {17, 8, 17, false}, {23, 22, 6, true}, {3, 22, 6, true}, {12, 22, 6, false}} {
		if got := hourInRange(c.h, c.s, c.e); got != c.want {
			t.Errorf("%d in [%d,%d) = %v", c.h, c.s, c.e, got)
		}
	}
}

const powercfgOut = `Power Scheme GUID: 381b4222 (Balanced)
  Subgroup GUID: 238c9fa8 (Sleep)
    Power Setting GUID: 29f6c1db-86da-48c5-9fdb-f2b67b1f44da  (Sleep after)
      Current AC Power Setting Index: 0x00000000
      Current DC Power Setting Index: 0x00000384
    Power Setting GUID: 9d7815a6-7ee4-497e-8888-515a05f02364  (Hibernate after)
      Current AC Power Setting Index: 0x00000000
      Current DC Power Setting Index: 0x00000000
`

func TestWindowsPower(t *testing.T) {
	f := newFake("windows")
	f.runs[psKey(powerScript)] = Result{Out: `{"battery":0,"status":0,"wake":1700000000000}`}
	f.runs["powercfg /q SCHEME_CURRENT SUB_SLEEP"] = Result{Out: powercfgOut}
	p := windowsPower(f)
	if !p.SourceKnown || p.OnBattery || p.SleepEnabled || !p.SleepKnown || !p.WakeKnown {
		t.Fatalf("AC never sleeps: %+v", p)
	}
	f.runs[psKey(powerScript)] = Result{Out: `{"battery":1,"status":1,"wake":0}`}
	p = windowsPower(f)
	if !p.OnBattery || !p.SleepEnabled || p.SleepModes[0] != "sleep after 15 min" || p.WakeKnown {
		t.Fatalf("battery sleeps after 15 min: %+v", p)
	}
	if p := windowsPower(newFake("windows")); p.SourceKnown || p.SleepKnown || p.SleepUnknownReason == "" {
		t.Fatalf("nothing answers: %+v", p)
	}
}

func TestWindowsWSLVersion(t *testing.T) {
	f := newFake("windows")
	f.runs["wsl --version"] = Result{Out: "W\x00S\x00L\x00 \x00v\x00e\x00r\x00s\x00i\x00o\x00n\x00:\x00 2.1.5.0\x00\n\x00"}
	if v := windowsWSLVersion(f); v != "2.1.5.0" {
		t.Fatalf("%q", v)
	}
}

func TestDarwin(t *testing.T) {
	f := newFake("darwin")
	f.runs["sysctl -n kern.boottime"] = Result{Out: "{ sec = " + itoa(int(f.now.Add(-2*time.Hour).Unix())) + ", usec = 5 } Sun Nov 22 10:00:00 2026\n"}
	f.runs["sysctl -n vm.swapusage"] = Result{Out: "total = 2048.00M  used = 512.50M  free = 1535.50M  (encrypted)\n"}
	f.runs["sysctl -n vm.loadavg"] = Result{Out: "{ 1.20 2.30 3.40 }\n"}
	if b := darwinBoot(f); !b.Known || b.UptimeSeconds != 7200 {
		t.Fatalf("boot %+v", b)
	}
	if s := darwinSwap(f); !s.Known || s.TotalBytes != 2048<<20 || s.UsedBytes != int64(512.5*float64(1<<20)) {
		t.Fatalf("swap %+v", s)
	}
	if l := darwinLoad(f); !l.LoadKnown || l.Load15 != 3.4 || l.StealKnown || l.StealUnknownReason == "" {
		t.Fatalf("load %+v", l)
	}
	g := newFake("darwin")
	if b := darwinBoot(g); b.Known {
		t.Fatal("boot")
	}
	if s := darwinSwap(g); s.Known {
		t.Fatal("swap")
	}
	if l := darwinLoad(g); l.LoadKnown || l.LoadUnknownReason == "" {
		t.Fatal("load")
	}
}

func TestDarwinUpdatesAndPower(t *testing.T) {
	f := newFake("darwin")
	f.runs["softwareupdate --list"] = Result{Out: "Software Update Tool\n\nFinding available software\nSoftware Update found the following new or updated software:\n* Label: macOS Sonoma 14.2-23C64\n\tTitle: macOS Sonoma 14.2, Version: 14.2, Size: 1GiB, Recommended: YES, Action: restart,\n* Label: Safari17.2\n\tTitle: Safari, Version: 17.2, Size: 100KiB, Recommended: YES,\n"}
	f.runs["defaults read /Library/Preferences/com.apple.SoftwareUpdate AutomaticallyInstallMacOSUpdates"] = Result{Out: "1\n"}
	u := darwinUpdates(f, time.Second)
	if !u.Known || u.PendingUpdateCount != 2 || len(u.RestartReasons) != 1 || !u.AutoRestartPossible {
		t.Fatalf("%+v", u)
	}
	if u := darwinUpdates(newFake("darwin"), time.Second); u.Known || u.UnknownReason == "" {
		t.Fatalf("timeout %+v", u)
	}
	f.runs["pmset -g batt"] = Result{Out: "Now drawing from 'Battery Power'\n -InternalBattery-0 (id=1)\t80%; discharging\n"}
	f.runs["pmset -g"] = Result{Out: "System-wide power settings:\nCurrently in use:\n standby 1\n sleep 10\n hibernatemode 3\n displaysleep 5\n"}
	f.runs["sysctl -n kern.waketime"] = Result{Out: "{ sec = 1700000000, usec = 1 }\n"}
	p := darwinPower(f)
	if !p.OnBattery || !p.SleepEnabled || !p.WakeKnown || p.LastWakeUnixMs != 1700000000000 {
		t.Fatalf("%+v", p)
	}
}

func TestParseDockerDF(t *testing.T) {
	h := parseDockerDF("Images|1.5GB\nContainers|20MB\nLocal Volumes|500kB\nBuild Cache|2.25GB\n")
	if !h.Known || h.ImagesBytes != 1.5e9 || h.ContainersBytes != 20e6 || h.VolumesBytes != 500e3 || h.BuildCacheBytes != 2.25e9 {
		t.Fatalf("%+v", h)
	}
	if h := parseDockerDF("nothing"); h.Known || h.UnknownReason == "" {
		t.Fatalf("%+v", h)
	}
}

func TestBootTracking(t *testing.T) {
	f := newFake("linux")
	boot := f.now.Add(-time.Hour).UnixMilli()
	if trackBoot(f, "/s.json", boot) {
		t.Fatal("first read is a baseline")
	}
	if trackBoot(f, "/s.json", boot+3000) {
		t.Fatal("same boot, jitter")
	}
	f.now = f.now.Add(3 * time.Hour)
	boot2 := f.now.Add(-5 * time.Minute).UnixMilli()
	if !trackBoot(f, "/s.json", boot2) {
		t.Fatal("a boot nobody asked for")
	}
	// Expected: crsp marks it first.
	f.now = f.now.Add(30 * time.Hour)
	if trackBoot(f, "/s.json", boot2) {
		t.Fatal("the flag ends after 24 hours")
	}
	if err := MarkExpectedRestart(f, "/s.json"); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(10 * time.Minute)
	if trackBoot(f, "/s.json", f.now.Add(-time.Minute).UnixMilli()) {
		t.Fatal("a restart crsp asked for is not flagged")
	}
	if trackBoot(f, "", boot) {
		t.Fatal("no state file")
	}
}

func TestBeats(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	b := NewBeats(func() time.Time { return now })
	if v := b.View(); v.RTTKnown || v.SkewKnown {
		t.Fatal("nothing yet")
	}
	b.Sent()
	sent := now
	now = now.Add(200 * time.Millisecond)
	// The controller's clock is 40 s behind the agent's.
	b.Replied(sent.Add(100*time.Millisecond - 40*time.Second).UnixMilli())
	v := b.View()
	if !v.RTTKnown || v.RTT != 200*time.Millisecond || !v.SkewKnown || v.Skew != 40*time.Second || !v.LastBeat.Equal(now) {
		t.Fatalf("%+v", v)
	}
	b.Reconnected()
	now = now.Add(23 * time.Hour)
	b.Reconnected()
	if b.View().Reconnects24 != 2 {
		t.Fatal("two in 24 h")
	}
	now = now.Add(2 * time.Hour)
	if b.View().Reconnects24 != 1 {
		t.Fatal("the old one drops out")
	}
}

func TestMonitorCollect(t *testing.T) {
	f := newFake("linux")
	f.files["/proc/uptime"] = "100 1\n"
	f.files["/proc/meminfo"] = "SwapTotal: 0 kB\nSwapFree: 0 kB\n"
	f.files["/proc/stat"] = "cpu 1 0 1 8 0 0 0 0\n"
	f.files["/proc/loadavg"] = "0.1 0.2 0.3 1/1 1\n"
	f.files["/etc/debian_version"] = "12"
	f.disks["/"] = [2]int64{100, 40}
	f.disks["/data"] = [2]int64{200, 150}
	m := NewMonitor(Config{Env: f, Sync: true, GitHubURL: "https://github.com", AgentVersion: "1.2.3",
		RunnerVersion: func() string { return "2.320.0" }, Docker: func() (string, string) { return "docker", "27.0" },
		Volumes: []Volume{{"system", "/"}, {"data_dir", "/data"}, {"docker", "/missing"}}})
	f.runs["docker system df --format {{.Type}}|{{.Size}}"] = Result{Out: "Images|1GB\n"}
	f.runs["systemctl is-enabled suspend.target hibernate.target hybrid-sleep.target suspend-then-hibernate.target"] = Result{Out: "masked\nmasked\nmasked\nmasked\n"}
	r := m.Collect(context.Background())
	if !r.Boot.Known || len(r.Volumes) != 3 || !r.Volumes[0].Known || r.Volumes[0].FreeBytes != 40 || r.Volumes[2].Known || r.Volumes[2].UnknownReason == "" {
		t.Fatalf("%+v %+v", r.Boot, r.Volumes)
	}
	if r.Oom.Known || r.Oom.UnknownReason == "" || r.Swap.UnknownReason != "" && r.Swap.Known {
		t.Fatalf("oom %+v swap %+v", r.Oom, r.Swap)
	}
	c := r.Connectivity
	if !c.GithubKnown || !c.GithubReachable || !c.DnsKnown || !c.DnsOk || c.RttKnown || c.SkewKnown {
		t.Fatalf("%+v", c)
	}
	if r.Versions.AgentVersion != "1.2.3" || r.Versions.RunnerAppVersion != "2.320.0" || r.Versions.DockerVersion != "27.0" {
		t.Fatalf("%+v", r.Versions)
	}
	if !r.Updates.Known || !r.Power.SleepKnown || !r.DockerUse.Known || r.DockerUse.ImagesBytes != 1e9 {
		t.Fatalf("%+v %+v %+v", r.Updates, r.Power, r.DockerUse)
	}
	// Steal needs two samples.
	if r.LoadAverage.StealKnown {
		t.Fatal("first steal read")
	}
	f.files["/proc/stat"] = "cpu 2 0 2 16 0 0 4 4\n"
	f.now = f.now.Add(time.Minute)
	if l := m.Collect(context.Background()).LoadAverage; !l.StealKnown || l.StealPct <= 0 {
		t.Fatalf("%+v", l)
	}
}

func TestMonitorGitHubDownAndOOMDelta(t *testing.T) {
	f := newFake("linux")
	f.headErr, f.dnsErr = errors.New("timeout"), errors.New("no such host")
	f.globs["/sys/fs/cgroup/crsp*/memory.events"] = []string{"/p/memory.events"}
	f.files["/p/memory.events"] = "oom_kill 3\n"
	m := NewMonitor(Config{Env: f, Sync: true, GitHubURL: "https://github.com"})
	r := m.Collect(context.Background())
	c := r.Connectivity
	if !c.GithubKnown || c.GithubReachable || c.GithubError != "timeout" || c.DnsOk || c.DnsError == "" {
		t.Fatalf("%+v", c)
	}
	if !r.Oom.Known || r.Oom.TotalKills != 3 || r.Oom.LastKillUnixMs != 0 {
		t.Fatalf("baseline is not a kill: %+v", r.Oom)
	}
	f.files["/p/memory.events"] = "oom_kill 4\n"
	f.now = f.now.Add(time.Minute)
	if o := m.Collect(context.Background()).Oom; o.TotalKills != 4 || o.LastKillUnixMs != f.now.UnixMilli() {
		t.Fatalf("%+v", o)
	}
}

func TestMonitorBudgetAndSlowOffPath(t *testing.T) {
	f := newFake("linux")
	f.files["/proc/uptime"] = "1 1"
	ch := make(chan struct{})
	defer close(ch)
	f.hang = map[string]chan struct{}{"needs-restarting -r": ch}
	m := NewMonitor(Config{Env: f, Budget: 20 * time.Millisecond})
	if v := m.volume(Volume{"system", "/x"}); v.Known {
		t.Fatal("unreadable volume")
	}
	got := budgeted(context.Background(), 10*time.Millisecond, func() string { <-ch; return "late" }, func(w string) string { return w })
	if got == "late" || got == "" {
		t.Fatalf("budget: %q", got)
	}
	// Slow collectors are not waited for: the first report says not read yet.
	r := m.Collect(context.Background())
	if r.Updates == nil || r.Power == nil || r.DockerUse == nil || r.Boot == nil {
		t.Fatalf("%+v", r)
	}
}
