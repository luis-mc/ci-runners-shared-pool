package container

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/redact"
)

// lazyRig is a backend over the real Docker engine type that nobody primed
// with Detect, the way the agent builds it (runners R5.27, R5.47).
func lazyRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	eng := &Docker{Cmd: r.f.Runner(), GOOS: "linux"}
	red := redact.New()
	d := sys.Deps{Cmd: r.f.Runner(), Redact: red.Redact, Secret: func(s string) { red.Add(s) }, LogLine: func(string, string) {}}
	r.b = New(d, eng, r.b.cfg)
	return r
}

func TestStartAndCleanUpWorkWithNoPriorDetect(t *testing.T) { // runners R5.47
	r := lazyRig(t)
	img := r.image()
	h := r.start("job-1", "demo", img)
	if err := r.b.CleanUp(ctx, h); err != nil {
		t.Fatalf("clean up: %v", err)
	}
	if r.f.count("version") == 0 {
		t.Fatal("the engine was never looked for")
	}
}

func TestPersistentWorksWithNoPriorDetect(t *testing.T) { // runners R5.47, R5.90
	r := lazyRig(t)
	r.f.failRunTag = "none" // the fake fails a run whose image it cannot read
	spec := backend.RunnerSpec{ID: "r_p1", Name: "p1", OS: "linux", Arch: "x64", GitHubURL: "https://github.com/luis-mc/demo", Image: r.image()}
	h, err := r.b.StartPersistent(ctx, spec, backend.StartConfig{Registration: "reg-tok"})
	if err != nil {
		t.Fatalf("start persistent: %v", err)
	}
	if err := r.b.CleanUp(ctx, h); err != nil {
		t.Fatalf("clean up: %v", err)
	}
}

func TestEngineThatComesUpLaterIsFound(t *testing.T) { // runners R5.47
	r := lazyRig(t)
	r.f.engineDown = true
	_, err := r.b.eng.Command("ps")
	var ne *NoEngineError
	if !errors.Is(err, ErrNoEngine) || !errors.As(err, &ne) || !strings.Contains(err.Error(), ReasonEngineDown) {
		t.Fatalf("the error names the cause: %v", err)
	}
	if strings.Contains(err.Error(), "no usable container engine") {
		t.Fatalf("the cause replaces the generic words: %v", err)
	}
	r.f.engineDown = false
	if _, err := r.b.eng.Command("ps"); err != nil {
		t.Fatalf("the engine started: %v", err)
	}
}

func TestCleanUpWithNoEngineHasNothingToClean(t *testing.T) { // runners R5.10
	r := lazyRig(t)
	r.f.engineDown = true
	for _, ref := range []string{PersistentPool + ":p1", PoolName("luis-mc", "demo") + ":job-1"} {
		if err := r.b.CleanUp(ctx, backend.Handle{Ref: ref}); err != nil {
			t.Fatalf("%s: %v", ref, err)
		}
	}
}

func TestStartWithNoEngineSaysTheReason(t *testing.T) { // runners R5.47
	r := lazyRig(t)
	r.f.engineDown = true
	_, err := r.b.Start(ctx, r.spec("job-1", "demo", backend.ImageRef{ID: "crsp-pool:k"}), backend.StartConfig{JITConfig: jitSecret})
	if err == nil || !strings.Contains(err.Error(), ReasonEngineDown) {
		t.Fatalf("want the engine reason, got %v", err)
	}
}

func TestConcurrentCommandsDetectWithoutARace(t *testing.T) {
	r := lazyRig(t)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.b.eng.Run(ctx, "ps"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}
