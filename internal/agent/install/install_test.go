package install_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/agent/install"
	"github.com/luis-mc/ci-runners-shared-pool/internal/depsetup"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
)

type fakeSys struct {
	mu      sync.Mutex
	goos    string
	admin   bool
	jobs    int
	runs    [][]string
	writes  map[string]string
	removed []string
	files   map[string]string // path -> sha256 it will report
	dlHash  string
	dlTotal int64
	dls     []string
	reboots int
	// runFn answers a command; nil means exit 0 with no output.
	runFn func(argv []string, line func(stream, text string)) int
}

func newFake(goos string) *fakeSys {
	return &fakeSys{goos: goos, admin: true, writes: map[string]string{}, files: map[string]string{}}
}

func (f *fakeSys) Run(_ context.Context, argv []string, line func(string, string)) (int, error) {
	f.mu.Lock()
	f.runs = append(f.runs, append([]string(nil), argv...))
	fn := f.runFn
	f.mu.Unlock()
	if fn == nil {
		return 0, nil
	}
	return fn(argv, line), nil
}
func (f *fakeSys) Download(_ context.Context, url, dest string, on func(b, t int64)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dls = append(f.dls, url)
	f.files[dest] = f.dlHash
	total := f.dlTotal
	if on != nil {
		on(total/2, total)
		on(total, total)
	}
	return nil
}
func (f *fakeSys) SHA256(p string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.files[p]
	if !ok {
		return "", errors.New("no file")
	}
	return h, nil
}
func (f *fakeSys) WriteFile(p string, d []byte, _ os.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes[p] = string(d)
	f.files[p] = "x"
	return nil
}
func (f *fakeSys) Remove(p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, p)
	delete(f.files, p)
	return nil
}
func (f *fakeSys) Exists(p string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.files[p]
	return ok
}
func (f *fakeSys) GOOS() string    { return f.goos }
func (f *fakeSys) Arch() string    { return "amd64" }
func (f *fakeSys) IsAdmin() bool   { return f.admin }
func (f *fakeSys) ActiveJobs() int { return f.jobs }
func (f *fakeSys) Reboot() error   { f.reboots++; return nil }
func (f *fakeSys) TempDir() string {
	if f.goos == "windows" {
		return `C:\Temp`
	}
	return "/tmp"
}
func (f *fakeSys) ran(prefix ...string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.runs {
		if len(r) >= len(prefix) && strings.Join(r[:len(prefix)], " ") == strings.Join(prefix, " ") {
			return true
		}
	}
	return false
}

type rec struct {
	mu sync.Mutex
	ev []*agentv1.DepsEvent
}

func (r *rec) emit(e *agentv1.DepsEvent) { r.mu.Lock(); r.ev = append(r.ev, e); r.mu.Unlock() }
func (r *rec) last() *agentv1.DepsEvent  { return r.ev[len(r.ev)-1] }

func (r *rec) all() string {
	var b strings.Builder
	for _, e := range r.ev {
		b.WriteString(e.GetCommand() + "|" + e.GetLine() + "|" + e.GetError() + "\n")
	}
	return b.String()
}

const ubuntu = "ID=ubuntu\nVERSION_CODENAME=noble\n"

func setup(sys *fakeSys, pinned depsetup.List, states map[string]string) (*install.Runner, *rec) {
	rc := &rec{}
	tick := time.Unix(1000, 0)
	r := install.New(install.Config{
		Sys: sys, Pinned: pinned, Emit: rc.emit,
		State: func(_ context.Context, id string) string {
			if s, ok := states[id]; ok {
				return s
			}
			return depsetup.StateMissing
		},
		Engine: func(context.Context) install.EngineInfo {
			return install.EngineInfo{Usable: true, Version: "27.0.1", ContainerOS: "linux"}
		},
		OSRelease: func() string { return ubuntu },
		Now:       func() time.Time { tick = tick.Add(time.Second); return tick },
	})
	return r, rc
}

func req(items ...string) *agentv1.DepsInstall {
	return &agentv1.DepsInstall{JobId: "j1", ItemIds: items, RunnerOs: "linux", RunnerKind: "ephemeral", MachineName: "pc", MachineRole: "worker"}
}

func desktopList(sha string) depsetup.List {
	l := depsetup.MustLoad()
	var out depsetup.List
	out.Release = l.Release
	for _, p := range l.Items {
		if p.ID == "docker-desktop" && p.OS == "windows" {
			p.URL, p.SHA256 = "https://desktop.docker.com/win/main/amd64/Docker%20Desktop%20Installer.exe", sha
		}
		out.Items = append(out.Items, p)
	}
	return out
}

