package linux

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/persistent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys/sysfake"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

type rig struct {
	b    *Backend
	cmd  *sysfake.Commands
	fs   *sysfake.FS
	body []byte
}

func newRig() *rig {
	r := &rig{cmd: &sysfake.Commands{}, fs: sysfake.NewFS(), body: []byte("tarball")}
	fe := &sysfake.Fetcher{FS: r.fs, Body: r.body}
	verify := func(path, want string) error {
		d, _ := r.fs.ReadFile(path)
		sum := sha256.Sum256(d)
		if hex.EncodeToString(sum[:]) != want {
			return runners.ErrHashMismatch
		}
		return nil
	}
	// getent finds nothing: group and user are new.
	r.cmd.Rules = []sysfake.Rule{{Match: "getent", Result: sys.Result{ExitCode: 2}}}
	r.b = New(sys.Deps{Cmd: r.cmd, FS: r.fs, Fetch: fe, Verify: verify}, Config{})
	return r
}

func (r *rig) spec() backend.RunnerSpec {
	sum := sha256.Sum256(r.body)
	return backend.RunnerSpec{ID: "r1", Name: "box-1", OS: "linux", Arch: "x64",
		Labels: []string{"self-hosted", "linux", "x64"}, GitHubURL: "https://github.com/luis-mc/demo",
		RunnerVersion: "2.330.0", RunnerSHA256: hex.EncodeToString(sum[:])}
}

func (r *rig) index(prefix string) int {
	for i, t := range r.cmd.Texts() {
		if strings.HasPrefix(t, prefix) {
			return i
		}
	}
	return -1
}

