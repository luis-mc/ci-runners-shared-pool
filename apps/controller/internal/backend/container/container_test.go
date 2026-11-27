package container

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys/sysfake"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/redact"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

const jitSecret = "FAKE-JIT-CONFIG-VALUE-0001"

type rig struct {
	t     *testing.T
	f     *fakeDocker
	b     *Backend
	logs  []string
	learn []string
}

func newRig(t *testing.T, mod ...func(*Config)) *rig {
	t.Helper()
	r := &rig{t: t, f: newFake()}
	cfg := Config{Dockerfile: "images/linux-base/Dockerfile", BuildContext: ".", BaseName: "ubuntu-24.04",
		Limits: Limits{MemoryMB: 8192, CPUs: 4}, Pins: map[string]string{"runner": "2.330.0"}}
	for _, m := range mod {
		m(&cfg)
	}
	red := redact.New()
	d := sys.Deps{Cmd: r.f.Runner(), FS: sysfake.NewFS(), Redact: red.Redact, Secret: func(s string) { r.learn = append(r.learn, s); red.Add(s) },
		LogLine: func(step, msg string) { r.logs = append(r.logs, step+": "+msg) }}
	r.b = New(d, r.f, cfg)
	return r
}

func (r *rig) image(layers ...string) backend.ImageRef {
	r.t.Helper()
	img, err := r.b.PrepareImage(context.Background(), backend.ImageSpec{OS: "linux", Layers: layers})
	if err != nil {
		r.t.Fatal(err)
	}
	return img
}

func (r *rig) spec(name, repo string, img backend.ImageRef) backend.RunnerSpec {
	return backend.RunnerSpec{ID: "r_" + name, Name: name, OS: "linux", Arch: "x64", GitHubURL: "https://github.com/luis-mc/" + repo, Image: img}
}

func (r *rig) start(name, repo string, img backend.ImageRef) backend.Handle {
	r.t.Helper()
	h, err := r.b.Start(context.Background(), r.spec(name, repo, img), backend.StartConfig{JITConfig: jitSecret})
	if err != nil {
		r.t.Fatal(err)
	}
	return h
}

func (r *rig) pool(repo string) *fakeC { return r.f.containers[PoolName("luis-mc", repo)] }

// R5.27: never privileged, no socket, non-root, one capability set, KVM only as
// a device.
func TestNoPrivilegedNoSocketKvmDevice(t *testing.T) {
	r := newRig(t)
	img := r.image()
	r.start("job-1", "demo", img)
	args := strings.Join(r.pool("demo").args, " ")
	for _, bad := range []string{"--privileged", "docker.sock", "podman.sock", " --pid ", "--network", "--cap-add", "--device"} {
		if strings.Contains(args, bad) {
			t.Errorf("run args hold %q: %s", bad, args)
		}
	}
	for _, want := range []string{"--user 1001:1001", "--cap-drop ALL", "--security-opt no-new-privileges", "--read-only", "--tmpfs /home/runner:", "--tmpfs /tmp:", "--memory 8192m", "--cpus 4", "sleep infinity"} {
		if !strings.Contains(args, want) {
			t.Errorf("run args miss %q: %s", want, args)
		}
	}

	// KVM: the cgroup rule and never a device mapping, still not privileged.
	k := newRig(t, func(c *Config) { c.KVM = true })
	k.start("job-1", "demo", k.image())
	kargs := strings.Join(k.pool("demo").args, " ")
	if !strings.Contains(kargs, "--device-cgroup-rule "+KVMCgroupRule) || strings.Contains(kargs, "--privileged") || strings.Contains(kargs, "--device ") {
		t.Errorf("kvm run args: %s", kargs)
	}

	// The guard refuses each way to break the rules.
	base := []string{"run", "-d", "--read-only", "--user", "1001:1001", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "img"}
	if err := CheckRunArgs(base); err != nil {
		t.Fatalf("a good call is refused: %v", err)
	}
	for name, extra := range map[string][]string{
		"privileged":  {"--privileged"},
		"cap-add":     {"--cap-add", "SYS_ADMIN"},
		"host pid":    {"--pid=host"},
		"host net":    {"--network", "host"},
		"host net 2":  {"--net=host"},
		"device":      {"--device", "/dev/sda"},
		"socket":      {"-v", "/var/run/docker.sock:/var/run/docker.sock"},
		"mount":       {"--mount", "type=bind,source=/run/podman/podman.sock,target=/x"},
		"unconfined":  {"--security-opt", "seccomp=unconfined"},
		"own seccomp": {"--security-opt", "seccomp=/tmp/p.json"},
		"rw root":     {"--read-only=false"},
		"userns host": {"--userns=host"},
	} {
		if err := CheckRunArgs(append(append([]string{}, base[:len(base)-1]...), append(extra, "img")...)); !errors.Is(err, ErrUnsafeRun) {
			t.Errorf("%s: not refused (%v)", name, err)
		}
	}
	for name, a := range map[string][]string{
		"root":         {"run", "--read-only", "--user", "0", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "img"},
		"no user":      {"run", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "img"},
		"no drop":      {"run", "--read-only", "--user", "1001", "--security-opt", "no-new-privileges", "img"},
		"new privs":    {"run", "--read-only", "--user", "1001", "--cap-drop", "ALL", "img"},
		"root:group":   {"run", "--read-only", "--user", "root:root", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "img"},
		"no read-only": {"run", "--user", "1001", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "img"},
	} {
		if err := CheckRunArgs(a); !errors.Is(err, ErrUnsafeRun) {
			t.Errorf("%s: not refused (%v)", name, err)
		}
	}
	// A socket as the crsp binary path is refused before any run.
	s := newRig(t, func(c *Config) { c.CrspHostPath = "/var/run/docker.sock" })
	_, err := s.b.Start(context.Background(), s.spec("job-1", "demo", s.image()), backend.StartConfig{JITConfig: jitSecret})
	if err == nil || s.f.count("run") != 0 {
		t.Fatalf("a socket mount must stop the start: %v", err)
	}
}