// DEP5: a hash mismatch runs nothing.
func TestHashMismatchRunsNothing(t *testing.T) {
	sys := newFake("windows")
	sys.dlHash, sys.dlTotal = "bad", 1000
	r, rc := setup(sys, desktopList("good"), nil)
	rq := req("docker-desktop")
	rq.AcceptLicence = true
	r.Install(context.Background(), rq)
	f := rc.last()
	if !f.GetFinal() || f.GetOutcome() != "failed" || f.GetError() != install.MsgHashMismatch {
		t.Fatalf("final = %v", f)
	}
	if len(sys.runs) != 0 {
		t.Fatalf("a command ran: %v", sys.runs)
	}
	if len(sys.removed) == 0 {
		t.Error("the bad file was not removed")
	}
}

func TestDesktopInstallsAfterMatchAndPausesForRestart(t *testing.T) {
	sys := newFake("windows")
	sys.dlHash, sys.dlTotal = "good", 1000
	sys.runFn = func(argv []string, line func(string, string)) int {
		line("stdout", "You must restart your computer to finish")
		return 0
	}
	r, rc := setup(sys, desktopList("good"), nil)
	rq := req("docker-desktop")
	rq.AcceptLicence = true
	r.Install(context.Background(), rq)
	f := rc.last()
	if f.GetOutcome() != "paused_restart" || len(f.GetAdded()) != 1 || f.GetAdded()[0].GetKind() != "windows-installer" {
		t.Fatalf("final = %v", f)
	}
	if !strings.HasSuffix(sys.runs[0][0], "crsp-docker-desktop.exe") || sys.runs[0][1] != "install" {
		t.Errorf("installer argv = %v", sys.runs[0])
	}
	if sys.reboots != 0 {
		t.Error("crsp rebooted by itself")
	}
	// download progress is real bytes, with a rate and a time left
	var sawBytes bool
	for _, e := range rc.ev {
		if e.GetPhase() == "download" && e.GetBytes() == 500 && e.GetTotal() == 1000 && e.GetRate() > 0 && e.GetEtaSec() > 0 && e.Pct != nil && e.GetPct() == 50 {
			sawBytes = true
		}
	}
	if !sawBytes {
		t.Error("no real download progress event")
	}
}

func TestDesktopNeedsLicenceAndPin(t *testing.T) {
	sys := newFake("windows")
	r, rc := setup(sys, desktopList("good"), nil)
	r.Install(context.Background(), req("docker-desktop"))
	if rc.last().GetOutcome() != "failed" || !strings.Contains(rc.last().GetError(), "right to use") || len(sys.dls) != 0 {
		t.Fatalf("licence not enforced: %v", rc.last())
	}
	// the shipped list has no sha256 for Docker Desktop: refused
	sys2 := newFake("windows")
	r2, rc2 := setup(sys2, depsetup.MustLoad(), nil)
	rq := req("docker-desktop")
	rq.AcceptLicence = true
	r2.Install(context.Background(), rq)
	if rc2.last().GetError() != install.MsgNotPinned || len(sys2.dls) != 0 || len(sys2.runs) != 0 {
		t.Fatalf("unpinned installer ran: %v", rc2.last())
	}
}

func TestUnknownItemRefused(t *testing.T) {
	for _, id := range []string{"evil-tool", "docker-engine"} { // second: not an item of windows
		sys := newFake("windows")
		r, rc := setup(sys, depsetup.MustLoad(), nil)
		r.Install(context.Background(), req(id))
		if rc.last().GetOutcome() != "failed" || len(sys.runs) != 0 || len(sys.dls) != 0 || len(sys.writes) != 0 {
			t.Errorf("%s: %v runs=%v", id, rc.last(), sys.runs)
		}
	}
}

func TestNeedsAdmin(t *testing.T) {
	sys := newFake("linux")
	sys.admin = false
	r, rc := setup(sys, depsetup.MustLoad(), nil)
	r.Install(context.Background(), req("docker-engine"))
	if rc.last().GetError() != install.MsgNeedsAdmin || len(sys.runs) != 0 {
		t.Fatalf("%v", rc.last())
	}
}

