package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/hygiene"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
)

type fixedRunners []CacheRunner

func (f fixedRunners) Runners(context.Context) ([]CacheRunner, error) { return f, nil }

// R5.90, R5.91: the agent reads the clean-up state and the size of the runner's
// own dependency folder; a runner whose hooks do not clean is left out.
func TestCleanStatusesReadStateAndDependencySize(t *testing.T) {
	root := t.TempDir()
	clean := filepath.Join(root, "clean")
	dirty := filepath.Join(root, "dirty")
	foreign := filepath.Join(root, "foreign")
	for _, d := range []string{clean, dirty, foreign} {
		if err := os.MkdirAll(filepath.Join(d, "cache", "npm"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(clean, "cache", "npm", "pkg"), make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{clean, dirty} {
		if err := hygiene.WriteKeep(sysFS{}, d, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := hygiene.WriteState(sysFS{}, dirty, hygiene.State{UncleanReason: "left /tmp/x", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	got := CleanStatuses(context.Background(), fixedRunners{{Name: "clean", Folder: clean}, {Name: "dirty", Folder: dirty}, {Name: "foreign", Folder: foreign}}, runnercache.OSFS{})
	if len(got) != 2 {
		t.Fatalf("%d statuses, want 2 (a runner without our hooks is left out)", len(got))
	}
	if got[0].GetRunnerName() != "clean" || got[0].GetDependencyCacheBytes() != 2048 || got[0].GetUncleanReason() != "" {
		t.Fatalf("clean: %v", got[0])
	}
	if got[1].GetUncleanReason() != "left /tmp/x" {
		t.Fatalf("dirty: %v", got[1])
	}
}
