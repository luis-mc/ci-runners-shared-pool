package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// R5.24: the agent keeps the last settings it got and applies them while the
// link is down (and after a restart).
func TestSettingsCacheRoundTrip(t *testing.T) {
	c := agent.SettingsCache{Path: filepath.Join(t.TempDir(), "sub", "settings.json")}
	if _, _, ok := c.Load(); ok {
		t.Fatal("a cache that was never written loaded")
	}
	s := machines.DefaultSettings()
	s.BudgetMemoryPct, s.Version = 70, 9
	if err := c.Save(s, machines.StateDrained); err != nil {
		t.Fatal(err)
	}
	got, state, ok := c.Load()
	if !ok || got.BudgetMemoryPct != 70 || got.Version != 9 || state != machines.StateDrained {
		t.Fatalf("%+v %q %v", got, state, ok)
	}
	if st, _ := os.Stat(c.Path); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 { // POSIX modes only
		t.Fatalf("mode = %v", st.Mode().Perm())
	}
	_ = os.WriteFile(c.Path, []byte("{not json"), 0o600)
	if _, _, ok := c.Load(); ok {
		t.Fatal("a corrupt cache loaded")
	}
}

func TestRestartedAgentAppliesCachedSettingsBeforeTheLinkIsUp(t *testing.T) {
	c := startController(t)
	code, _ := c.Svc.CreateJoinCode(context.Background(), "owner", 0)
	cfg := fastConfig(t, c, code.Code)
	stop := runAgent(t, cfg)
	waitFor(t, "settings cached", func() bool { _, _, ok := cfg.DataDirCache().Load(); return ok })
	_ = stop()
}