func TestLinuxEngineInstall(t *testing.T) {
	sys := newFake("linux")
	sys.runFn = func(argv []string, line func(string, string)) int {
		if argv[0] == "systemctl" && argv[1] == "is-enabled" {
			return 1
		}
		return 0
	}
	r, rc := setup(sys, depsetup.MustLoad(), nil)
	r.Install(context.Background(), req("docker-engine"))
	f := rc.last()
	if f.GetOutcome() != "done" || f.GetEngineVersion() != "27.0.1" {
		t.Fatalf("final = %v", f)
	}
	if got := sys.writes[install.RepoPath]; got != "deb [arch=amd64 signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu noble stable\n" {
		t.Errorf("repo line = %q", got)
	}
	if len(sys.dls) != 1 || sys.dls[0] != "https://download.docker.com/linux/ubuntu/gpg" {
		t.Errorf("downloads = %v", sys.dls)
	}
	for _, r := range sys.runs {
		j := strings.Join(r, " ")
		if r[0] == "sh" || r[0] == "bash" || strings.Contains(j, "curl") || strings.Contains(j, "get.docker.com") {
			t.Errorf("shell or curl used: %s", j)
		}
	}
	if !sys.ran("env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin") ||
		!sys.ran("systemctl", "enable", "--now", "docker") || !sys.ran("docker", "run", "--rm", "hello-world") {
		t.Errorf("runs = %v", sys.runs)
	}
	kinds := map[string]int{}
	for _, a := range f.GetAdded() {
		kinds[a.GetKind()]++
	}
	if kinds["apt-package"] != 5 || kinds["apt-repo-file"] != 1 || kinds["apt-key"] != 1 || kinds["systemd-enable"] != 1 {
		t.Errorf("added = %v", f.GetAdded())
	}
	// the first event of each command names the command; phases are counted
	first := rc.ev[0]
	if first.GetPhaseCount() < 3 || first.GetJobId() != "j1" || first.GetMachineName() != "pc" || first.GetMachineOs() != "linux" || first.GetTs() == 0 {
		t.Errorf("first event = %v", first)
	}
	var cmds int
	for _, e := range rc.ev {
		if e.GetCommand() != "" {
			cmds++
		}
		if e.Pct != nil && !e.GetFinal() && e.GetPhase() != "download" && e.GetBytes() == 0 {
			t.Errorf("a made-up percent: %v", e)
		}
		if e.Pct == nil && e.GetReason() == "" {
			t.Errorf("unknown pct without reason: %v", e)
		}
	}
	if cmds < 8 {
		t.Errorf("only %d command events", cmds)
	}
}

func TestLinuxEngineConflictsAndOtherDistro(t *testing.T) {
	sys := newFake("linux")
	sys.runFn = func(argv []string, line func(string, string)) int {
		if argv[0] == "dpkg-query" {
			line("stdout", "docker.io install ok installed")
			line("stdout", "runc install ok installed")
		}
		return 0
	}
	r, rc := setup(sys, depsetup.MustLoad(), nil)
	r.Install(context.Background(), req("docker-engine"))
	e := rc.last().GetError()
	if rc.last().GetOutcome() != "failed" || !strings.Contains(e, "docker.io") || !strings.Contains(e, "runc") || sys.ran("apt-get") || sys.ran("env") {
		t.Fatalf("conflicts not refused: %v", rc.last())
	}
	// other family: "Do it myself"
	sys2 := newFake("linux")
	rc3 := &rec{}
	r3 := install.New(install.Config{Sys: sys2, Pinned: depsetup.MustLoad(), Emit: rc3.emit, OSRelease: func() string { return "ID=arch\n" }})
	r3.Install(context.Background(), req("docker-engine"))
	if !strings.Contains(rc3.last().GetError(), "Do it myself") || sys2.ran("env") {
		t.Fatalf("other family: %v", rc3.last())
	}
}

// DEP8: the test container fails although every install command exited 0.
func TestVerifyFailsWhenTestContainerFails(t *testing.T) {
	sys := newFake("linux")
	sys.runFn = func(argv []string, line func(string, string)) int {
		if argv[0] == "docker" && argv[1] == "run" {
			line("stderr", "docker: Error response from daemon")
			return 125
		}
		return 0
	}
	r, rc := setup(sys, depsetup.MustLoad(), nil)
	r.Install(context.Background(), req("docker-engine"))
	f := rc.last()
	if f.GetOutcome() != "failed" || f.GetPhase() != "verify" || !strings.Contains(f.GetError(), "test container failed") {
		t.Fatalf("final = %v", f)
	}
	if len(f.GetAdded()) == 0 {
		t.Error("the final event must carry what was added so undo works")
	}
}

