package checks

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/container"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
)

// fakeEngine is the container engine of the tests (CRSP49 interface).
type fakeEngine struct {
	status container.Status
	derr   error
	// answers map a substring of the docker args to the result.
	answers []answer
	calls   []string
}

type answer struct {
	match string
	res   sys.Result
	err   error
}

func (f *fakeEngine) Detect(context.Context) (container.Status, error) { return f.status, f.derr }
func (f *fakeEngine) Command(args ...string) (sys.Command, error) {
	return sys.Command{Name: "docker", Args: args}, nil
}
func (f *fakeEngine) Run(_ context.Context, args ...string) (sys.Result, error) {
	text := strings.Join(args, " ")
	f.calls = append(f.calls, text)
	for _, a := range f.answers {
		if strings.Contains(text, a.match) {
			return a.res, a.err
		}
	}
	return sys.Result{}, nil
}

type fakeSource struct {
	pools []ContainerPool
	eng   *fakeEngine
	err   error // Engine error
}

func (s *fakeSource) Pools(context.Context) ([]ContainerPool, error) { return s.pools, nil }
func (s *fakeSource) Engine(context.Context, string) (container.Engine, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.eng, nil
}

func usable() container.Status {
	return container.Status{Present: true, Usable: true, Route: container.RouteNative, Version: "27.3.1"}
}

func pool() ContainerPool {
	return ContainerPool{Name: "linux", MachineID: "m1", MachineName: "box", Image: "crsp/linux:1", ImageKey: "key-1",
		Layers: []string{"llvm"}, CacheDir: "/var/crsp/cache/1/lane-1/tools", JobUser: "1001:1001", DeclaredRunners: 2, RecordedRunners: 2}
}

func runContainer(t *testing.T, src ContainerSource, prefix string) []doctor.Finding {
	t.Helper()
	reg := doctor.NewRegistry()
	RegisterContainer(reg, src)
	e := &doctor.Engine{Registry: reg, Store: doctor.NewMemStore(), Now: func() time.Time { return now },
		Env: func(context.Context) (doctor.Env, error) { return doctor.Env{}, nil }}
	r, err := e.Run(context.Background(), doctor.Scope{Checks: []string{prefix}}, doctor.TriggerCLI, "test")
	if err != nil {
		t.Fatal(err)
	}
	return r.Findings
}

func TestEngineCheck(t *testing.T) {
	src := &fakeSource{pools: []ContainerPool{pool(), pool()}, eng: &fakeEngine{status: usable()}}
	fs := runContainer(t, src, "container.engine")
	if len(fs) != 1 || fs[0].Severity != doctor.OK { // two pools, one machine, one finding
		t.Fatalf("%+v", fs)
	}
	src.eng = &fakeEngine{status: container.Status{Present: true, Reason: container.ReasonEngineDown}}
	if fs := runContainer(t, src, "container.engine"); fs[0].Severity != doctor.Fail || !strings.Contains(fs[0].Detail, "does not answer") {
		t.Fatalf("down: %+v", fs)
	}
	src.eng = &fakeEngine{derr: errors.New("boom")}
	if fs := runContainer(t, src, "container.engine"); fs[0].Severity != doctor.Warn || !strings.Contains(fs[0].Title, "cannot probe") {
		t.Fatalf("unreadable engine is a warning: %+v", fs)
	}
	src.err = doctor.ErrUnreachable
	if fs := runContainer(t, src, "container.engine"); fs[0].Severity != doctor.Skip {
		t.Fatalf("offline agent: %+v", fs)
	}
}

func TestImageCurrent(t *testing.T) {
	eng := &fakeEngine{status: usable(), answers: []answer{{match: "image inspect", res: sys.Result{Stdout: "key-1\n"}}}}
	src := &fakeSource{pools: []ContainerPool{pool()}, eng: eng}
	if fs := runContainer(t, src, "container.image"); fs[0].Severity != doctor.OK {
		t.Fatalf("current: %+v", fs)
	}
	if !strings.Contains(eng.calls[0], container.ImageKeyLabel) || !strings.Contains(eng.calls[0], "crsp/linux:1") {
		t.Errorf("call %s", eng.calls[0])
	}
	eng.answers = []answer{{match: "image inspect", res: sys.Result{Stdout: "old-key\n"}}}
	if fs := runContainer(t, src, "container.image"); fs[0].Severity != doctor.Fail || !strings.Contains(fs[0].Title, "out of date") || fs[0].Fix.Command == "" {
		t.Fatalf("stale: %+v", fs)
	}
	eng.answers = []answer{{match: "image inspect", res: sys.Result{ExitCode: 1, Stderr: "Error: No such image: crsp/linux:1"}}}
	if fs := runContainer(t, src, "container.image"); fs[0].Severity != doctor.Fail || !strings.Contains(fs[0].Title, "not built") {
		t.Fatalf("missing: %+v", fs)
	}
	eng.answers = []answer{{match: "image inspect", res: sys.Result{ExitCode: 1, Stderr: "Cannot connect to the Docker daemon"}}}
	if fs := runContainer(t, src, "container.image"); fs[0].Severity != doctor.Warn {
		t.Fatalf("engine error is cannot probe: %+v", fs)
	}
	eng.answers = []answer{{match: "image inspect", err: context.DeadlineExceeded}}
	if fs := runContainer(t, src, "container.image"); fs[0].Severity != doctor.Warn {
		t.Fatalf("timeout is cannot probe: %+v", fs)
	}
}

