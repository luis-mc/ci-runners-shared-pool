package agent_test

import (
	"context"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/load"
)

func winBox(cmds ...cmdRule) *fakeSys {
	b := linuxBox()
	b.goos = "windows"
	b.cmds = cmds
	return &fakeSys{b}
}

func TestEngineDockerInWSL2ThroughTheWindowsCLI(t *testing.T) {
	s := winBox(
		cmdRule{[]string{"docker", "version"}, load.Result{Out: "27.3.1\n"}, true},
		cmdRule{[]string{"docker", "info"}, load.Result{Out: "Ubuntu 24.04.1 LTS\n"}, true})
	r := agent.DetectEngine(s)
	if r.Kind != agent.EngineWSLViaCLI || !r.Usable || r.Version != "27.3.1" {
		t.Fatalf("%+v", r)
	}
	if s.did("wsl") {
		t.Fatal("wsl must not be called when the Windows CLI answers")
	}
}

func TestEngineDockerDesktop(t *testing.T) {
	r := agent.DetectEngine(winBox(
		cmdRule{[]string{"docker", "version"}, load.Result{Out: "27.3.1\n"}, true},
		cmdRule{[]string{"docker", "info"}, load.Result{Out: "Docker Desktop\n"}, true}))
	if r.Kind != agent.EngineDesktop || !r.Usable {
		t.Fatalf("%+v", r)
	}
}

func TestEngineFallsBackToWslDockerOnlyWhenTheCLICannotRun(t *testing.T) {
	r := agent.DetectEngine(winBox(cmdRule{[]string{"wsl", "docker", "version"}, load.Result{Out: "26.1.4\n"}, true}))
	if r.Kind != agent.EngineWSLDirect || !r.Usable || r.Version != "26.1.4" {
		t.Fatalf("%+v", r)
	}
}

func TestEngineNotUsableSaysWhy(t *testing.T) {
	down := agent.DetectEngine(winBox(cmdRule{[]string{"docker", "version"}, load.Result{Code: 1}, true}))
	if down.Usable || down.Kind != agent.EngineWSLViaCLI || down.Reason == "" {
		t.Fatalf("engine down: %+v", down)
	}
	none := agent.DetectEngine(winBox())
	if none.Usable || none.Kind != agent.EngineNone || none.Reason == "" {
		t.Fatalf("none: %+v", none)
	}
	wslNoDocker := agent.DetectEngine(winBox(cmdRule{[]string{"wsl", "docker", "version"}, load.Result{Code: 127}, true}))
	if wslNoDocker.Usable || wslNoDocker.Kind != agent.EngineNone {
		t.Fatalf("wsl without docker: %+v", wslNoDocker)
	}
}

func TestEngineOnLinuxNeverCallsWsl(t *testing.T) {
	b := linuxBox()
	b.cmds = []cmdRule{{[]string{"docker", "version"}, load.Result{Out: "27.3.1\n"}, true}}
	if r := agent.DetectEngine(&fakeSys{b}); r.Kind != agent.EngineNative || !r.Usable {
		t.Fatalf("%+v", r)
	}
	if b.did("wsl") {
		t.Fatal("wsl called on Linux")
	}
	if r := agent.DetectEngine(&fakeSys{linuxBox()}); r.Usable || r.Kind != agent.EngineNone {
		t.Fatalf("%+v", r)
	}
}

func TestParseWSLKVM(t *testing.T) {
	for name, c := range map[string]struct {
		out  string
		want agent.WSLKVM
	}{
		"loaded":     {"device=1\nmodule=kvm_intel\n", agent.WSLKVM{Known: true, DevicePresent: true, ModuleLoaded: true, Module: "kvm_intel"}},
		"not loaded": {"device=0\n", agent.WSLKVM{Known: true}},
		"crlf+nul":   {"d\x00evice=0\r\nmodule=kvm_amd\r\n", agent.WSLKVM{Known: true, ModuleLoaded: true, Module: "kvm_amd"}},
		"noise":      {"wsl: no distribution\n", agent.WSLKVM{}},
		"empty":      {"", agent.WSLKVM{}},
	} {
		if got := agent.ParseWSLKVM(c.out); got != c.want {
			t.Errorf("%s: %+v, want %+v", name, got, c.want)
		}
	}
}

func TestReadWSLKVMOnlyOnWindowsAndOnlyReads(t *testing.T) {
	if k := agent.ReadWSLKVM(&fakeSys{linuxBox()}); k.Known {
		t.Fatal("Linux is not asked")
	}
	s := winBox(cmdRule{[]string{"wsl", "-e", "sh", "-c"}, load.Result{Out: "device=0\n"}, true})
	if k := agent.ReadWSLKVM(s); !k.Known || k.DevicePresent || k.ModuleLoaded {
		t.Fatalf("%+v", k)
	}
	for _, c := range s.calls {
		for _, a := range c {
			if a == "modprobe" || a == "insmod" {
				t.Fatalf("the probe must not load a module: %v", c)
			}
		}
	}
	if k := agent.ReadWSLKVM(winBox()); k.Known {
		t.Fatal("no wsl.exe: not known")
	}
}

// CRSP127: the engine says which OS its containers run.
func TestEngineReportsContainerOS(t *testing.T) {
	r := agent.DetectEngine(winBox(
		cmdRule{[]string{"docker", "version"}, load.Result{Out: "29.9.3\r\n"}, true},
		cmdRule{[]string{"docker", "info", "--format", "{{.OSType}}"}, load.Result{Out: "linux\r\n"}, true},
		cmdRule{[]string{"docker", "info"}, load.Result{Out: "Docker Desktop\n"}, true}))
	if r.Kind != agent.EngineDesktop || !r.Usable || r.ContainerOS != "linux" || r.Version != "29.9.3" {
		t.Fatalf("%+v", r)
	}
	win := agent.DetectEngine(winBox(
		cmdRule{[]string{"docker", "version"}, load.Result{Out: "29.9.3\n"}, true},
		cmdRule{[]string{"docker", "info", "--format", "{{.OSType}}"}, load.Result{Out: "windows\n"}, true}))
	if win.ContainerOS != "windows" {
		t.Fatalf("windows containers mode: %+v", win)
	}
	if wsl := agent.DetectEngine(winBox(cmdRule{[]string{"wsl", "docker", "version"}, load.Result{Out: "26.1.4\n"}, true})); wsl.ContainerOS != "linux" {
		t.Fatalf("wsl docker: %+v", wsl)
	}
	if none := agent.DetectEngine(winBox()); none.ContainerOS != "" {
		t.Fatalf("no engine must report no container OS: %+v", none)
	}
	if down := agent.DetectEngine(winBox(cmdRule{[]string{"docker", "version"}, load.Result{Code: 1}, true})); down.ContainerOS != "" {
		t.Fatalf("engine down: %+v", down)
	}
}

func TestMachineReportCarriesContainerOS(t *testing.T) {
	s := winBox(
		cmdRule{[]string{"docker", "version"}, load.Result{Out: "29.9.3\n"}, true},
		cmdRule{[]string{"docker", "info", "--format", "{{.OSType}}"}, load.Result{Out: "linux\n"}, true},
		cmdRule{[]string{"docker", "info"}, load.Result{Out: "Docker Desktop\n"}, true})
	rep := agent.BuildMachineReport(context.Background(), s, &agentv1.Facts{}, nil, time.Now())
	e := rep.GetEngine()
	if e.GetKind() != "docker-desktop" || !e.GetUsable() || e.GetContainerOs() != "linux" {
		t.Fatalf("%+v", e)
	}
}
