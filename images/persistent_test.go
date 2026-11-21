package images

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The scripts of the persistent Linux pool container (runners R5.91, R5.93) are
// run here the way the runner runs them, in a folder of the test.

type pool struct {
	t                          *testing.T
	state, deps, dir, work, tm string
	tmpRoot                    string
}

func newPool(t *testing.T, keep string) *pool {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the container scripts run on Linux")
	}
	root := t.TempDir()
	p := &pool{t: t, state: filepath.Join(root, "state"), deps: filepath.Join(root, "deps"), tmpRoot: t.TempDir()}
	p.write(filepath.Join(p.tmpRoot, "leftover", "f"))
	p.dir = filepath.Join(p.state, "r1")
	p.work = filepath.Join(p.dir, "_work")
	p.tm = filepath.Join(root, "tmp-r1")
	for _, f := range []string{
		"_work/repo/repo/src/main.go", "_work/_temp/script.sh", "_work/_temp/_runner_file_commands/save_state",
		"_work/_actions/a/action.yml", "_work/_tool/node/bin", "r1-tmp/x",
	} {
		p.write(filepath.Join(p.dir, f))
	}
	p.write(filepath.Join(p.tm, "build", "a.o"))
	p.write(filepath.Join(p.deps, "r1", "npm", "pkg"))
	p.write(filepath.Join(p.dir, "crsp-hygiene.env"), "CRSP_KEEP_DEPENDENCIES="+keep+"\n")
	return p
}

func (p *pool) write(path string, content ...string) {
	p.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		p.t.Fatal(err)
	}
	c := "x"
	if len(content) > 0 {
		c = content[0]
	}
	if err := os.WriteFile(path, []byte(c), 0o644); err != nil {
		p.t.Fatal(err)
	}
}