func started(t *testing.T) *rig {
	t.Helper()
	r := newRig()
	if _, err := r.b.Start(context.Background(), r.spec(), backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	return r
}

// R5.21: its own system user, no login shell, no password, no sudo, in the
// group crsp-runners.
func TestUnitUserAndGroup(t *testing.T) {
	r := started(t)
	if r.index("groupadd --system crsp-runners") < 0 {
		t.Errorf("group not made: %v", r.cmd.Texts())
	}
	i := r.index("useradd")
	if i < 0 {
		t.Fatal("no useradd")
	}
	add := r.cmd.Texts()[i]
	for _, want := range []string{"--system", "--gid crsp-runners", "--shell /usr/sbin/nologin", "--no-create-home", "crsp-box-1"} {
		if !strings.Contains(add, want) {
			t.Errorf("useradd lacks %s: %s", want, add)
		}
	}
	for _, c := range r.cmd.Texts() {
		for _, bad := range []string{"passwd", "chpasswd", "sudo", "sudoers", "usermod", "wheel", "adm"} {
			if strings.Contains(c, bad) && !strings.HasPrefix(c, "getent passwd") {
				t.Errorf("call %q must not give a password or rights", c)
			}
		}
	}
	unit := string(r.fs.Files["/etc/systemd/system/actions.runner.luis-mc-demo.box-1.service"])
	if !strings.Contains(unit, "User=crsp-box-1\n") || !strings.Contains(unit, "Group=crsp-runners\n") {
		t.Errorf("unit:\n%s", unit)
	}
}

// R5.22: hardening and folder mode; hooks call /usr/local/bin/crsp.
func TestUnitHardeningAndFolderMode(t *testing.T) {
	r := started(t)
	unit := string(r.fs.Files["/etc/systemd/system/actions.runner.luis-mc-demo.box-1.service"])
	for _, want := range []string{"NoNewPrivileges=yes", "PrivateTmp=yes", "ProtectSystem=strict",
		"ReadWritePaths=/var/lib/crsp/runners/box-1", "Restart=on-failure"} {
		if !strings.Contains(unit, want+"\n") {
			t.Errorf("unit lacks %s", want)
		}
	}
	if r.index("chmod 0700 /var/lib/crsp/runners/box-1") < 0 {
		t.Errorf("folder mode: %v", r.cmd.Texts())
	}
	if r.index("chown -R crsp-box-1:crsp-runners /var/lib/crsp/runners/box-1") < 0 {
		t.Error("folder owner")
	}
	for _, n := range []string{"job-started", "job-completed"} {
		p := "/var/lib/crsp/runners/box-1/hooks/" + n + ".sh"
		body := string(r.fs.Files[p])
		if !strings.Contains(body, "/usr/local/bin/crsp") {
			t.Errorf("%s does not call /usr/local/bin/crsp", p)
		}
		if r.fs.Modes[p] != 0o700 {
			t.Errorf("%s mode %o", p, r.fs.Modes[p])
		}
	}
	env := string(r.fs.Files["/var/lib/crsp/runners/box-1/.env"])
	if !strings.Contains(env, "ACTIONS_RUNNER_HOOK_JOB_STARTED=/var/lib/crsp/runners/box-1/hooks/job-started.sh") {
		t.Errorf(".env:\n%s", env)
	}
	for _, want := range []string{"CI_TOOLS_DIR=/var/lib/crsp/runners/box-1/cache/tools\n", "CI_TOOLS_QUOTA_GIB=20\n", "CRSP_RUST_CACHE_CAP_GIB=40\n",
		"RUSTUP_HOME=/var/lib/crsp/runners/box-1/cache/tools/rustup\n", "CARGO_HOME=/var/lib/crsp/runners/box-1/cache/tools/cargo\n"} {
		if !strings.Contains(env, want) {
			t.Errorf(".env lacks %q:\n%s", want, env)
		}
	}
	if i, j := r.index("systemctl daemon-reload"), r.index("systemctl enable --now"); i < 0 || j < i {
		t.Error("daemon-reload then enable --now")
	}
}

// R6.2: the token is in the environment of the one process only; self-update off.
func TestTokenOnlyInEnvAndUpdateOff(t *testing.T) {
	r := started(t)
	n := 0
	for _, c := range r.cmd.Calls {
		if strings.Contains(c.String(), "REGTOKEN") {
			t.Error("token in an argument")
		}
		if c.SecretEnv["ACTIONS_RUNNER_INPUT_TOKEN"] == "REGTOKEN" {
			n++
			if c.Name != "runuser" || !strings.Contains(c.String(), "-u crsp-box-1 --") || !strings.Contains(c.String(), "--disableupdate") {
				t.Errorf("configure call: %s", c.String())
			}
		}
	}
	if n != 1 {
		t.Errorf("token reached %d processes", n)
	}
	for name, d := range r.fs.Files {
		if strings.Contains(string(d), "REGTOKEN") {
			t.Errorf("token written to %s", name)
		}
	}
}

func TestRerunAlreadyAndStaleEnv(t *testing.T) {
	r := started(t)
	r.cmd.Calls = nil
	if _, err := r.b.Start(context.Background(), r.spec(), backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	if len(r.cmd.Calls) != 0 {
		t.Errorf("already: %v", r.cmd.Texts())
	}
	r.fs.Files["/var/lib/crsp/runners/box-1/.env"] = []byte("stale\n")
	if _, err := r.b.Start(context.Background(), r.spec(), backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	if r.index("systemctl stop actions.runner.luis-mc-demo.box-1.service") < 0 || r.index("systemctl start actions.runner.luis-mc-demo.box-1.service") < r.index("pgrep") || r.index("useradd") >= 0 || r.index("runuser") >= 0 {
		t.Errorf("stale env: %v", r.cmd.Texts())
	}
}

func TestReregisterRemovesFirstAndStopsOnFailure(t *testing.T) {
	r := started(t)
	meta, _ := persistent.ReadMeta(r.fs, "/var/lib/crsp/runners/box-1/"+persistent.MetaFile)
	meta.Labels = []string{"self-hosted", "linux", "x64", "gpu"}
	_ = persistent.WriteMeta(r.fs, "/var/lib/crsp/runners/box-1/"+persistent.MetaFile, *meta)

	r.cmd.Calls = nil
	r.cmd.Rules = append([]sysfake.Rule{{Match: "runuser -u crsp-box-1 -- /var/lib/crsp/runners/box-1/config.sh remove", Result: sys.Result{ExitCode: 1}}}, r.cmd.Rules...)
	_, err := r.b.Start(context.Background(), r.spec(), backend.StartConfig{Registration: "REG2", Removal: "REMOVETOKEN"})
	if !errors.Is(err, ErrRemovalFailed) {
		t.Fatalf("err = %v", err)
	}
	if r.index("useradd") >= 0 || r.index("chmod") >= 0 || r.index("systemctl disable") >= 0 {
		t.Error("nothing changes after a failed removal")
	}
	if r.index("systemctl start actions.runner.luis-mc-demo.box-1.service") < 0 {
		t.Error("old unit started again")
	}

	r.cmd.Calls, r.cmd.Rules = nil, r.cmd.Rules[1:]
	if _, err := r.b.Start(context.Background(), r.spec(), backend.StartConfig{Registration: "REG2", Removal: "REMOVETOKEN"}); err != nil {
		t.Fatal(err)
	}
	rm, reg := r.index("runuser -u crsp-box-1 -- /var/lib/crsp/runners/box-1/config.sh remove"), r.index("runuser -u crsp-box-1 -- /var/lib/crsp/runners/box-1/config.sh --unattended")
	if rm < 0 || reg < rm {
		t.Errorf("remove at %d, register at %d", rm, reg)
	}
}

func TestWrongHashRefused(t *testing.T) {
	r := newRig()
	spec := r.spec()
	spec.RunnerSHA256 = strings.Repeat("cd", 32)
	_, err := r.b.Start(context.Background(), spec, backend.StartConfig{Registration: "REG"})
	if !errors.Is(err, runners.ErrHashMismatch) {
		t.Fatalf("err = %v", err)
	}
	if r.index("tar") >= 0 || r.index("useradd") >= 0 {
		t.Error("nothing is unpacked or made after a bad hash")
	}
	if len(r.fs.Names()) != 0 {
		t.Errorf("download kept: %v", r.fs.Names())
	}
}

const listOut = "actions.runner.luis-mc-demo.box-1.service enabled enabled\nactions.runner.other-old.hand.service enabled enabled\n"

func TestFindFromUnitList(t *testing.T) {
	r := started(t)
	r.cmd.Rules = []sysfake.Rule{
		{Match: "list-unit-files", Result: sys.Result{Stdout: listOut}},
		{Match: "show actions.runner.luis-mc-demo.box-1.service", Result: sys.Result{Stdout: "User=crsp-box-1\nWorkingDirectory=/var/lib/crsp/runners/box-1\nActiveState=active\n"}},
		{Match: "show actions.runner.other-old.hand.service", Result: sys.Result{Stdout: "User=pi\nWorkingDirectory=\nExecStart={ path=/home/pi/actions-runner/runsvc.sh ; argv[]=/home/pi/actions-runner/runsvc.sh ; }\nActiveState=inactive\n"}},
	}
	found, err := r.b.Find(context.Background())
	if err != nil || len(found) != 2 {
		t.Fatalf("%v %v", found, err)
	}
	if !found[0].Managed || found[0].Account != "crsp-box-1" || !found[0].Running {
		t.Errorf("%+v", found[0])
	}
	if found[1].Managed || found[1].Folder != "/home/pi/actions-runner" || found[1].Account != "pi" || found[1].Running {
		t.Errorf("%+v", found[1])
	}
}

func TestCleanUpTwice(t *testing.T) {
	r := started(t)
	svc := "actions.runner.luis-mc-demo.box-1"
	r.cmd.Rules = []sysfake.Rule{
		{Match: "list-unit-files", Result: sys.Result{Stdout: svc + ".service enabled\n"}},
		{Match: "show " + svc, Result: sys.Result{Stdout: "User=crsp-box-1\nWorkingDirectory=/var/lib/crsp/runners/box-1\nActiveState=active\n"}},
	}
	h := backend.Handle{Ref: svc}
	if err := r.b.CleanUp(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	if r.index("userdel crsp-box-1") < 0 {
		t.Error("user not removed")
	}
	if _, ok := r.fs.Files["/etc/systemd/system/"+svc+".service"]; ok {
		t.Error("unit not removed")
	}
	for n := range r.fs.Files {
		if strings.HasPrefix(n, "/var/lib/crsp/runners/box-1/") {
			t.Errorf("folder file left: %s", n)
		}
	}
	r.cmd.Rules = []sysfake.Rule{{Match: "list-unit-files", Result: sys.Result{}}}
	if err := r.b.CleanUp(context.Background(), h); err != nil {
		t.Fatalf("second CleanUp: %v", err)
	}
}

func TestDetect(t *testing.T) {
	r := newRig()
	r.cmd.Rules = []sysfake.Rule{{Match: "id -u", Result: sys.Result{Stdout: "1000\n"}}}
	s, _ := r.b.Detect(context.Background())
	if s.Usable || s.Reason == "" || !s.Present {
		t.Errorf("%+v", s)
	}
	r.cmd.Rules = []sysfake.Rule{{Match: "id -u", Result: sys.Result{Stdout: "0\n"}}}
	s, _ = r.b.Detect(context.Background())
	if !s.Usable {
		t.Errorf("%+v", s)
	}
	r.cmd.Rules = []sysfake.Rule{{Match: "systemctl --version", Result: sys.Result{ExitCode: 127}}}
	s, _ = r.b.Detect(context.Background())
	if s.Present {
		t.Errorf("%+v", s)
	}
}

// runners R5.46: a listener that outlives its unit blocks the start; crsp never
// starts a second listener for a registered runner.
func TestRestartNeverStartsASecondListener(t *testing.T) {
	r := started(t)
	r.b.d.Sleep = func(context.Context, time.Duration) {}
	r.cmd.Rules = []sysfake.Rule{{Match: "pgrep", Result: sys.Result{Stdout: "4242\n"}}}
	r.fs.Files["/var/lib/crsp/runners/box-1/.env"] = []byte("stale\n")
	r.cmd.Calls = nil
	_, err := r.b.Start(context.Background(), r.spec(), backend.StartConfig{Registration: "REGTOKEN"})
	if !errors.Is(err, persistent.ErrListenerAlive) {
		t.Fatalf("err = %v", err)
	}
	if r.index("systemctl start") >= 0 || r.index("systemctl restart") >= 0 {
		t.Errorf("a service was started beside a live listener: %v", r.cmd.Texts())
	}
}

func TestStopWaitsForTheListenerNotTheUnit(t *testing.T) {
	r := started(t)
	svc := persistent.ServiceName(r.spec().GitHubURL, r.spec().Name)
	r.cmd.Rules = []sysfake.Rule{
		{Match: "list-unit-files", Result: sys.Result{Stdout: svc + ".service enabled\n"}},
		{Match: "systemctl show", Result: sys.Result{Stdout: "User=crsp-box-1\nWorkingDirectory=/var/lib/crsp/runners/box-1\nExecStart={ path=/var/lib/crsp/runners/box-1/bin/runsvc.sh ; }\nActiveState=inactive\n"}},
	}
	if err := r.b.Stop(context.Background(), backend.Handle{Ref: svc}); err != nil {
		t.Fatal(err)
	}
	r.cmd.Calls = nil
	if err := r.b.Stop(context.Background(), backend.Handle{Ref: svc}); err != nil {
		t.Fatal(err)
	}
	if r.index("pgrep") < 0 || r.index("pgrep") < r.index("systemctl stop") {
		t.Errorf("Stop must look for the listener after the unit stops: %v", r.cmd.Texts())
	}
}

// R5.56: the tool homes are under the runner's own folder.
func TestToolHomesPerRunner(t *testing.T) {
	r := started(t)
	env := string(r.fs.Files["/var/lib/crsp/runners/box-1/.env"])
	for _, want := range []string{
		"RUSTUP_HOME=/var/lib/crsp/runners/box-1/cache/tools/rustup\n",
		"CARGO_HOME=/var/lib/crsp/runners/box-1/cache/tools/cargo\n",
	} {
		if !strings.Contains(env, want) {
			t.Errorf(".env lacks %q:\n%s", want, env)
		}
	}
}

// R5.11: the one-time tokens reach the redactor before any log line is written.
func TestSecretsRedacted(t *testing.T) {
	r := newRig()
	var learned []string
	r.b.d.Secret = func(s string) { learned = append(learned, s) }
	cfg := backend.StartConfig{Registration: "REGTOKEN", Removal: "RMTOKEN"}
	if _, err := r.b.Start(context.Background(), r.spec(), cfg); err != nil {
		t.Fatal(err)
	}
	if strings.Join(learned, ",") != "REGTOKEN,RMTOKEN" {
		t.Errorf("learned %v", learned)
	}
}

// R5.59: a listener that outlives the stop gets terminate, then kill, on its
// process group; nothing real is signalled, the fake process table answers.
func TestStopEndsASurvivingListenerGroup(t *testing.T) {
	r := started(t)
	r.b.d.Sleep = func(context.Context, time.Duration) {}
	svc := persistent.ServiceName(r.spec().GitHubURL, r.spec().Name)
	table := &procTable{pids: []int{4242}, termSurvives: true}
	r.b.d.Cmd = table
	if err := r.b.stopUnit(context.Background(), svc+".service", "/var/lib/crsp/runners/box-1"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(table.signals, ";"); got != "TERM 4242;TERM -4242;KILL 4242;KILL -4242" {
		t.Errorf("signals = %q", got)
	}
	if len(table.pids) != 0 {
		t.Errorf("listener left: %v", table.pids)
	}
}

func TestStopSkipsKillWhenTerminateWorks(t *testing.T) {
	r := started(t)
	r.b.d.Sleep = func(context.Context, time.Duration) {}
	table := &procTable{pids: []int{4242}}
	r.b.d.Cmd = table
	if err := r.b.stopUnit(context.Background(), "x.service", "/var/lib/crsp/runners/box-1"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(table.signals, ";"); got != "TERM 4242;TERM -4242" {
		t.Errorf("signals = %q", got)
	}
}

func TestStopReportsAListenerThatWillNotDie(t *testing.T) {
	r := started(t)
	r.b.d.Sleep = func(context.Context, time.Duration) {}
	table := &procTable{pids: []int{4242}, immortal: true}
	r.b.d.Cmd = table
	err := r.b.stopUnit(context.Background(), "x.service", "/var/lib/crsp/runners/box-1")
	if !errors.Is(err, persistent.ErrListenerAlive) {
		t.Fatalf("err = %v", err)
	}
}

// R5.59: the first start never runs beside a listener of the same folder.
func TestFreshStartRefusedWhileAListenerRuns(t *testing.T) {
	r := newRig()
	r.cmd.Rules = []sysfake.Rule{{Match: "pgrep", Result: sys.Result{Stdout: "777\n"}}}
	_, err := r.b.Start(context.Background(), r.spec(), backend.StartConfig{Registration: "REGTOKEN"})
	if !errors.Is(err, persistent.ErrListenerAlive) {
		t.Fatalf("err = %v", err)
	}
	if r.index("systemctl enable") >= 0 {
		t.Errorf("service started beside a listener: %v", r.cmd.Texts())
	}
}

func TestUnitKillsTheGroupOnStop(t *testing.T) {
	if !strings.Contains(Unit("n", "u", "/f"), "KillMode=mixed") {
		t.Error("the unit must end the listener with its service")
	}
}

// procTable is a fake process table: pgrep lists pids, ps gives pgid = pid,
// kill removes them. Nothing real is signalled.
type procTable struct {
	pids         []int
	termSurvives bool
	immortal     bool
	signals      []string
}

func (p *procTable) Run(_ context.Context, c sys.Command) (sys.Result, error) {
	switch c.Name {
	case "pgrep":
		if len(p.pids) == 0 {
			return sys.Result{ExitCode: 1}, nil
		}
		var b strings.Builder
		for _, n := range p.pids {
			fmt.Fprintf(&b, "%d\n", n)
		}
		return sys.Result{Stdout: b.String()}, nil
	case "ps":
		return sys.Result{Stdout: c.Args[len(c.Args)-1] + "\n"}, nil
	case "kill":
		sig, target := c.Args[1], c.Args[3]
		p.signals = append(p.signals, sig+" "+target)
		if !p.immortal && (sig == "KILL" || !p.termSurvives) {
			p.pids = nil
		}
	}
	return sys.Result{}, nil
}

// runners R5.53 / machines R5.54: the runner's CPU count reaches its jobs.
func TestEnvCarriesCIMaxParallel(t *testing.T) {
	r := newRig()
	spec := r.spec()
	spec.MaxParallel = 4
	if _, err := r.b.Start(context.Background(), spec, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	if env := string(r.fs.Files["/var/lib/crsp/runners/box-1/.env"]); !strings.Contains(env, "CI_MAX_PARALLEL=4\n") {
		t.Errorf(".env:\n%s", env)
	}
}

func TestEnvHasNoCIMaxParallelWithoutAPoolSize(t *testing.T) {
	r := started(t)
	if env := string(r.fs.Files["/var/lib/crsp/runners/box-1/.env"]); strings.Contains(env, "CI_MAX_PARALLEL") {
		t.Errorf("no pool size, no CPU count:\n%s", env)
	}
}

type fixedIDs map[string]int

func (f fixedIDs) ID(_ context.Context, n string) (int, error) { return f[n], nil }

// R5.72: the account is made with its persistent uid from the reserved range.
func TestUserGetsPersistentUID(t *testing.T) {
	r := newRig()
	user := runners.AccountName("box-1", nil)
	r.b.cfg.IDs = fixedIDs{user: 64003}
	if _, err := r.b.Start(context.Background(), r.spec(), backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	i := r.index("useradd")
	if i < 0 || !strings.Contains(r.cmd.Texts()[i], "--uid 64003") {
		t.Fatalf("useradd lacks the uid: %v", r.cmd.Texts())
	}
}

// R5.90, R5.91: every tool keeps its downloads in the runner's own folder, the
// hooks know the folder, and the setting is written once and never over a
// changed one.
func TestDependencyFolderAndKeepSettingPerRunner(t *testing.T) {
	r := newRig()
	spec := r.spec()
	spec.KeepDependencies = false
	if _, err := r.b.Start(context.Background(), spec, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	env := string(r.fs.Files["/var/lib/crsp/runners/box-1/.env"])
	for _, want := range []string{
		"CRSP_RUNNER_DIR=/var/lib/crsp/runners/box-1\n",
		"RUNNER_TOOL_CACHE=/var/lib/crsp/runners/box-1/cache/tools\n",
		"npm_config_cache=/var/lib/crsp/runners/box-1/cache/npm\n",
		"npm_config_store_dir=/var/lib/crsp/runners/box-1/cache/pnpm-store\n",
		"GOMODCACHE=/var/lib/crsp/runners/box-1/cache/go/mod\n",
		"GOCACHE=/var/lib/crsp/runners/box-1/cache/go/build\n",
		"PIP_CACHE_DIR=/var/lib/crsp/runners/box-1/cache/pip\n",
		"PLAYWRIGHT_BROWSERS_PATH=/var/lib/crsp/runners/box-1/cache/playwright\n",
	} {
		if !strings.Contains(env, want) {
			t.Errorf(".env lacks %q:\n%s", want, env)
		}
	}
	if got := string(r.fs.Files["/var/lib/crsp/runners/box-1/crsp-hygiene.env"]); got != "CRSP_KEEP_DEPENDENCIES=0\n" {
		t.Fatalf("settings file %q", got)
	}
	// the agent changed it; a start again does not undo it
	r.fs.Files["/var/lib/crsp/runners/box-1/crsp-hygiene.env"] = []byte("CRSP_KEEP_DEPENDENCIES=1\n")
	spec.MaxParallel = 4
	if _, err := r.b.Start(context.Background(), spec, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	if got := string(r.fs.Files["/var/lib/crsp/runners/box-1/crsp-hygiene.env"]); got != "CRSP_KEEP_DEPENDENCIES=1\n" {
		t.Fatalf("settings file overwritten: %q", got)
	}
}

func TestEnvCarriesTheVMImageMirror(t *testing.T) { // OPS37
	r := newRig()
	r.b.d.Addresses.VMImageMirror = "https://mirror.corp/vm"
	if _, err := r.b.Start(context.Background(), r.spec(), backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	if env := string(r.fs.Files["/var/lib/crsp/runners/box-1/.env"]); !strings.Contains(env, "CRSP_VM_IMAGE_MIRROR=https://mirror.corp/vm\n") {
		t.Errorf(".env:\n%s", env)
	}
	if env := string(started(t).fs.Files["/var/lib/crsp/runners/box-1/.env"]); strings.Contains(env, "CRSP_VM_IMAGE_MIRROR") {
		t.Errorf("default adds the variable:\n%s", env)
	}
}

// Caches R5.57: a second runner of the repository gets its own build folder and
// the repository's group, and the shared folders get group and default ACLs,
// so it never meets `Permission denied` on what the first runner made.
func TestSecondRunnerSharesToolFoldersByGroup(t *testing.T) {
	r := newRig()
	st := runnercache.SharedTools{Root: "/var/lib/crsp/tools/r1", Group: runnercache.RepoGroup("r1")}
	for _, name := range []string{"box-1", "box-2"} {
		spec := r.spec()
		spec.Name, spec.SharedTools = name, st
		if _, err := r.b.Start(context.Background(), spec, backend.StartConfig{Registration: "T"}); err != nil {
			t.Fatal(err)
		}
	}
	texts := strings.Join(r.cmd.Texts(), "\n")
	for _, want := range []string{
		"groupadd --system crsp-repo-r1",
		"usermod -aG crsp-repo-r1 crsp-box-1",
		"usermod -aG crsp-repo-r1 crsp-box-2",
		"/var/lib/crsp/tools/r1/cargo-target/box-1",
		"/var/lib/crsp/tools/r1/cargo-target/box-2",
		"setfacl -R -d -m g:crsp-repo-r1:rwX /var/lib/crsp/tools/r1",
	} {
		if !strings.Contains(texts, want) {
			t.Errorf("missing %q in:\n%s", want, texts)
		}
	}
	unit, err := r.fs.ReadFile(r.b.unitPath(persistent.ServiceName("https://github.com/luis-mc/demo", "box-2")))
	if err != nil || !strings.Contains(string(unit), "ReadWritePaths=/var/lib/crsp/runners/box-2 /var/lib/crsp/tools/r1") {
		t.Errorf("the unit must allow writes to the shared folder: %v\n%s", err, unit)
	}
	envText, _ := r.fs.ReadFile("/var/lib/crsp/runners/box-2/.env")
	if !strings.Contains(string(envText), "CARGO_TARGET_DIR=/var/lib/crsp/tools/r1/cargo-target/box-2") {
		t.Errorf("each runner has its own build folder:\n%s", envText)
	}
}