func TestVerifyWrongContainerOSAndVirtualization(t *testing.T) {
	sys := newFake("windows")
	r, rc := setup(sys, depsetup.MustLoad(), map[string]string{"wsl2": "ok", "docker-desktop": "ok"})
	_ = r
	rc2 := &rec{}
	r2 := install.New(install.Config{Sys: sys, Pinned: depsetup.MustLoad(), Emit: rc2.emit,
		State: func(context.Context, string) string { return "ok" },
		Engine: func(context.Context) install.EngineInfo {
			return install.EngineInfo{Usable: true, Version: "27", ContainerOS: "windows"}
		}})
	r2.Install(context.Background(), req("docker-desktop"))
	if rc2.last().GetOutcome() != "failed" || !strings.Contains(rc2.last().GetError(), "Linux containers are needed") || sys.ran("docker") {
		t.Fatalf("%v", rc2.last())
	}
	rc3 := &rec{}
	r3 := install.New(install.Config{Sys: sys, Pinned: depsetup.MustLoad(), Emit: rc3.emit,
		State:             func(context.Context, string) string { return "ok" },
		Engine:            func(context.Context) install.EngineInfo { return install.EngineInfo{Reason: "no answer"} },
		VirtualizationOff: func() bool { return true }})
	r3.Install(context.Background(), req("wsl2"))
	if !strings.Contains(rc3.last().GetError(), "Virtualization is turned off") {
		t.Fatalf("%v", rc3.last())
	}
	_ = rc
}

// DEP11: a second run on a good machine changes nothing.
func TestSecondRunChangesNothing(t *testing.T) {
	sys := newFake("linux")
	r, rc := setup(sys, depsetup.MustLoad(), map[string]string{"docker-engine": "ok"})
	r.Install(context.Background(), req("docker-engine"))
	if rc.last().GetOutcome() != "done" || len(rc.last().GetAdded()) != 0 {
		t.Fatalf("%v", rc.last())
	}
	if len(sys.writes) != 0 || len(sys.dls) != 0 || len(sys.removed) != 0 {
		t.Errorf("files changed: %v %v", sys.writes, sys.dls)
	}
	for _, r := range sys.runs {
		if r[0] == "apt-get" || r[0] == "env" || r[0] == "systemctl" || r[0] == "wsl" {
			t.Errorf("a changing command ran: %v", r)
		}
	}
}

// DEP7 (agent part): WSL needs a restart: stop with paused_restart, never reboot.
func TestWSLPausesForRestartAndResumes(t *testing.T) {
	sys := newFake("windows")
	sys.runFn = func(argv []string, line func(string, string)) int {
		if argv[0] == "wsl" && argv[1] == "--install" {
			line("stdout", "The requested operation is successful. Changes will not be effective until the system is rebooted.")
		}
		return 0
	}
	r, rc := setup(sys, depsetup.MustLoad(), nil)
	r.Install(context.Background(), req("docker-in-wsl2", "wsl2"))
	f := rc.last()
	if f.GetOutcome() != "paused_restart" || len(f.GetAdded()) != 1 || f.GetAdded()[0].GetKind() != "wsl-feature" {
		t.Fatalf("final = %v", f)
	}
	if len(sys.runs) != 1 || strings.Join(sys.runs[0], " ") != "wsl --install --no-distribution" {
		t.Errorf("runs = %v", sys.runs)
	}
	if sys.reboots != 0 {
		t.Error("rebooted by itself")
	}
	// after the restart: resume from install; wsl2 is ok now, the distro is made
	sys2 := newFake("windows")
	sys2.runFn = func(argv []string, line func(string, string)) int {
		if len(argv) > 2 && argv[1] == "--list" {
			line("stdout", "Ubuntu\x00")
		}
		return 0
	}
	r2, rc2 := setup(sys2, depsetup.MustLoad(), map[string]string{"wsl2": "ok"})
	rq := req("wsl2", "docker-in-wsl2")
	rq.ResumeFrom = "install"
	rq.Added = f.GetAdded()
	r2.Install(context.Background(), rq)
	f2 := rc2.last()
	if f2.GetOutcome() != "done" || len(f2.GetAdded()) != 2 {
		t.Fatalf("final = %v", f2)
	}
	// every command that works on a distro names crsp-docker; the user's Ubuntu is never named
	for _, c := range sys2.runs {
		j := strings.Join(c, " ")
		if strings.Contains(j, "Ubuntu ") && !strings.Contains(j, "--name crsp-docker") {
			t.Errorf("touched a distro: %s", j)
		}
		if c[0] == "wsl" && c[1] == "-d" && c[2] != "crsp-docker" {
			t.Errorf("wrong distro: %s", j)
		}
	}
	if !sys2.ran("wsl", "--install", "-d", "Ubuntu-24.04", "--name", "crsp-docker", "--no-launch") ||
		!sys2.ran("wsl", "-d", "crsp-docker", "-u", "root", "--", "systemctl", "enable", "--now", "docker") {
		t.Errorf("runs = %v", sys2.runs)
	}
	// resume_from=verify skips download and install completely
	sys3 := newFake("windows")
	r3, rc3 := setup(sys3, depsetup.MustLoad(), nil)
	rq3 := req("wsl2")
	rq3.ResumeFrom = "verify"
	r3.Install(context.Background(), rq3)
	if rc3.last().GetOutcome() != "done" || sys3.ran("wsl") || len(sys3.dls) != 0 || rc3.ev[0].GetPhaseCount() != 2 {
		t.Fatalf("resume at verify: %v %v", rc3.last(), sys3.runs)
	}
}

