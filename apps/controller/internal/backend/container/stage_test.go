package container

import (
	"context"
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/images"
)

func stagedRig(t *testing.T) (*rig, string) {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(t.TempDir(), "crsp-bin")
	if err := os.WriteFile(bin, elfHeader(nativeMachine()), 0o700); err != nil {
		t.Fatal(err)
	}
	return newRig(t, func(c *Config) {
		c.Dockerfile, c.BuildContext = "", ""
		c.Assets, c.BuildRoot, c.CrspHostPath = images.FS, root, bin
	}), root
}

// R5.33, question 36: with no folder installed, the build uses the files in
// the binary, and the build folder is gone after the build.
func TestBuildUsesEmbeddedFiles(t *testing.T) {
	r, root := stagedRig(t)
	img := r.image("node")
	if img.ID == "" {
		t.Fatal("no image")
	}
	out := r.f.text()
	key := keyOf(img.ID)
	ctxDir := filepath.Join(root, key, "linux-base")
	if !strings.Contains(out, "-f "+filepath.Join(ctxDir, "Dockerfile")+" "+ctxDir) {
		t.Fatalf("the build does not use the staged folder:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, key)); !os.IsNotExist(err) {
		t.Fatalf("the build folder is still there: %v", err)
	}
}

func TestStageWritesTreeAndBinary(t *testing.T) {
	r, root := stagedRig(t)
	dir, err := r.b.stageBuild(context.Background(), "abc123", runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(root, "abc123") {
		t.Fatalf("dir %s", dir)
	}
	for _, p := range []string{"linux-base/Dockerfile", "layers/browsers/Dockerfile", "layers/kvm/kvm-wait.sh", "linux-base/crsp"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	got, err := os.ReadFile(filepath.Join(dir, "linux-base", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := images.FS.ReadFile("linux-base/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Error("staged Dockerfile differs from the embedded one")
	}
	// A second stage replaces the folder.
	if _, err := r.b.stageBuild(context.Background(), "abc123", runtime.GOARCH); err != nil {
		t.Fatal(err)
	}
}

func TestStageRefusesBadKey(t *testing.T) {
	r, _ := stagedRig(t)
	for _, k := range []string{"", "../x", "a/b"} {
		if _, err := r.b.stageBuild(context.Background(), k, runtime.GOARCH); err == nil {
			t.Errorf("key %q was accepted", k)
		}
	}
}

// The key depends on base, layers and pins only, so it is the same whether the
// files come from a folder or from the binary.
func TestKeyUnchangedByAssetSource(t *testing.T) {
	staged, _ := stagedRig(t)
	plain := newRig(t)
	if a, b := staged.b.KeyFor(backend.ImageSpec{Layers: []string{"node"}}), plain.b.KeyFor(backend.ImageSpec{Layers: []string{"node"}}); a != b {
		t.Fatalf("keys differ: %s %s", a, b)
	}
}

func TestDefaultConfigEmbedsImagesAndNeedsNoInstalledFolder(t *testing.T) {
	c := DefaultConfig()
	if c.Assets == nil || c.BuildRoot == "" || c.Dockerfile != "" || c.BuildContext != "" {
		t.Fatalf("%+v", c)
	}
	if strings.Contains(c.BuildRoot, "/usr/share/crsp") {
		t.Fatalf("build root %s", c.BuildRoot)
	}
}

// Question 33: the pool size of the machine is the cap of the pool container.
func TestPoolSizeBecomesPoolLimits(t *testing.T) {
	r := newRig(t)
	img := r.image()
	spec := r.spec("crsp-job-1", "repo-a", img)
	spec.PoolMemoryMB, spec.PoolCPUs = 6144, 2.5
	if _, err := r.b.Start(t.Context(), spec, backend.StartConfig{JITConfig: "x"}); err != nil {
		t.Fatal(err)
	}
	out := r.f.text()
	if !strings.Contains(out, "--memory 6144m") || !strings.Contains(out, "--cpus 2.5") {
		t.Fatalf("pool size is not the limit:\n%s", out)
	}
}

func TestLimitsFallBackToConfig(t *testing.T) {
	r := newRig(t)
	l := r.b.limitsFor(backend.RunnerSpec{})
	if l.MemoryMB != 8192 || l.CPUs != 4 {
		t.Fatalf("%+v", l)
	}
}

func TestRunArgsGuardPassesLimits(t *testing.T) {
	a, err := RunArgs(PoolSpec{Owner: "o", Repo: "r", ImageTag: "crsp-pool:k", Limits: Limits{MemoryMB: 2048, CPUs: 1.5}})
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckRunArgs(a); err != nil {
		t.Fatal(err)
	}
	j := strings.Join(a, " ")
	if !strings.Contains(j, "--memory 2048m") || !strings.Contains(j, "--cpus 1.5") {
		t.Fatal(j)
	}
}

func TestDefaultConfigPinsActionsRunnerInKey(t *testing.T) {
	c := DefaultConfig()
	if v := c.Pins["actions-runner"]; v == "" {
		t.Fatalf("no runner pin: %+v", c.Pins)
	}
	with := runners.ImageKey(runners.ImageKeyInput{Base: c.BaseName, Pins: c.Pins})
	other := runners.ImageKey(runners.ImageKeyInput{Base: c.BaseName, Pins: map[string]string{"actions-runner": "0.0.1:x"}})
	if with == other {
		t.Fatal("a new pin must change the image key")
	}
}

// elfHeader is the smallest file debug/elf reads: a 64-bit little-endian
// executable header for a machine type.
func elfHeader(m elf.Machine) []byte {
	h := make([]byte, 64)
	copy(h, "\x7fELF")
	h[4], h[5], h[6] = byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB), byte(elf.EV_CURRENT)
	binary.LittleEndian.PutUint16(h[16:], uint16(elf.ET_EXEC))
	binary.LittleEndian.PutUint16(h[18:], uint16(m))
	binary.LittleEndian.PutUint32(h[20:], uint32(elf.EV_CURRENT))
	binary.LittleEndian.PutUint16(h[52:], 64)
	return h
}

// nativeMachine is the ELF machine type of this host's architecture, so a staged
// build on any CI runner (amd64 or arm64) finds a binary of its own arch.
func nativeMachine() elf.Machine {
	if runtime.GOARCH == "arm64" {
		return elf.EM_AARCH64
	}
	return elf.EM_X86_64
}
