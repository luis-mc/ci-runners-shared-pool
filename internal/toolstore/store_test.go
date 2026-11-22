package toolstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

func newStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore(filepath.Join(t.TempDir(), "store"), Darwin, "arm64")
	s.Now = func() time.Time { return t0 }
	t.Cleanup(func() { _ = unsealDir(s.Root) })
	return s
}

var rust185 = Tool{Tool: ToolRust, Version: "1.85.0"}

// R5.111: missing versions are installed once; the second ask installs nothing; the version is sealed.
func TestEnsureInstalledOnce(t *testing.T) {
	s, in := newStore(t), &FakeInstaller{}
	plan := Plan{rust185, {Tool: ToolGo, Version: "1.24.0"}}
	if got := s.Missing(plan); len(got) != 2 {
		t.Fatalf("missing = %v", got)
	}
	if err := s.EnsureInstalled(context.Background(), in, plan); err != nil || in.Calls != 2 {
		t.Fatalf("install: %v calls %d", err, in.Calls)
	}
	if err := s.EnsureInstalled(context.Background(), in, plan); err != nil || in.Calls != 2 {
		t.Fatalf("second ask: %v calls %d", err, in.Calls)
	}
	if len(s.Missing(plan)) != 0 || !s.Has(rust185) {
		t.Fatal("not in store")
	}
	info, err := os.Stat(filepath.Join(s.Dir(rust185), "bin", "tool"))
	if err != nil || info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("file mode %v %v: not sealed", info, err)
	}
	if d, _ := os.Stat(s.Dir(rust185)); runtime.GOOS != "windows" && d.Mode().Perm()&0o222 != 0 {
		t.Fatal("folder not sealed")
	}
	if !strings.Contains(in.Dests[0], ".staging") {
		t.Fatalf("installed outside staging: %v", in.Dests)
	}
}

func TestEnsureInstalledConcurrentOnce(t *testing.T) {
	s, in := newStore(t), &FakeInstaller{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.EnsureInstalled(context.Background(), in, Plan{rust185})
		}()
	}
	wg.Wait()
	if in.Calls != 1 {
		t.Fatalf("%d installs, want 1", in.Calls)
	}
}

// A failed install leaves nothing a job can use, and the other tools still install.
func TestEnsureInstalledFailureLeavesNothing(t *testing.T) {
	s := newStore(t)
	in := &FakeInstaller{Fail: map[string]error{"rust-1.85.0": errors.New("boom")}}
	err := s.EnsureInstalled(context.Background(), in, Plan{rust185, {Tool: ToolGo, Version: "1.24.0"}})
	if err == nil || !strings.Contains(err.Error(), "rust 1.85.0") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	if s.Has(rust185) || !s.Has(Tool{Tool: ToolGo, Version: "1.24.0"}) {
		t.Fatal("store state wrong")
	}
	if _, err := os.Stat(filepath.Join(s.Root, ".staging", "rust-1.85.0")); err == nil {
		t.Fatal("staging left behind")
	}
	if err := s.EnsureInstalled(context.Background(), in, Plan{{Tool: "cobol", Version: "1"}, {Tool: ToolGo, Version: "../x"}}); err == nil {
		t.Fatal("bad tools must fail")
	}
}

// Versions of one tool sit side by side; a folder without the marker is not installed.
func TestSideBySideAndMarker(t *testing.T) {
	s, in := newStore(t), &FakeInstaller{}
	a, b := Tool{Tool: ToolNode, Version: "20.1.0"}, Tool{Tool: ToolNode, Version: "22.1.0"}
	if err := s.EnsureInstalled(context.Background(), in, Plan{a, b}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Root, ToolNode, "18.0.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	l, err := s.List()
	if err != nil || len(l) != 2 || l[0].Tool != a || l[1].Tool != b {
		t.Fatalf("list = %+v %v", l, err)
	}
	if s.Has(Tool{Tool: ToolNode, Version: "18.0.0"}) {
		t.Fatal("unmarked folder counts as installed")
	}
}

// R5.118: a version unused for 30 days goes; one in use, and one used lately stay (Xcode is never in the store, so never pruned).
func TestPrune(t *testing.T) {
	s, in := newStore(t), &FakeInstaller{}
	day := func(n int) time.Time { return t0.Add(-time.Duration(n) * 24 * time.Hour) }
	old, busy, fresh := Tool{Tool: ToolGo, Version: "1.20.0"}, Tool{Tool: ToolGo, Version: "1.21.0"}, Tool{Tool: ToolGo, Version: "1.24.0"}
	s.Now = func() time.Time { return day(60) }
	if err := s.EnsureInstalled(context.Background(), in, Plan{old, busy}); err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return day(5) }
	if err := s.EnsureInstalled(context.Background(), in, Plan{fresh}); err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return day(45) }
	s.Now = func() time.Time { return t0 }
	removed, err := s.Prune(PruneOptions{InUse: Plan{busy}})
	if err != nil {
		t.Fatal(err)
	}
	if planStr(Plan(removed)) != "go 1.20.0" {
		t.Fatalf("removed %v", removed)
	}
	if !s.Has(busy) || !s.Has(fresh) || s.Has(old) {
		t.Fatal("store state wrong")
	}
}

func TestProbeLacks(t *testing.T) {
	s, in := newStore(t), &FakeInstaller{}
	_ = s.EnsureInstalled(context.Background(), in, Plan{{Tool: ToolGo, Version: "1.24.0"}})
	items := s.Probe(Plan{{Tool: ToolGo, Version: "1.24.0"}, rust185})
	l := Lacks(items)
	if len(items) != 2 || len(l) != 1 || l[0] != (Lack{Tool: ToolRust, Version: "1.85.0"}) {
		t.Fatalf("items %+v lacks %+v", items, l)
	}
	if in.Calls != 1 {
		t.Fatal("probe installed")
	}
}