func TestToolProbeFailVsCannotProbe(t *testing.T) {
	eng := &fakeEngine{status: usable()}
	src := &fakeSource{pools: []ContainerPool{pool()}, eng: eng}
	// base and llvm both found.
	eng.answers = []answer{{match: "run --rm", res: sys.Result{Stdout: "ok git\nok clang-cl\ndone\n"}}}
	fs := runContainer(t, src, "container.layer")
	if len(fs) != 2 {
		t.Fatalf("one finding per layer, base first: %+v", fs)
	}
	for _, f := range fs {
		if f.Severity != doctor.OK {
			t.Fatalf("%+v", f)
		}
	}
	// The probe ran and clang-cl is not there: fail, naming the tool.
	eng.answers = []answer{{match: "clang-cl", res: sys.Result{Stdout: "missing clang-cl\nok lld-link\ndone\n"}}, {match: "run --rm", res: sys.Result{Stdout: "ok git\ndone\n"}}}
	fs = runContainer(t, src, "container.layer")
	var llvm doctor.Finding
	for _, f := range fs {
		if strings.Contains(f.Subject.Name, "llvm") {
			llvm = f
		}
	}
	if llvm.Severity != doctor.Fail || !strings.Contains(llvm.Detail, "clang-cl") || strings.Contains(llvm.Detail, "lld-link") {
		t.Fatalf("missing tool: %+v", llvm)
	}
	// The probe could not run (engine error, no end marker): warn, never fail.
	eng.answers = []answer{{match: "run --rm", res: sys.Result{ExitCode: 125, Stderr: "docker: Error response from daemon"}}}
	for _, f := range runContainer(t, src, "container.layer") {
		if f.Severity != doctor.Warn || !strings.Contains(f.Title, "cannot probe") {
			t.Fatalf("cannot probe: %+v", f)
		}
	}
	eng.answers = []answer{{match: "run --rm", err: context.DeadlineExceeded}}
	for _, f := range runContainer(t, src, "container.layer") {
		if f.Severity != doctor.Warn {
			t.Fatalf("timeout: %+v", f)
		}
	}
	// An unknown layer is a fail of its own.
	p := pool()
	p.Layers = []string{"nope"}
	src.pools = []ContainerPool{p}
	eng.answers = []answer{{match: "run --rm", res: sys.Result{Stdout: "ok git\ndone\n"}}}
	fs = runContainer(t, src, "container.layer")
	var unknown *doctor.Finding
	for i := range fs {
		if strings.Contains(fs[i].Title, "not known") {
			unknown = &fs[i]
		}
	}
	if len(fs) != 2 || unknown == nil || unknown.Severity != doctor.Fail {
		t.Fatalf("unknown layer: %+v", fs)
	}
}

func TestLayerScriptQuotesAndChecksFolders(t *testing.T) {
	s := layerScript([]string{"git", "$PLAYWRIGHT_BROWSERS_PATH", "a'b"})
	for _, want := range []string{"command -v 'git'", `-d "${PLAYWRIGHT_BROWSERS_PATH}"`, `'a'\''b'`, "echo done"} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q: %s", want, s)
		}
	}
	for _, l := range container.Layers {
		if len(l.Tools) == 0 {
			t.Errorf("layer %s has no tools", l.Name)
		}
	}
}