func TestStopBetweenCommands(t *testing.T) {
	sys := newFake("linux")
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	sys.runFn = func(argv []string, line func(string, string)) int {
		n++
		if n == 4 {
			cancel() // Stop pressed while a command runs: it finishes, the next one does not start
		}
		return 0
	}
	r, rc := setup(sys, depsetup.MustLoad(), nil)
	r.Install(ctx, req("docker-engine"))
	f := rc.last()
	if f.GetOutcome() != "stopped" || !f.GetFinal() {
		t.Fatalf("final = %v", f)
	}
	if n != 4 {
		t.Errorf("%d commands ran, want 4", n)
	}
}

func TestSecretsNeverInEvents(t *testing.T) {
	sys := newFake("linux")
	sys.runFn = func(argv []string, line func(string, string)) int {
		line("stdout", "login with token=abc123SECRET and Authorization: Bearer xyz789SECRET")
		line("stderr", `reading C:\Users\luis\AppData and /home/luis/.docker ghp_`+strings.Repeat("a", 30))
		return 0
	}
	r, rc := setup(sys, depsetup.MustLoad(), nil)
	r.Install(context.Background(), req("docker-engine"))
	all := rc.all()
	for _, bad := range []string{"abc123SECRET", "xyz789SECRET", "luis", "ghp_aaaa"} {
		if strings.Contains(all, bad) {
			t.Errorf("%q leaked in events", bad)
		}
	}
	if got := install.Redact("docker login --password hunter2 --token t0k"); strings.Contains(got, "hunter2") || strings.Contains(got, "t0k") {
		t.Errorf("flag secret leaked: %s", got)
	}
}

func TestSecondJobAndRestartHost(t *testing.T) {
	sys := newFake("windows")
	sys.jobs = 2
	r, rc := setup(sys, depsetup.MustLoad(), nil)
	r.RestartHost("j9", "pc", "worker")
	if rc.last().GetOutcome() != "failed" || sys.reboots != 0 {
		t.Fatalf("%v", rc.last())
	}
	sys.jobs = 0
	r.RestartHost("j9", "pc", "worker")
	if rc.last().GetOutcome() != "done" || sys.reboots != 1 {
		t.Fatalf("%v", rc.last())
	}
}

