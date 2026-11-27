package agent_test

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/load"
)

func byName(bs []*agentv1.BackendSupport) map[string]*agentv1.BackendSupport {
	m := map[string]*agentv1.BackendSupport{}
	for _, b := range bs {
		m[b.Name] = b
	}
	return m
}

// R5.11: for each backend the agent reports present or not, usable or not, its
// version, and the reason when it is not usable. A persistent runner is always
// listed as usable.
func TestBackendsOnALinuxBoxWithDockerAndKVM(t *testing.T) {
	b := linuxBox()
	b.files["/dev/kvm"] = ""
	b.rw["/dev/kvm"] = true
	b.cmds = []cmdRule{
		{[]string{"docker", "version"}, load.Result{Out: "27.3.1\n"}, true},
		{[]string{"docker", "info"}, load.Result{Out: "true\n"}, true},
		{[]string{"podman", "version"}, load.Result{Out: "5.2.0\n"}, true},
		{[]string{"podman", "info"}, load.Result{Out: "true\n"}, true},
		{[]string{"firecracker", "--version"}, load.Result{Out: "Firecracker v1.9.0\n"}, true},
	}
	m := byName(agent.DetectBackends(context.Background(), &fakeSys{b}))
	if !m["persistent"].Usable || !m["persistent"].Present {
		t.Fatalf("persistent = %+v", m["persistent"])
	}
	if d := m["docker"]; !d.Present || !d.Usable || d.Version != "27.3.1" || !d.Rootless {
		t.Fatalf("docker = %+v", d)
	}
	if p := m["podman"]; !p.Usable || p.Version != "5.2.0" || !p.Rootless {
		t.Fatalf("podman = %+v", p)
	}
	if k := m["kvm"]; !k.Present || !k.Usable {
		t.Fatalf("kvm = %+v", k)
	}
	if f := m["firecracker"]; !f.Usable || f.Version != "1.9.0" {
		t.Fatalf("firecracker = %+v", f)
	}
	for _, name := range []string{"nerdctl", "wsl2", "hyperv", "tart"} {
		if x := m[name]; x == nil || x.Usable || x.Reason == "" {
			t.Errorf("%s = %+v: not usable here, and it must say why", name, x)
		}
	}
	if len(m) != 9 {
		t.Fatalf("%d backends reported, want 9", len(m))
	}
}

func TestBackendReasons(t *testing.T) {
	b := linuxBox()
	b.files["/dev/kvm"] = "" // exists, not readable by the agent account
	b.cmds = []cmdRule{
		{[]string{"docker", "version"}, load.Result{Code: 1}, true}, // client present, engine down
		{[]string{"docker", "--version"}, load.Result{Out: "Docker version 27.3.1, build abc\n"}, true},
	}
	m := byName(agent.DetectBackends(context.Background(), &fakeSys{b}))
	if d := m["docker"]; !d.Present || d.Usable || d.Version != "27.3.1" || d.Reason == "" {
		t.Fatalf("docker with the engine down = %+v", d)
	}
	if k := m["kvm"]; !k.Present || k.Usable || k.Reason == "" {
		t.Fatalf("kvm without access = %+v", k)
	}
	b2 := linuxBox()
	if k := byName(agent.DetectBackends(context.Background(), &fakeSys{b2}))["kvm"]; k.Present || k.Reason != "no /dev/kvm" {
		t.Fatalf("kvm = %+v", k)
	}
	if d := byName(agent.DetectBackends(context.Background(), &fakeSys{b2}))["docker"]; d.Present || d.Reason == "" {
		t.Fatalf("docker = %+v", d)
	}
}

func TestBackendsWindowsReadDockerThroughTheWindowsCLIFirst(t *testing.T) {
	b := linuxBox()
	b.goos, b.files = "windows", map[string]string{}
	b.cmds = []cmdRule{
		{[]string{"docker", "version"}, load.Result{Out: "27.0.3\n"}, true},
		{[]string{"wsl", "--status"}, load.Result{Out: "Default Version: 2\n"}, true},
		{[]string{"wsl", "--version"}, load.Result{Out: "WSL version: 2.3.26.0\nKernel version: 5.15\n"}, true},
		{[]string{"powershell"}, load.Result{Out: "Enabled\n"}, true},
	}
	p := &fakeSys{b}
	m := byName(agent.DetectBackends(context.Background(), p))
	if !m["docker"].Usable || !m["wsl2"].Usable || m["wsl2"].Version != "2.3.26.0" || !m["hyperv"].Usable {
		t.Fatalf("docker=%+v wsl2=%+v hyperv=%+v", m["docker"], m["wsl2"], m["hyperv"])
	}
	if p.did("wsl", "docker") {
		t.Fatal("the Windows docker CLI answered; docker was read through wsl too")
	}
	if m["kvm"].Usable || m["kvm"].Reason == "" {
		t.Fatalf("kvm on Windows = %+v", m["kvm"])
	}
}