// R5.27: one pool container per repository.
func TestOnePoolContainerPerRepository(t *testing.T) {
	r := newRig(t)
	img := r.image()
	r.start("job-1", "demo", img)
	r.start("job-2", "demo", img)
	r.start("job-3", "other", img)
	if r.f.count("run") != 2 || len(r.f.containers) != 2 {
		t.Fatalf("pools: %d runs, %d containers\n%s", r.f.count("run"), len(r.f.containers), r.f.text())
	}
	if PoolName("luis-mc", "demo") == PoolName("luis-mc", "other") || PoolName("a", "b-c") == PoolName("a-b", "c") {
		t.Fatal("two repositories must never share a pool name")
	}
	if PoolName("Luis-MC", "Demo") != PoolName("luis-mc", "demo") {
		t.Fatal("the name must not depend on case")
	}
	// An organization URL is not a repository.
	sp := r.spec("job-4", "demo", img)
	sp.GitHubURL = "https://github.com/luis-mc"
	if _, err := r.b.Start(context.Background(), sp, backend.StartConfig{JITConfig: jitSecret}); err == nil {
		t.Fatal("a pool belongs to one repository")
	}
}

// R5.25, R5.40: the config reaches the runner on standard input; see
// jitstdin_test.go for the stdin-only checks.
func TestJitConfigRedactedAndNotInLogs(t *testing.T) {
	r := newRig(t)
	h := r.start("job-1", "demo", r.image())
	for _, l := range r.logs {
		if strings.Contains(l, jitSecret) {
			t.Fatalf("the JIT config is in a log line: %s", l)
		}
	}
	c := r.pool("demo")
	if len(c.started) != 1 {
		t.Fatalf("one runner started: %v", c.started)
	}
	if !strings.Contains(strings.Join(r.learn, ","), jitSecret) {
		t.Fatal("the redactor must learn the config before it is used")
	}
	_ = h

	// A failing engine's text is redacted, so the config never reaches an error.
	r.f.containers[PoolName("luis-mc", "demo")].running = true
	bad := newRig(t)
	img := bad.image()
	bad.f.failRunTag = img.ID
	_, err := bad.b.Start(context.Background(), bad.spec("job-1", "demo", img), backend.StartConfig{JITConfig: jitSecret})
	if err == nil || strings.Contains(err.Error(), jitSecret) {
		t.Fatalf("error %v", err)
	}
	// A registration token is refused: a container runner is ephemeral.
	if _, err := r.b.Start(context.Background(), r.spec("job-9", "demo", img), backend.StartConfig{Registration: "REG"}); !errors.Is(err, ErrNeedsJIT) {
		t.Fatalf("err = %v", err)
	}
}