// DEP10: undo removes only what the record names.
func TestUndoOnlyRecorded(t *testing.T) {
	sys := newFake("linux")
	sys.files[install.KeyPath], sys.files[install.RepoPath] = "k", "r"
	sys.files["/etc/apt/sources.list.d/other.list"] = "o"
	r, rc := setup(sys, depsetup.MustLoad(), nil)
	r.Undo(context.Background(), &agentv1.DepsUndo{JobId: "u1", Added: []*agentv1.DepsAdded{
		{ItemId: "docker-engine", Kind: "systemd-enable", Ref: "docker"},
		{ItemId: "docker-engine", Kind: "apt-package", Ref: "docker-ce"},
		{ItemId: "docker-engine", Kind: "apt-package", Ref: "containerd.io"},
		{ItemId: "docker-engine", Kind: "apt-repo-file", Ref: install.RepoPath},
		{ItemId: "docker-engine", Kind: "apt-key", Ref: install.KeyPath},
	}})
	if rc.last().GetOutcome() != "done" || len(rc.last().GetAdded()) != 0 {
		t.Fatalf("%v", rc.last())
	}
	if !sys.ran("apt-get", "remove", "-y", "docker-ce", "containerd.io") || !sys.ran("systemctl", "disable", "--now", "docker") {
		t.Errorf("runs = %v", sys.runs)
	}
	if len(sys.runs) != 2 {
		t.Errorf("more than the record ran: %v", sys.runs)
	}
	if strings.Join(sys.removed, ",") != install.RepoPath+","+install.KeyPath || !sys.Exists("/etc/apt/sources.list.d/other.list") {
		t.Errorf("removed = %v", sys.removed)
	}
}

func TestUndoRefusesWhatIsNotRecordedOrNotOurs(t *testing.T) {
	sys := newFake("windows")
	r, rc := setup(sys, depsetup.MustLoad(), nil)
	r.Undo(context.Background(), &agentv1.DepsUndo{JobId: "u2", Added: []*agentv1.DepsAdded{
		{ItemId: "docker-in-wsl2", Kind: "wsl-distro", Ref: "Ubuntu"},
		{ItemId: "x", Kind: "apt-package", Ref: "--allow-remove-essential"},
		{ItemId: "x", Kind: "apt-key", Ref: "/etc/passwd"},
		{ItemId: "x", Kind: "rm-rf", Ref: "/"},
	}})
	f := rc.last()
	if f.GetOutcome() != "failed" || len(f.GetAdded()) != 4 || len(sys.runs) != 0 || len(sys.removed) != 0 {
		t.Fatalf("final=%v runs=%v removed=%v", f, sys.runs, sys.removed)
	}
	// the crsp distro is unregistered; WSL itself stays while another distro exists
	sys2 := newFake("windows")
	sys2.runFn = func(argv []string, line func(string, string)) int {
		if len(argv) > 1 && argv[1] == "--list" {
			line("stdout", "Ubuntu")
			line("stdout", "crsp-docker")
		}
		return 0
	}
	r2, rc2 := setup(sys2, depsetup.MustLoad(), nil)
	r2.Undo(context.Background(), &agentv1.DepsUndo{JobId: "u3", Added: []*agentv1.DepsAdded{
		{ItemId: "docker-in-wsl2", Kind: "wsl-distro", Ref: "crsp-docker"},
		{ItemId: "wsl2", Kind: "wsl-feature", Ref: "wsl2"},
	}})
	if !sys2.ran("wsl", "--unregister", "crsp-docker") || sys2.ran("wsl", "--uninstall") {
		t.Errorf("runs = %v", sys2.runs)
	}
	if len(rc2.last().GetAdded()) != 1 || rc2.last().GetAdded()[0].GetKind() != "wsl-feature" {
		t.Errorf("left = %v", rc2.last().GetAdded())
	}
}

// CRSP247: the progress event contract. Every event names a phase by a zero-based index below the count, carries
// a weight, and an event with no percent says why, so the bar is never stuck without a reason (UX-88).
func TestProgressEventsKeepTheContract(t *testing.T) {
	sys := newFake("windows")
	sys.dlHash, sys.dlTotal = "good", 1000
	r, rc := setup(sys, desktopList("good"), nil)
	rq := req("docker-desktop")
	rq.AcceptLicence = true
	r.Install(context.Background(), rq)
	if len(rc.ev) == 0 {
		t.Fatal("no events")
	}
	seen := map[int32]bool{}
	for _, e := range rc.ev {
		seen[e.GetPhaseIndex()] = true
		if e.GetPhaseIndex() < 0 || e.GetPhaseIndex() >= e.GetPhaseCount() {
			t.Errorf("phase %q has index %d of %d: the index is zero-based", e.GetPhase(), e.GetPhaseIndex(), e.GetPhaseCount())
		}
		if e.GetPhaseWeight() <= 0 {
			t.Errorf("phase %q has no weight", e.GetPhase())
		}
		if e.Pct == nil && e.GetReason() == "" {
			t.Errorf("phase %q has no percent and no reason", e.GetPhase())
		}
	}
	if !seen[0] {
		t.Error("no event for the first phase (index 0)")
	}
}
