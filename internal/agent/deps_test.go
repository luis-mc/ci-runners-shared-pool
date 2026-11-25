package agent_test

import (
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/agent"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/internal/load"
)

type depsFakeSys struct {
	*fakeProbeBase
	admin bool
}

func (d depsFakeSys) IsAdmin() bool { return d.admin }

func box(goos string, rules ...cmdRule) depsFakeSys {
	b := linuxBox()
	b.goos = goos
	b.cmds = rules
	return depsFakeSys{b, true}
}

func rule(res string, code int, argv ...string) cmdRule {
	return cmdRule{argv, load.Result{Out: res, Code: code}, true}
}

func state(rep *agentv1.DepsReport, id string) string {
	for _, it := range rep.GetItems() {
		if it.GetId() == id {
			return it.GetState()
		}
	}
	return "absent"
}

func TestDetectDepsTable(t *testing.T) {
	desktopOK := []cmdRule{
		rule("27.0.1\n", 0, "docker", "version"),
		rule("Docker Desktop\n", 0, "docker", "info", "--format", "{{.OperatingSystem}}"),
		rule("linux\n", 0, "docker", "info", "--format", "{{.OSType}}"),
		rule("ok", 0, "wsl", "--status"),
	}
	cases := []struct {
		name     string
		sys      depsFakeSys
		runnerOS string
		want     map[string]string
	}{
		{"windows: nothing installed", box("windows"), "linux",
			map[string]string{"wsl2": "missing", "docker-in-wsl2": "missing"}},
		{"windows: wsl installed but not ready", box("windows", rule("", 1, "wsl", "--status")), "linux",
			map[string]string{"wsl2": "missing", "docker-in-wsl2": "missing"}},
		{"windows: wsl ok, no engine", box("windows", rule("ok", 0, "wsl", "--status")), "linux",
			map[string]string{"wsl2": "ok", "docker-in-wsl2": "missing"}},
		{"windows: engine stopped", box("windows", rule("ok", 0, "wsl", "--status"), rule("", 1, "docker", "version")), "linux",
			map[string]string{"wsl2": "ok", "docker-in-wsl2": "stopped"}},
		{"windows: windows containers", box("windows", rule("27.0.1\n", 0, "docker", "version"), rule("windows\n", 0, "docker", "info", "--format", "{{.OSType}}"), rule("ok", 0, "wsl", "--status")), "linux",
			map[string]string{"wsl2": "ok", "docker-desktop": "wrong_kind"}},
		{"windows: docker desktop ok", box("windows", desktopOK...), "linux",
			map[string]string{"wsl2": "ok", "docker-desktop": "ok"}},
		{"windows: docker in the crsp distro ok", box("windows", rule("ok", 0, "wsl", "--status"),
			rule("27.1.0\n", 0, "wsl", "-d", "crsp-docker", "-u", "root", "--", "docker", "version")), "linux",
			map[string]string{"wsl2": "ok", "docker-in-wsl2": "ok"}},
		{"windows runner on windows needs nothing", box("windows"), "windows", map[string]string{}},
		{"macos runner on macos needs nothing", box("darwin"), "macos", map[string]string{}},
		{"linux: no docker", box("linux"), "linux", map[string]string{"docker-engine": "missing"}},
		{"linux: engine stopped", box("linux", rule("", 1, "docker", "version")), "linux", map[string]string{"docker-engine": "stopped"}},
		{"linux: ok", box("linux", rule("27.0.1\n", 0, "docker", "version"), rule("linux\n", 0, "docker", "info", "--format", "{{.OSType}}")), "linux",
			map[string]string{"docker-engine": "ok"}},
		{"linux: podman counts", box("linux", rule("5.0.0\n", 0, "podman", "version", "--format", "{{.Version}}")), "linux",
			map[string]string{"docker-engine": "ok"}},
		{"linux: snap docker is the wrong kind", box("linux", rule("27.0.1\n", 0, "docker", "version"), rule("", 0, "snap", "list", "docker")), "linux",
			map[string]string{"docker-engine": "wrong_kind"}},
		{"macos: no docker", box("darwin"), "linux", map[string]string{"docker-desktop": "missing"}},
		{"macos: ok", box("darwin", rule("27.0.1\n", 0, "docker", "version"), rule("linux\n", 0, "docker", "info", "--format", "{{.OSType}}")), "linux",
			map[string]string{"docker-desktop": "ok"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rep := agent.DetectDeps(c.sys, c.runnerOS, "ephemeral")
			if len(rep.GetItems()) != len(c.want) {
				t.Fatalf("items = %v, want %v", rep.GetItems(), c.want)
			}
			for id, st := range c.want {
				if got := state(rep, id); got != st {
					t.Errorf("%s = %s, want %s (%v)", id, got, st, rep.GetItems())
				}
			}
			if rep.GetOs() != c.sys.goos || rep.GetDiskFreeBytes() != 200<<30 || !rep.GetAdmin() {
				t.Errorf("report facts: %v", rep)
			}
		})
	}
}

func TestDetectDepsDistroAndVirtualization(t *testing.T) {
	if rep := agent.DetectDeps(box("linux"), "linux", "ephemeral"); rep.GetDistro() != "" {
		// the fake os-release has no ID line
		t.Errorf("distro = %q", rep.GetDistro())
	}
	b := box("linux")
	b.files["/etc/os-release"] = "ID=ubuntu\nVERSION_CODENAME=noble\n"
	if rep := agent.DetectDeps(b, "linux", "ephemeral"); rep.GetDistro() != "ubuntu" {
		t.Errorf("distro = %q", rep.GetDistro())
	}
	off := box("windows",
		rule("False\n", 0, "powershell", "-NoProfile", "-Command", "(Get-CimInstance Win32_ComputerSystem).HypervisorPresent"),
		rule("False\n", 0, "powershell", "-NoProfile", "-Command", "(Get-CimInstance Win32_Processor | Select-Object -First 1).VirtualizationFirmwareEnabled"))
	if !agent.DetectDeps(off, "linux", "ephemeral").GetVirtualizationOff() {
		t.Error("virtualization off was not read")
	}
	if agent.DetectDeps(box("windows"), "linux", "ephemeral").GetVirtualizationOff() {
		t.Error("unknown must not be reported as off")
	}
	b2 := box("windows")
	b2.admin = false
	if agent.DetectDeps(b2, "linux", "ephemeral").GetAdmin() {
		t.Error("admin should be false")
	}
}