// R5.26: Stop and CleanUp leave nothing for the next job.
func TestNothingCarriesOver(t *testing.T) {
	r := newRig(t)
	img := r.image()
	h1 := r.start("job-1", "demo", img)
	c := r.pool("demo")
	if _, ok := c.files[jobDir("job-1")]; !ok {
		t.Fatal("the runner has its own folder")
	}
	ctx := context.Background()
	if err := r.b.Stop(ctx, h1); err != nil {
		t.Fatal(err)
	}
	if err := r.b.CleanUp(ctx, h1); err != nil {
		t.Fatal(err)
	}
	if err := r.b.CleanUp(ctx, h1); err != nil { // twice is safe (R5.10)
		t.Fatalf("second clean up: %v", err)
	}
	if len(c.files) != 0 {
		t.Fatalf("files left: %v", c.files)
	}
	// The next job gets its own folder, copied from the image: nothing of job 1.
	r.start("job-2", "demo", img)
	if _, ok := c.files[jobDir("job-1")]; ok {
		t.Fatal("job 1's folder came back")
	}
	if !strings.Contains(r.f.text(), RunnerDir) {
		t.Fatal("the new runner is copied from the image")
	}
	// The container is gone: clean up still succeeds.
	delete(r.f.containers, PoolName("luis-mc", "demo"))
	if err := r.b.CleanUp(ctx, backend.Handle{Ref: PoolName("luis-mc", "demo") + ":job-2"}); err != nil {
		t.Fatalf("clean up with no container: %v", err)
	}
	// A start that fails halfway cleans up what it made, and the error is redacted.
	f := newRig(t)
	fimg := f.image()
	f.start("ok-1", "demo", fimg)
	f.f.failStart = "exec failed, config " + jitSecret
	_, err := f.b.Start(ctx, f.spec("bad-1", "demo", fimg), backend.StartConfig{JITConfig: jitSecret})
	if err == nil || strings.Contains(err.Error(), jitSecret) {
		t.Fatalf("err = %v", err)
	}
	fc := f.pool("demo")
	if _, ok := fc.files[jobDir("bad-1")]; ok {
		t.Fatal("a failed start left its folder")
	}
	if _, ok := fc.files[jobDir("ok-1")]; !ok {
		t.Fatal("a failed start removed another runner's folder")
	}
}

// R5.25: Start refuses what a container cannot run.
func TestStartRefusals(t *testing.T) {
	r := newRig(t)
	img := r.image()
	ctx := context.Background()
	cfg := backend.StartConfig{JITConfig: jitSecret}
	for name, mod := range map[string]func(*backend.RunnerSpec){
		"windows":  func(s *backend.RunnerSpec) { s.OS = "windows" },
		"bad name": func(s *backend.RunnerSpec) { s.Name = "a b; rm -rf /" },
		"no repo":  func(s *backend.RunnerSpec) { s.GitHubURL = "https://github.com/luis-mc" },
	} {
		s := r.spec("job-1", "demo", img)
		mod(&s)
		if _, err := r.b.Start(ctx, s, cfg); err == nil {
			t.Errorf("%s: not refused", name)
		}
	}
	if r.f.count("run") != 0 || r.f.count("exec") != 0 {
		t.Fatalf("a refused start touched the engine:\n%s", r.f.text())
	}
}

func TestDetectReportsTheEngine(t *testing.T) {
	r := newRig(t)
	s, err := r.b.Detect(context.Background())
	if err != nil || !s.Usable || s.Name != backend.Docker || s.Isolation != backend.IsolationContainer || len(s.OS) != 1 || s.OS[0] != "linux" {
		t.Fatalf("%+v %v", s, err)
	}
}

