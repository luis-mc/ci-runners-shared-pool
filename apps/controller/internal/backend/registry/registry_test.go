package registry

import (
	"runtime"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/linux"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/macos"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/windows"
)

// R5.8 and R5.24: every persistent backend implements the interface, and the
// registry of a binary holds only the backend of its OS.
func TestEveryBackendImplementsInterface(t *testing.T) {
	d := sys.Deps{}
	for name, b := range map[string]backend.Backend{
		"windows": windows.New(d, windows.Config{}),
		"linux":   linux.New(d, linux.Config{}),
		"macos":   macos.New(d, macos.Config{}),
	} {
		if b.Name() != backend.Persistent {
			t.Errorf("%s: name %s", name, b.Name())
		}
		if _, ok := b.(backend.Finder); !ok {
			t.Errorf("%s: no Finder", name)
		}
		if _, ok := b.(backend.Deregisterer); !ok {
			t.Errorf("%s: no Deregisterer", name)
		}
	}
}

func TestRegistryHoldsOnlyThisOS(t *testing.T) {
	reg := Platform(sys.Deps{})
	// Linux and Windows also carry the container backend (runners R5.27, R5.47,
	// Linux containers through the Windows docker command); macOS does not.
	want := map[string]int{"windows": 2, "linux": 2, "darwin": 1}[runtime.GOOS]
	if len(reg.All()) != want {
		t.Fatalf("GOOS=%s: %d backends, want %d", runtime.GOOS, len(reg.All()), want)
	}
	if want > 0 {
		if _, ok := reg.Get(backend.Persistent); !ok {
			t.Fatal("no persistent backend")
		}
	}
	_, hasDocker := reg.Get(backend.Docker)
	if hasDocker != (runtime.GOOS == "linux" || runtime.GOOS == "windows") {
		t.Fatalf("GOOS=%s: container backend present is %v", runtime.GOOS, hasDocker)
	}
}
