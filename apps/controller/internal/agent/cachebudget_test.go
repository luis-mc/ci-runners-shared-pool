package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
)

// holder is a fake gate: runners named in busy have a job running.
type holder struct {
	busy     map[string]bool
	held     []string
	released []string
}

func (h *holder) HoldRunner(n string) (bool, []machines.JobRef) {
	h.held = append(h.held, n)
	return !h.busy[n], nil
}
func (h *holder) ReleaseRunner(n string) { h.released = append(h.released, n) }

// budgetRunner makes a runner folder with the given .env and Rust cache files
// of the given size and age.
func budgetRunner(t *testing.T, env string, files map[string]int, age time.Duration) string {
	t.Helper()
	f := t.TempDir()
	if err := os.WriteFile(filepath.Join(f, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	d := runnercache.DirsFor(f)
	for rel, n := range files {
		p := filepath.Join(d.Rust, rel)
		put(t, p, n)
		ts := time.Now().Add(-age)
		_ = os.Chtimes(p, ts, ts)
	}
	return f
}

func TestBudgetPassWithoutBudgetOrOverDoesNothing(t *testing.T) {
	f := budgetRunner(t, "CRSP_CACHE_BUDGET_GIB=1\n", map[string]int{"k/a": 100}, time.Hour)
	h := &holder{}
	b := agent.NewBudgeter(agent.Config{Caches: staticRunners{{Name: "r1", Folder: f}}}).WithHolder(h)
	if lines := b.Pass(context.Background()); len(lines) != 0 || len(h.held) != 0 {
		t.Fatalf("under budget must not hold or evict: %v %v", lines, h.held)
	}
	g := budgetRunner(t, "", map[string]int{"k/a": 100}, time.Hour)
	b = agent.NewBudgeter(agent.Config{Caches: staticRunners{{Name: "r1", Folder: g}}}).WithHolder(h)
	if lines := b.Pass(context.Background()); len(lines) != 0 {
		t.Fatalf("a runner without the pool values enforces nothing: %v", lines)
	}
}

func TestBudgetPassAlertsOnceAndKeepsBusyRunner(t *testing.T) {
	// The alert limit is in whole GiB, so a sparse file stands for a big one.
	f := t.TempDir()
	env := "CRSP_CACHE_BUDGET_GIB=1\nCRSP_CACHE_ALERT_GIB=1\n"
	if err := os.WriteFile(filepath.Join(f, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(runnercache.DirsFor(f).Rust, "k", "huge.rlib")
	if err := os.MkdirAll(filepath.Dir(big), 0o755); err != nil {
		t.Fatal(err)
	}
	fh, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := fh.Truncate(2 << 30); err != nil { // sparse: 2 GiB without disk use
		t.Fatal(err)
	}
	_ = fh.Close()

	h := &holder{busy: map[string]bool{"r1": true}}
	b := agent.NewBudgeter(agent.Config{Caches: staticRunners{{Name: "r1", Folder: f}}}).WithHolder(h)
	lines := b.Pass(context.Background())
	if !strings.Contains(strings.Join(lines, "\n"), "size alert") || !strings.Contains(strings.Join(lines, "\n"), "still over") {
		t.Fatalf("want an alert and a still-over line: %v", lines)
	}
	if _, err := os.Stat(big); err != nil {
		t.Fatal("a cache in use by a running job was removed")
	}
	if len(h.released) != 1 {
		t.Fatalf("the runner must be released: %v", h.released)
	}
	// The same file is not alerted again.
	for _, ln := range b.Pass(context.Background()) {
		if strings.Contains(ln, "size alert") {
			t.Fatalf("alerted twice: %v", ln)
		}
	}
	// Idle now: the budget pass removes the entry.
	h.busy = nil
	b.Pass(context.Background())
	if _, err := os.Stat(big); err == nil {
		t.Fatal("an idle runner's cache over the budget stays")
	}
}
