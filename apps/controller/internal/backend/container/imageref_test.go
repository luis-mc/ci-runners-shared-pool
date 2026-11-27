package container

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
)

func TestValidateImageRef(t *testing.T) {
	d := "sha256:" + strings.Repeat("a", 64)
	good := []string{"crsp-pool:abc123", "192.168.1.169:8460/crsp/std/ubuntu-24.04:v1", "192.168.1.169:8460/crsp/std/base@" + d,
		"192.168.1.169:8460/crsp/std/base:v1@" + d, "name@" + d, "localhost/x:1", "ubuntu"}
	for _, g := range good {
		if err := ValidateImageRef(g); err != nil {
			t.Errorf("%q refused: %v", g, err)
		}
	}
	bad := []string{"", "https://192.168.1.169:8460/crsp/x:1", "Crsp-Pool:abc", "crsp-pool::abc", "crsp-pool:", `C:\img\x`, "crsp pool:abc",
		"host:8460/Crsp/x:1", "host:99999999/x:1", "name@sha256:zz", "name@" + d + ":tag", "/x:1"}
	for _, b := range bad {
		err := ValidateImageRef(b)
		if !errors.Is(err, ErrBadImageRef) {
			t.Errorf("%q accepted or wrong error: %v", b, err)
		} else if !strings.Contains(err.Error(), fmt.Sprintf("%q", b)) {
			t.Errorf("error does not name the value %q: %v", b, err)
		}
	}
}

func TestRunArgsRefuseABadImage(t *testing.T) {
	for _, tag := range []string{"", "https://x/y:1", "Bad:1"} {
		if _, err := PersistentRunArgs(PersistentSpec{ImageTag: tag}); !errors.Is(err, ErrBadImageRef) {
			t.Errorf("persistent %q: %v", tag, err)
		}
		if _, err := RunArgs(PoolSpec{Owner: "o", Repo: "r", ImageTag: tag}); !errors.Is(err, ErrBadImageRef) {
			t.Errorf("pool %q: %v", tag, err)
		}
	}
	reg := "192.168.1.169:8460/crsp/std/base:v1"
	if _, err := PersistentRunArgs(PersistentSpec{ImageTag: reg}); err != nil {
		t.Errorf("registry host:port ref: %v", err)
	}
	d := "192.168.1.169:8460/crsp/std/base@sha256:" + strings.Repeat("b", 64)
	if _, err := RunArgs(PoolSpec{Owner: "o", Repo: "r", ImageTag: d}); err != nil {
		t.Errorf("digest ref: %v", err)
	}
}

// CRSP204: the owner's case. Windows host, registry configured, persistent
// kind, no image built yet: the spec has no image. It used to send an empty
// image to `docker run ... chown` ("invalid reference format").
func TestPersistentWithNoImageGetsOneThroughEnsureImage(t *testing.T) {
	r := newRig(t, func(c *Config) { c.HostOS = "windows"; c.CrspHostPath = `C:\Program Files\crsp\crsp-linux` })
	r.f.failRunTag = "none"
	spec := backend.RunnerSpec{ID: "r_p1", Name: "p1", OS: "linux", Arch: "x64", Container: true,
		GitHubURL: "https://github.com/luis-mc/demo"}
	if spec.Image.ID != "" {
		t.Fatal("the case has an image")
	}
	// The old tag the plan gives for this spec is empty.
	if plan, err := PlanPool(r.b.cfg, spec, "persistent", "pool", KVMFacts{}); err != nil || plan.Pool.ImageTag != "" {
		t.Fatalf("plan tag %q, %v", plan.Pool.ImageTag, err)
	}
	if _, err := r.b.StartPersistent(ctx, spec, backend.StartConfig{Registration: "reg-tok"}); err != nil {
		t.Fatalf("start persistent: %v", err)
	}
	if r.f.count("build") == 0 {
		t.Error("the image was not made (local, registry, build)")
	}
	c := r.f.containers[PersistentPool]
	if c == nil || !strings.Contains(strings.Join(c.args, " "), "crsp-pool:") {
		t.Fatalf("pool container: %+v", c)
	}
	for _, call := range r.f.calls {
		if strings.HasPrefix(call, "docker run") && strings.Contains(call, "  ") {
			t.Errorf("a call has an empty image: %q", call)
		}
	}
}

func TestPersistentOwnVolumesNeverGetsAnEmptyImage(t *testing.T) {
	r := newRig(t)
	err := r.b.ensurePersistentPool(ctx, backend.RunnerSpec{ID: "r", Name: "p", OS: "linux", Image: backend.ImageRef{ID: "https://x/y"}})
	if !errors.Is(err, ErrBadImageRef) || !strings.Contains(err.Error(), "https://x/y") {
		t.Fatalf("%v", err)
	}
	if r.f.count("run") != 0 {
		t.Errorf("docker was called: %s", r.f.text())
	}
}