// R5.33: one build per key; R5.32: layers listed with the size.
func TestBuildOnceReusesTheImage(t *testing.T) {
	r := newRig(t)
	a := r.image("node", "playwright")
	b := r.image("playwright", "node") // same layers, other order
	if a.ID != b.ID || r.f.count("build") != 1 {
		t.Fatalf("%s %s, %d builds", a.ID, b.ID, r.f.count("build"))
	}
	if a.SizeMB != 900 {
		t.Fatalf("size %d", a.SizeMB)
	}
	c := r.image("node") // other layers: another key, one more build
	if c.ID == a.ID || r.f.count("build") != 2 {
		t.Fatalf("%s, %d builds", c.ID, r.f.count("build"))
	}
	if !strings.Contains(r.f.text(), "--build-arg CRSP_LAYERS=node,playwright") {
		t.Fatalf("layers are not passed to the build:\n%s", r.f.text())
	}
	imgs, err := r.b.ListImages(context.Background())
	if err != nil || len(imgs) != 2 {
		t.Fatalf("%v %v", imgs, err)
	}
	for _, im := range imgs {
		if im.SizeMB != 900 || len(im.Layers) == 0 || im.OS != "linux" {
			t.Errorf("image %+v must show layers and size", im)
		}
	}
	// Another pinned runner version is another image.
	p := newRig(t, func(c *Config) { c.Pins = map[string]string{"runner": "2.331.0"} })
	if p.image("node").ID == c.ID {
		t.Fatal("a new pin must change the key")
	}
}