func TestBackendsWindowsDockerFallsBackToWSL(t *testing.T) {
	b := linuxBox()
	b.goos, b.files = "windows", map[string]string{}
	b.cmds = []cmdRule{{[]string{"wsl", "docker", "version"}, load.Result{Out: "26.1.4\n"}, true}}
	if d := byName(agent.DetectBackends(context.Background(), &fakeSys{b}))["docker"]; !d.Usable || d.Version != "26.1.4" {
		t.Fatalf("docker via wsl = %+v", d)
	}
}

func TestBackendsLinuxNeverCallsWSL(t *testing.T) {
	p := &fakeSys{linuxBox()}
	agent.DetectBackends(context.Background(), p)
	if p.did("wsl") {
		t.Fatal("wsl was called on Linux")
	}
}

func TestBackendsMacOSTart(t *testing.T) {
	b := linuxBox()
	b.goos, b.arch, b.files = "darwin", "arm64", map[string]string{}
	b.cmds = []cmdRule{{[]string{"tart", "--version"}, load.Result{Out: "2.18.0\n"}, true}}
	if tr := byName(agent.DetectBackends(context.Background(), &fakeSys{b}))["tart"]; !tr.Usable || tr.Version != "2.18.0" {
		t.Fatalf("tart = %+v", tr)
	}
	b.arch = "x64"
	if tr := byName(agent.DetectBackends(context.Background(), &fakeSys{b}))["tart"]; tr.Usable || tr.Reason == "" {
		t.Fatalf("tart on Intel = %+v", tr)
	}
}

// R5.13: the agent detects again every 10 minutes (here: fast) and when the user
// presses Check again. A change is shown on the machine and written to the audit log.
func TestRedetectPeriodicallyAndOnDemandAndChangesAreAudited(t *testing.T) {
	c := startController(t)
	code, _ := c.Svc.CreateJoinCode(context.Background(), "owner", 0)
	cfg := fastConfig(t, c, code.Code)
	cfg.RedetectEvery = 60 * time.Millisecond
	p := cfg.Sys.(*fakeSys)
	runAgent(t, cfg)
	var id string
	waitFor(t, "joined", func() bool {
		vs, _ := c.Svc.List(context.Background())
		if len(vs) == 1 && vs[0].Machine.Online {
			id = vs[0].Machine.ID
			return true
		}
		return false
	})
	dockerUsable := func() bool {
		v, _ := c.Svc.Get(context.Background(), id)
		for _, b := range v.Backends {
			if b.Name == "docker" {
				return b.Usable
			}
		}
		return false
	}
	if dockerUsable() {
		t.Fatal("docker usable before it was installed")
	}
	// Docker gets installed; the periodic detection finds it.
	p.mu.Lock()
	p.cmds = append(p.cmds, cmdRule{[]string{"docker", "version"}, load.Result{Out: "27.0\n"}, true})
	p.mu.Unlock()
	waitFor(t, "docker found by the periodic detection", dockerUsable)
	changed := false
	for _, a := range c.Repo.AuditLog() {
		if a.Action == "machine.facts.change" {
			changed = true
		}
	}
	if !changed {
		t.Fatal("the change was not written to the audit log")
	}
	// Check again: the controller asks and the agent answers with its facts.
	p.mu.Lock()
	calls := len(p.calls)
	p.mu.Unlock()
	if err := c.Svc.Redetect(context.Background(), "owner", id); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "check again detected", func() bool { p.mu.Lock(); defer p.mu.Unlock(); return len(p.calls) > calls })
}

// CRSP127: Docker Desktop runs on WSL2 in its own distro; `wsl --status` can
// exit non-zero while the engine works. That is not "not ready".
func TestBackendsWSL2NotReadyFalseReportWhenDockerDesktopRunsOnWSL2(t *testing.T) {
	cmds := func(kernel string) []cmdRule {
		return []cmdRule{
			{[]string{"docker", "version"}, load.Result{Out: "29.9.3\n"}, true},
			{[]string{"docker", "info", "--format", "{{.KernelVersion}}"}, load.Result{Out: kernel}, true},
			{[]string{"wsl", "--status"}, load.Result{Code: 1}, true},
			{[]string{"wsl", "--version"}, load.Result{Out: "WSL version: 2.3.26.0\n"}, true},
		}
	}
	b := linuxBox()
	b.goos, b.files = "windows", map[string]string{}
	b.cmds = cmds("5.15.167.4-microsoft-standard-WSL2\r\n")
	w := byName(agent.DetectBackends(context.Background(), &fakeSys{b}))["wsl2"]
	if !w.Usable || w.Reason != "" || w.Version != "2.3.26.0" {
		t.Fatalf("wsl2 = %+v", w)
	}
	// Without a WSL2 kernel behind the engine the old report stays.
	b2 := linuxBox()
	b2.goos, b2.files = "windows", map[string]string{}
	b2.cmds = cmds("6.1.0\n")
	w2 := byName(agent.DetectBackends(context.Background(), &fakeSys{b2}))["wsl2"]
	if w2.Usable || w2.Reason == "" {
		t.Fatalf("wsl2 = %+v", w2)
	}
}