func TestToolCacheWritable(t *testing.T) {
	eng := &fakeEngine{status: usable(), answers: []answer{{match: "run --rm", res: sys.Result{Stdout: "writable\n"}}}}
	src := &fakeSource{pools: []ContainerPool{pool()}, eng: eng}
	if fs := runContainer(t, src, "container.toolcache"); fs[0].Severity != doctor.OK {
		t.Fatalf("writable: %+v", fs)
	}
	call := eng.calls[0]
	for _, want := range []string{"--user 1001:1001", "source=/var/crsp/cache/1/lane-1/tools,target=/ci-tools"} {
		if !strings.Contains(call, want) {
			t.Errorf("call lacks %q: %s", want, call)
		}
	}
	eng.answers = []answer{{match: "run --rm", res: sys.Result{Stdout: "not-writable\n"}}}
	if fs := runContainer(t, src, "container.toolcache"); fs[0].Severity != doctor.Fail || !strings.Contains(fs[0].Detail, "1001:1001") {
		t.Fatalf("not writable: %+v", fs)
	}
	eng.answers = []answer{{match: "run --rm", res: sys.Result{ExitCode: 126, Stderr: "cannot start"}}}
	if fs := runContainer(t, src, "container.toolcache"); fs[0].Severity != doctor.Warn {
		t.Fatalf("cannot probe: %+v", fs)
	}
}

func TestRecordsAgree(t *testing.T) {
	src := &fakeSource{pools: []ContainerPool{pool()}, eng: &fakeEngine{status: usable()}}
	if fs := runContainer(t, src, "container.records"); fs[0].Severity != doctor.OK {
		t.Fatalf("%+v", fs)
	}
	p := pool()
	p.RecordedRunners = 1
	src.pools = []ContainerPool{p}
	if fs := runContainer(t, src, "container.records"); fs[0].Severity != doctor.Warn || !strings.Contains(fs[0].Detail, "2 runners; 1") {
		t.Fatalf("%+v", fs)
	}
}

func TestContainerChecksRespectMachineScope(t *testing.T) {
	src := &fakeSource{pools: []ContainerPool{pool()}, eng: &fakeEngine{status: usable()}}
	reg := doctor.NewRegistry()
	RegisterContainer(reg, src)
	e := &doctor.Engine{Registry: reg, Store: doctor.NewMemStore(), Now: func() time.Time { return now },
		Env: func(context.Context) (doctor.Env, error) { return doctor.Env{}, nil }}
	r, err := e.Run(context.Background(), doctor.Scope{Machines: []string{"other"}, Checks: []string{"container"}}, doctor.TriggerCLI, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Findings) != 0 {
		t.Fatalf("a pool of another machine was checked: %+v", r.Findings)
	}
}

func TestMemoryCapCheck(t *testing.T) {
	eng := &fakeEngine{status: usable(), answers: []answer{{match: "inspect", res: sys.Result{Stdout: "true 137 4294967296\n"}}}}
	src := &fakeSource{pools: []ContainerPool{pool()}, eng: eng}
	fs := runContainer(t, src, "container.memory-cap")
	if fs[0].Severity != doctor.Fail || !strings.Contains(fs[0].Title, "stopped by the memory cap (exit 137)") || !strings.Contains(fs[0].Detail, "4096 MiB") {
		t.Fatalf("killed: %+v", fs)
	}
	eng.answers = []answer{{match: "inspect", res: sys.Result{Stdout: "false 0 4294967296\n"}}}
	if fs := runContainer(t, src, "container.memory-cap"); fs[0].Severity != doctor.OK {
		t.Fatalf("fine: %+v", fs)
	}
	eng.answers = []answer{{match: "inspect", res: sys.Result{ExitCode: 1, Stderr: "boom"}}}
	if fs := runContainer(t, src, "container.memory-cap"); fs[0].Severity != doctor.Warn {
		t.Fatalf("cannot probe: %+v", fs)
	}
}

// CRSP253 (runners R5.131): the warm pool.
func TestWarmPool(t *testing.T) {
	a, b, c := pool(), pool(), pool()
	a.Name, a.WarmWanted, a.WarmReady = "full", 1, 1
	b.Name, b.WarmWanted, b.WarmReady = "cold", 2, 1
	c.Name = "none"
	fs := runContainer(t, &fakeSource{pools: []ContainerPool{a, b, c}, eng: &fakeEngine{status: usable()}}, "container.warm")
	if len(fs) != 2 {
		t.Fatalf("%d findings: %+v", len(fs), fs)
	}
	got := map[string]doctor.Severity{}
	for _, f := range fs {
		got[f.Subject.Name] = f.Severity
	}
	if got["full on box"] != doctor.OK || got["cold on box"] != doctor.Warn {
		t.Fatalf("%v", got)
	}
}