// R5.33: keep the newest, delete older ones no pool uses.
func TestPruneKeepsNewestAndWhatPoolsUse(t *testing.T) {
	r := newRig(t)
	var ids []string
	for _, l := range []string{"a", "b", "c", "d", "e"} {
		ids = append(ids, r.image(l).ID)
	}
	// A pool runs the oldest image.
	r.start("job-1", "demo", backend.ImageRef{ID: ids[0]})
	gone, err := r.b.PruneImages(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(gone) != 1 || gone[0] != keyOf(ids[1]) {
		t.Fatalf("deleted %v; want only %s (the second oldest)", gone, keyOf(ids[1]))
	}
	if _, ok := r.f.images[ids[0]]; !ok {
		t.Fatal("an image a pool uses was deleted")
	}
}

func drainFor(r *rig, polls *int) runners.Drain {
	clock := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	return runners.Drain{Poll: time.Minute, WarnAfter: time.Hour, Now: func() time.Time { return clock },
		Sleep: func(_ context.Context, d time.Duration) error { clock = clock.Add(d); *polls++; return nil }}
}

// R5.35: build aside, drain, swap. The old image keeps serving until idle.
func TestRebuildDrainsAndKeepsOldOnFailure(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	old := r.image("node")
	r.start("job-1", "demo", old)
	oldName := PoolName("luis-mc", "demo")
	r.f.workers = 1 // a job runs

	// The job ends after the third idle check; a new runner is refused while draining.
	var sawDraining bool
	r.f.onBusyCheck = func(n int) {
		if n == 3 {
			// Start is refused while the swap waits.
			_, err := r.b.Start(ctx, r.spec("job-2", "demo", old), backend.StartConfig{JITConfig: jitSecret})
			sawDraining = errors.Is(err, ErrDraining)
		}
		if n >= 4 {
			r.f.workers = 0
		}
	}
	polls := 0
	res, err := r.b.Refresh(ctx, "luis-mc", "demo", backend.ImageSpec{OS: "linux", Layers: []string{"node", "playwright"}}, drainFor(r, &polls))
	if err != nil || !res.Swapped {
		t.Fatalf("%+v %v", res, err)
	}
	if !sawDraining {
		t.Fatal("a runner started while the pool waited to swap")
	}
	if polls == 0 {
		t.Fatal("the drain never waited")
	}
	// Order: the build comes before the stop; the stop comes after the last busy check.
	text := r.f.calls
	idx := func(prefix string, from int) int {
		for i := from; i < len(text); i++ {
			if strings.HasPrefix(text[i], prefix) {
				return i
			}
		}
		return -1
	}
	build := idx("docker build", 0)
	stop := idx("docker stop", 0)
	lastBusy := -1
	for i, c := range text {
		if strings.Contains(c, "pgrep") {
			lastBusy = i
		}
	}
	if build < 0 || stop < 0 || build > stop || lastBusy > stop {
		t.Fatalf("order: build %d, last idle check %d, stop %d\n%s", build, lastBusy, stop, r.f.text())
	}
	// Right before the swap the idle check ran again: a pgrep sits between the last poll and the stop.
	if !strings.Contains(text[stop-1], "pgrep") && !strings.Contains(text[stop-1], "inspect") {
		t.Fatalf("the idle check must be the call right before the swap, got %q", text[stop-1])
	}
	nu := r.f.containers[oldName]
	if nu == nil || keyOf(nu.image) != r.b.KeyFor(backend.ImageSpec{Layers: []string{"node", "playwright"}}) || res.From == res.To {
		t.Fatalf("pool is not on the new image: %+v %+v", nu, res)
	}
	// Draining is over: a runner starts again.
	r.start("job-3", "demo", backend.ImageRef{ID: nu.image})

	// The build fails: the old image keeps serving and the message says so.
	r2 := newRig(t)
	o2 := r2.image("node")
	r2.start("job-1", "demo", o2)
	r2.f.failBuild = "RUN apt-get failed"
	res2, err := r2.b.Refresh(ctx, "luis-mc", "demo", backend.ImageSpec{OS: "linux", Layers: []string{"node", "x"}}, drainFor(r2, new(int)))
	if err == nil || !strings.HasPrefix(res2.Message, "the rebuild failed: ") || !strings.Contains(res2.Message, "apt-get") || res2.Swapped {
		t.Fatalf("%+v %v", res2, err)
	}
	if c := r2.f.containers[PoolName("luis-mc", "demo")]; c == nil || !c.running || c.image != o2.ID || r2.f.count("stop") != 0 {
		t.Fatal("the old pool must be untouched when the rebuild fails")
	}
	r2.start("job-2", "demo", o2) // still takes runners
}

// R6.4: a drain that times out never stops the job; a job that starts in the
// last moment stops the swap.
func TestRefreshNeverCutsARunningJob(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	old := r.image("node")
	r.start("job-1", "demo", old)
	r.f.workers = 1
	polls := 0
	dr := drainFor(r, &polls)
	dr.Timeout = 3 * time.Minute
	res, err := r.b.Refresh(ctx, "luis-mc", "demo", backend.ImageSpec{OS: "linux", Layers: []string{"node", "x"}}, dr)
	if !runners.IsDrainTimeout(err) || res.Swapped {
		t.Fatalf("%+v %v", res, err)
	}
	if r.f.count("stop") != 0 || r.f.count("rm") != 0 || !r.f.containers[PoolName("luis-mc", "demo")].running {
		t.Fatalf("a timeout must stop nothing:\n%s", r.f.text())
	}
	// Draining ends with the refresh: runners start again.
	r.start("job-2", "demo", old)

	// The idle answer cannot be read: fail closed, nothing is stopped.
	r.f.inspectFail = "Cannot connect to the Docker daemon"
	if _, err := r.b.Refresh(ctx, "luis-mc", "demo", backend.ImageSpec{OS: "linux", Layers: []string{"node", "y"}}, drainFor(r, new(int))); err == nil || r.f.count("stop") != 0 {
		t.Fatalf("fail closed: %v", err)
	}
	r.f.inspectFail = ""

	// A job starts between the idle poll and the swap: the second check catches it.
	r.f.workers = 0
	calls := 0
	r.f.onBusyCheck = func(int) {
		calls++
		if calls == 2 {
			r.f.workers = 1
		}
	}
	res, err = r.b.Refresh(ctx, "luis-mc", "demo", backend.ImageSpec{OS: "linux", Layers: []string{"node", "z"}}, drainFor(r, new(int)))
	if err == nil || res.Swapped || r.f.count("stop") != 0 {
		t.Fatalf("a job that started at the last moment must stop the swap: %+v %v", res, err)
	}
}

// R5.35: the swap that fails puts the old image back, and says so.
func TestSwapFailureRestoresTheOldImage(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	old := r.image("node")
	r.start("job-1", "demo", old)
	spec := backend.ImageSpec{OS: "linux", Layers: []string{"node", "x"}}
	r.f.failRunTag = ImageTag(r.b.KeyFor(spec))
	res, err := r.b.Refresh(ctx, "luis-mc", "demo", spec, drainFor(r, new(int)))
	if err == nil || res.Swapped || !strings.Contains(res.Message, "the old image runs again") {
		t.Fatalf("%+v %v", res, err)
	}
	if c := r.f.containers[PoolName("luis-mc", "demo")]; c == nil || !c.running || c.image != old.ID {
		t.Fatal("the old image must run again")
	}
	// Already current: nothing to do.
	res, err = r.b.Refresh(ctx, "luis-mc", "demo", backend.ImageSpec{OS: "linux", Layers: []string{"node"}}, drainFor(r, new(int)))
	if err != nil || res.Swapped || !strings.Contains(res.Message, "already") {
		t.Fatalf("%+v %v", res, err)
	}
}

// TestRefreshOfAStoppedPoolDoesNotWait: handover lesson 27. An Exited pool is
// drained at once: the drain is never asked, and the swap runs.
func TestRefreshOfAStoppedPoolDoesNotWait(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	old := r.image("node")
	r.start("job-1", "demo", old)
	r.f.containers[PoolName("luis-mc", "demo")].running = false
	r.f.workers = 1 // would block a running pool; a stopped one has no job
	var asked int
	dr := runners.Drain{
		Idle:    func(context.Context) (bool, string, error) { asked++; return false, "never", nil },
		Timeout: time.Minute,
		Sleep: func(context.Context, time.Duration) error {
			t.Error("slept while waiting on a stopped pool")
			return context.Canceled
		},
	}
	res, err := r.b.Refresh(ctx, "luis-mc", "demo", backend.ImageSpec{OS: "linux", Layers: []string{"node", "stopped"}}, dr)
	if err != nil || !res.Swapped {
		t.Fatalf("%+v %v\n%s", res, err, r.f.text())
	}
	if asked != 0 {
		t.Errorf("the drain was asked %d times", asked)
	}
	if c := r.f.containers[PoolName("luis-mc", "demo")]; c == nil || !c.running || c.image == old.ID {
		t.Errorf("the pool did not start on the new image: %+v", c)
	}
	// Draining flag is not left behind.
	r.start("job-2", "demo", r.image("node", "stopped"))
}

// R5.63: the root is read-only, the work dirs are tmpfs, and the tool cache is a
// tmpfs only when no cache lane mounts it.
func TestReadOnlyRootWorkTmpfs(t *testing.T) {
	spec := PoolSpec{Owner: "o", Repo: "r", ImageTag: "img", ImageKey: "k"}
	a, err := RunArgs(spec)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(a, " ")
	for _, want := range []string{"--read-only", "--tmpfs /home/runner:", "--tmpfs /tmp:", "--tmpfs /var/tmp:", "--tmpfs /opt/hostedtoolcache:"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args miss %q: %s", want, joined)
		}
	}
	for _, bad := range []string{"seccomp", "--network", "--cap-add", "--privileged"} {
		if strings.Contains(joined, bad) {
			t.Errorf("args hold %q: %s", bad, joined)
		}
	}
	lane := runnercache.LaneDirs{Tools: "/h/t", Rust: "/h/r", ToolCache: "/h/tc", Wine: "/h/w", Browsers: "/h/b"}
	spec.Lane = &lane
	a, err = RunArgs(spec)
	if err != nil {
		t.Fatal(err)
	}
	if j := strings.Join(a, " "); strings.Contains(j, "--tmpfs /opt/hostedtoolcache") {
		t.Errorf("tool cache is a lane mount and a tmpfs: %s", j)
	}
}

// R5.82: the runners spec names every security option RunArgs sets, so a new
// option without a line in "what a job can reach" fails here.
func TestJobReachListMatchesFlags(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "docs", "features", "runners", "spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	spec := string(raw)
	i := strings.Index(spec, "**R5.81**")
	if i < 0 {
		t.Fatal("the spec has no R5.81")
	}
	section := spec[i:]
	a, err := RunArgs(PoolSpec{Owner: "o", Repo: "r", ImageTag: "img", ImageKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range a {
		if strings.HasPrefix(f, "--") && (strings.Contains(f, "cap") || strings.Contains(f, "security") || f == "--read-only" || f == "--privileged" || f == "--network") {
			if !strings.Contains(section, f) {
				t.Errorf("the spec section does not name %s", f)
			}
		}
	}
	for _, d := range []string{WorkTmpfsHome, WorkTmpfsTmp, WorkTmpfsVarTmp, WorkTmpfsToolCache, "no-new-privileges", "seccomp"} {
		if !strings.Contains(section, d) {
			t.Errorf("the spec section does not name %s", d)
		}
	}
}