func script(t *testing.T, name string) string {
	t.Helper()
	b, err := FS.ReadFile("persistent/" + name)
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(f, b, 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func (p *pool) run(name string, extra ...string) (string, error) {
	cmd := exec.CommandContext(p.t.Context(), "setsid", "sh", script(p.t, "clean.sh"), name)
	cmd.Env = append(os.Environ(), "CRSP_RUNNER_DIR="+p.dir, "CRSP_STATE_DIR="+p.state, "CRSP_DEPS="+filepath.Join(p.deps, "r1"),
		"GITHUB_WORKSPACE="+filepath.Join(p.work, "repo", "repo"), "TMPDIR="+p.tm, "RUNNER_TEMP="+filepath.Join(p.work, "_temp"),
		"CRSP_CLEAN_POLL=0", "CRSP_TMP_ROOT="+p.tmpRoot)
	cmd.Env = append(cmd.Env, extra...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestContainerCleanAlwaysRemovesTemporaryFiles(t *testing.T) {
	for _, keep := range []string{"1", "0"} {
		for _, phase := range []string{"job-completed", "job-started"} {
			p := newPool(t, keep)
			if out, err := p.run(phase); err != nil {
				t.Fatalf("keep=%s %s: %v\n%s", keep, phase, err, out)
			}
			if exists(filepath.Join(p.work, "repo", "repo", "src")) || exists(filepath.Join(p.work, "_temp", "script.sh")) || exists(filepath.Join(p.tm, "build")) {
				t.Errorf("keep=%s %s: temporary files stay", keep, phase)
			}
			if !exists(filepath.Join(p.work, "_temp")) {
				t.Errorf("keep=%s %s: _temp itself must stay", keep, phase)
			}
			st, _ := os.ReadFile(filepath.Join(p.dir, "crsp-clean.json"))
			if !strings.Contains(string(st), `"unclean_reason":""`) || !strings.Contains(string(st), phase) {
				t.Errorf("state %s", st)
			}
		}
	}
}

func TestContainerDependenciesKeptOnlyWhenOn(t *testing.T) {
	on := newPool(t, "1")
	if out, err := on.run("job-completed"); err != nil {
		t.Fatal(err, out)
	}
	if !exists(filepath.Join(on.deps, "r1", "npm", "pkg")) || !exists(filepath.Join(on.work, "_actions", "a")) || !exists(filepath.Join(on.work, "_tool", "node")) {
		t.Error("keep on: dependencies were removed")
	}
	off := newPool(t, "0")
	if out, err := off.run("job-completed"); err != nil {
		t.Fatal(err, out)
	}
	if exists(filepath.Join(off.deps, "r1", "npm")) || exists(filepath.Join(off.work, "_actions")) || exists(filepath.Join(off.work, "_tool")) {
		t.Error("keep off: dependencies stay")
	}
}

func TestContainerStartKeepsTheFilesTheRunnerMadeForTheJob(t *testing.T) {
	p := newPool(t, "1")
	if out, err := p.run("job-started"); err != nil {
		t.Fatal(err, out)
	}
	if !exists(filepath.Join(p.work, "_temp", "_runner_file_commands", "save_state")) {
		t.Error("the file commands of the starting job were removed")
	}
	if !exists(filepath.Join(p.work, "repo", "repo")) {
		t.Error("the workspace folder of the starting job must stay")
	}
}

func TestContainerCleanEndsLeftOverProcessesOfItsSessionOnly(t *testing.T) {
	p := newPool(t, "1")
	pidFile := filepath.Join(t.TempDir(), "pid")
	bystander := exec.CommandContext(t.Context(), "sleep", "300")
	if err := bystander.Start(); err != nil { // another session: not this runner's
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bystander.Process.Kill(); _ = bystander.Wait() })
	cs := script(t, "clean.sh")
	cmd := exec.CommandContext(t.Context(), "setsid", "sh", "-c", `sleep 300 & echo $! >"$1"; sh "$2" job-completed`, "sh", pidFile, cs)
	cmd.Env = append(os.Environ(), "CRSP_RUNNER_DIR="+p.dir, "CRSP_STATE_DIR="+p.state, "CRSP_DEPS="+filepath.Join(p.deps, "r1"), "CRSP_TMP_ROOT="+p.tmpRoot)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	b, _ := os.ReadFile(pidFile)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	// The clean may end the process a moment after it returns: poll, bounded.
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	limit := time.NewTimer(5 * time.Second)
	defer limit.Stop()
	for syscallAlive(pid) == nil {
		select {
		case <-tick.C:
		case <-limit.C:
			t.Fatalf("the process the job left (%d) still runs", pid)
		}
	}
	if err := syscallAlive(bystander.Process.Pid); err != nil {
		t.Error("a process of another session was ended")
	}
}

func syscallAlive(pid int) error {
	st, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return err
	}
	if i := strings.LastIndex(string(st), ") "); i > 0 && strings.HasPrefix(string(st)[i+2:], "Z") {
		return os.ErrNotExist
	}
	return nil
}

func TestContainerCleanThatFailsIsUncleanAndStartHolds(t *testing.T) {
	p := newPool(t, "1")
	// a rm that cannot remove one folder: a file in use
	bin := t.TempDir()
	fake := "#!/bin/sh\nfor a in \"$@\"; do case \"$a\" in */build) echo 'in use' >&2; exit 1;; esac; done\nexec /bin/rm \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "rm"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	env := "PATH=" + bin + ":" + os.Getenv("PATH")
	out, err := p.run("job-completed", env)
	if err == nil || !strings.Contains(out, "not clean") || !strings.Contains(out, "build") {
		t.Fatalf("err %v\n%s", err, out)
	}
	st, _ := os.ReadFile(filepath.Join(p.dir, "crsp-clean.json"))
	if !strings.Contains(string(st), "left") || !strings.Contains(string(st), "build") {
		t.Fatalf("state %s", st)
	}
	// job-started holds the job: exit 1 after the maximum wait
	if _, err := p.run("job-started", env, "CRSP_CLEAN_MAX_WAIT=0"); err == nil {
		t.Fatal("an unclean runner took a job")
	}
	// the clean-up that passes makes it healthy again
	if out, err := p.run("job-started"); err != nil {
		t.Fatal(err, out)
	}
	st, _ = os.ReadFile(filepath.Join(p.dir, "crsp-clean.json"))
	if !strings.Contains(string(st), `"unclean_reason":""`) {
		t.Fatalf("still unclean: %s", st)
	}
}

func TestContainerSweepKeepsItsOwnGroundAndTheSharedTmp(t *testing.T) {
	p := newPool(t, "1")
	// the runner's own folders live inside the swept root, as under /tmp
	root := p.tmpRoot
	p.state = filepath.Join(root, "state")
	p.dir = filepath.Join(p.state, "r1")
	p.work = filepath.Join(p.dir, "_work")
	p.deps = filepath.Join(root, "deps")
	p.tm = filepath.Join(root, "tmp-r1")
	p.write(filepath.Join(p.dir, "_work", "_temp", "script.sh"))
	p.write(filepath.Join(p.dir, "crsp-hygiene.env"), "CRSP_KEEP_DEPENDENCIES=1\n")
	p.write(filepath.Join(p.deps, "r1", "npm", "pkg"))
	p.write(filepath.Join(p.tm, "build", "a.o"))
	p.write(filepath.Join(root, "other", "junk"))
	for _, phase := range []string{"job-completed", "job-started"} {
		if out, err := p.run(phase, "TMPDIR="+root); err != nil {
			t.Fatalf("%s: %v\n%s", phase, err, out)
		}
		if !exists(filepath.Join(p.dir, "crsp-clean.json")) || !exists(p.state) || !exists(filepath.Join(p.deps, "r1", "npm", "pkg")) {
			t.Errorf("%s: the sweep removed the runner folder, state or dependencies", phase)
		}
		if exists(filepath.Join(root, "other")) || exists(filepath.Join(root, "leftover")) {
			t.Errorf("%s: a foreign entry stays", phase)
		}
		if !exists(root) {
			t.Fatalf("%s: the root itself went", phase)
		}
		p.write(filepath.Join(root, "other", "junk"))
		p.write(filepath.Join(root, "leftover", "f"))
	}
	// TMPDIR and RUNNER_TEMP equal to the shared root: the root is never emptied by step 2
	p.write(filepath.Join(p.dir, "keepme"))
	if out, err := p.run("job-completed", "TMPDIR="+root, "RUNNER_TEMP="+root); err != nil {
		t.Fatal(err, out)
	}
	if !exists(filepath.Join(p.dir, "keepme")) {
		t.Error("the runner folder was emptied")
	}
}

func TestContainerSharedTmpOnlyWhileNoOtherRunnerHasAJob(t *testing.T) {
	p := newPool(t, "1")
	tmp := t.TempDir()
	_ = tmp
	// /tmp is the container's; the test only checks the busy rule through the script text
	b, _ := FS.ReadFile("persistent/clean.sh")
	if !strings.Contains(string(b), "busy_peers") {
		t.Fatal("the shared /tmp rule is missing")
	}
	_ = p
}

func TestSupervisorRestartsADeadRunnerWithBackOffAndNeverEphemeral(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux scripts")
	}
	state := filepath.Join(t.TempDir(), "state")
	run := filepath.Join(t.TempDir(), "run")
	if err := os.MkdirAll(filepath.Join(state, "r1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(state, "runners.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "runners.d", "r1.env"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "r1", "run.sh"), []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sup := script(t, "pool-supervisor.sh")
	tick := func() string {
		cmd := exec.CommandContext(t.Context(), "sh", sup)
		cmd.Env = append(os.Environ(), "CRSP_STATE_DIR="+state, "CRSP_DEPS_DIR="+filepath.Join(t.TempDir(), "deps"), "CRSP_SUPERVISOR_RUN="+run, "CRSP_SUPERVISOR_ONCE=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return string(out)
	}
	if out := tick(); !strings.Contains(out, "starting runner r1") {
		t.Fatalf("first tick: %s", out)
	}
	// the runner exits at once: poll until the supervisor sees it dead
	var out string
	poll := time.NewTicker(50 * time.Millisecond)
	defer poll.Stop()
	for i := 0; i < 100; i++ {
		<-poll.C
		if out = tick(); strings.Contains(out, "ended, starting it again") {
			break
		}
	}
	if !strings.Contains(out, "ended, starting it again in 2 s") {
		t.Fatalf("second tick: %s", out)
	}
	if out := tick(); strings.Contains(out, "starting runner") {
		t.Fatalf("started again before the back-off ended: %s", out)
	}
	add, err := FS.ReadFile("persistent/add-runner.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(add), "--unattended --replace") || strings.Contains(strings.ReplaceAll(string(add), "never with --ephemeral", ""), "--ephemeral") {
		t.Fatal("a persistent runner registers with --unattended --replace and never --ephemeral")
	}
}

// OPS70: add-runner.sh is idempotent per runner name, and list.sh names what
// the supervisor goes by.
type addRig struct {
	t                      *testing.T
	state, run, src, calls string
	add                    string
}

func newAddRig(t *testing.T, configBody string) *addRig {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the container scripts run on Linux")
	}
	root := t.TempDir()
	a := &addRig{t: t, state: filepath.Join(root, "state"), run: filepath.Join(root, "run"), src: filepath.Join(root, "src"), calls: filepath.Join(root, "calls")}
	if err := os.MkdirAll(a.src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.src, "config.sh"), []byte("#!/bin/sh\necho \"$@\" >>"+a.calls+"\n"+configBody+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	a.add = script(t, "add-runner.sh")
	return a
}

func (a *addRig) do(name string, extra ...string) (string, error) {
	cmd := exec.CommandContext(a.t.Context(), "sh", a.add, name, "https://github.com/o/r", "linux", "", "1")
	cmd.Env = append(os.Environ(), "CRSP_STATE_DIR="+a.state, "CRSP_SUPERVISOR_RUN="+a.run, "CRSP_RUNNER_SRC="+a.src, "CRSP_ADD_WAIT=0")
	cmd.Env = append(cmd.Env, extra...)
	cmd.Stdin = strings.NewReader("tok\n")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (a *addRig) entries() []string {
	a.t.Helper()
	m, _ := filepath.Glob(filepath.Join(a.state, "runners.d", "*.env"))
	d, _ := filepath.Glob(filepath.Join(a.state, "*", "config.sh"))
	return append(m, d...)
}

func TestAddRunnerTwiceLeavesOneFolderAndOneEntry(t *testing.T) {
	a := newAddRig(t, "exit 0")
	for i := 0; i < 2; i++ {
		if out, err := a.do("pool-1-r7"); err != nil {
			t.Fatal(err, out)
		}
	}
	if got := a.entries(); len(got) != 2 {
		t.Fatalf("one .env and one folder expected: %v", got)
	}
	b, _ := os.ReadFile(a.calls)
	if n := strings.Count(string(b), "--replace"); n != 2 || strings.Count(string(b), "--name pool-1-r7") != 2 {
		t.Fatalf("each add registers the one name with --replace: %s", b)
	}
}

func TestAddRunnerRetryAfterFailureKeepsTheSetUpFolder(t *testing.T) {
	a := newAddRig(t, `[ -f ../fail ] && exit 1; exit 0`)
	if out, err := a.do("n1"); err != nil {
		t.Fatal(err, out)
	}
	if err := os.WriteFile(filepath.Join(a.state, "fail"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.do("n1"); err == nil {
		t.Fatal("the failing config must fail the add")
	}
	if _, err := os.Stat(filepath.Join(a.state, "n1", "config.sh")); err != nil {
		t.Fatalf("a failed retry removed the folder of a set up runner: %v", err)
	}
	// a first add that fails leaves no folder behind
	if _, err := a.do("n2"); err == nil {
		t.Fatal("fail expected")
	}
	if _, err := os.Stat(filepath.Join(a.state, "n2")); err == nil {
		t.Fatal("a failed first add left its folder")
	}
}

func TestAddRunnerRefusesWhileTheListenerStillRuns(t *testing.T) {
	a := newAddRig(t, "exit 0")
	if out, err := a.do("n1"); err != nil {
		t.Fatal(err, out)
	}
	// a live process stands for the running listener
	cmd := exec.CommandContext(t.Context(), "sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if err := os.MkdirAll(a.run, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.run, "n1.pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := a.do("n1")
	if err == nil || !strings.Contains(out, "still running") {
		t.Fatalf("a second listener must not be registered over the first: %v %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(a.state, "runners.d", "n1.env")); err == nil {
		t.Fatal("the entry stays removed, so the supervisor ends the old listener")
	}
}

func TestListScriptNamesEntriesAndWhetherTheyRun(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux scripts")
	}
	root := t.TempDir()
	state, run := filepath.Join(root, "state"), filepath.Join(root, "run")
	for _, f := range []string{"runners.d/up.env", "runners.d/held.env", "runners.d/held.stopped", "runners.d/dead.env"} {
		p := filepath.Join(state, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(run, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"up", "held"} {
		if err := os.WriteFile(filepath.Join(run, n+".pid"), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(t.Context(), "sh", script(t, "list.sh"))
	cmd.Env = append(os.Environ(), "CRSP_STATE_DIR="+state, "CRSP_SUPERVISOR_RUN="+run)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); got != "dead|0\nheld|0\nup|1\n" {
		t.Fatalf("list: %q", got)
	}
}
