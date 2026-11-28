package mirror

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/lancache"
)

// fakeMirror makes a git mirror folder of size bytes, last changed at.
func fakeMirror(t *testing.T, g *Git, repo string, size int, at time.Time) {
	t.Helper()
	p, err := g.Path(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "pack"), make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
}

func putAt(t *testing.T, st *lancache.Store, clock *time.Time, at time.Time, ns, scope, name string, size int) {
	t.Helper()
	*clock = at
	n, _ := lancache.Name(ns, scope, name)
	if _, err := st.Put(n, "", strings.NewReader(strings.Repeat(name[:1], size))); err != nil {
		t.Fatal(err)
	}
}

func TestBudgetEvictsLeastRecentlyUsedAcrossKinds(t *testing.T) {
	t0 := time.Unix(1_000_000, 0)
	clock := t0
	st, err := lancache.Open(t.TempDir(), 1<<20, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	g := &Git{Dir: t.TempDir()}
	// Oldest first: a git mirror (t0), an Actions entry (t0+1h), a package (t0+2h), a newer mirror (t0+3h).
	fakeMirror(t, g, "o/old", 400, t0)
	putAt(t, st, &clock, t0.Add(time.Hour), lancache.NSActions, "r1", "a/v", 300)
	putAt(t, st, &clock, t0.Add(2*time.Hour), lancache.NSPackages, "o__r", "p/x", 200)
	fakeMirror(t, g, "o/new", 100, t0.Add(3*time.Hour))
	b := &Budget{Store: st, Git: g, Bytes: func() int64 { return 350 }, Now: func() time.Time { return t0.Add(5 * time.Hour) }}
	if got := b.Used(); got != 1000 {
		t.Fatalf("used %d", got)
	}
	freed, gone := b.Enforce()
	if freed != 700 || len(gone) != 2 || gone[0].Name != "o/old" || gone[1].Kind != BudgetStore || !strings.HasPrefix(gone[1].Name, "actions/") {
		t.Fatalf("freed %d gone %+v", freed, gone)
	}
	if b.Used() > 350 {
		t.Fatalf("still over: %d", b.Used())
	}
	if len(st.Entries("packages/")) != 1 || len(g.OnDisk()) != 1 {
		t.Fatal("the newer items must stay")
	}
}

func TestBudgetUnderCapRemovesNothing(t *testing.T) {
	st, _ := lancache.Open(t.TempDir(), 1<<20, nil)
	n, _ := lancache.Name(lancache.NSPackages, "o__r", "x")
	_, _ = st.Put(n, "", strings.NewReader("abc"))
	for _, bytes := range []int64{1 << 20, 0, -1} {
		b := &Budget{Store: st, Bytes: func() int64 { return bytes }}
		if freed, gone := b.Enforce(); freed != 0 || len(gone) != 0 {
			t.Fatalf("budget %d removed %v", bytes, gone)
		}
	}
	if (&Budget{Store: st}).Used() != 3 {
		t.Fatal("used")
	}
}

func TestBudgetKeepsARecentGitMirror(t *testing.T) {
	now := time.Unix(2_000_000, 0)
	g := &Git{Dir: t.TempDir()}
	fakeMirror(t, g, "o/busy", 500, now.Add(-10*time.Minute))
	b := &Budget{Git: g, Bytes: func() int64 { return 100 }, Now: func() time.Time { return now }}
	if freed, _ := b.Enforce(); freed != 0 || len(g.OnDisk()) != 1 {
		t.Fatal("a mirror used 10 minutes ago was evicted")
	}
	now = now.Add(time.Hour)
	if freed, _ := b.Enforce(); freed != 500 || len(g.OnDisk()) != 0 {
		t.Fatalf("an old mirror over the budget stayed (freed %d)", freed)
	}
}

func TestBudgetSettingDefaultAndRange(t *testing.T) {
	if got := (Settings{}).Normalize(); got.BudgetGiB != 60 || got.PackageProxy {
		t.Fatalf("defaults %+v", got)
	}
	if (Settings{}).BudgetBytes() != 60<<30 {
		t.Fatal("bytes")
	}
	for _, bad := range []int{-1, 4097} {
		if (Settings{BudgetGiB: bad}).Validate() == nil {
			t.Errorf("budget %d accepted", bad)
		}
	}
	// A smaller budget applies at once: the store's cap follows it.
	st, _ := lancache.Open(t.TempDir(), 1<<40, nil)
	ms := &memSettings{}
	kicked := 0
	s := &Service{Settings: ms, ActStore: st, Budget: &Budget{Store: st, Bytes: func() int64 { kicked++; return 0 }}}
	if _, err := s.Put(context.Background(), audit.Actor{}, &rec{}, Settings{BudgetGiB: 2, PackageProxy: true}); err != nil {
		t.Fatal(err)
	}
	if st.Cap() != 2<<30 || !ms.v.PackageProxy {
		t.Fatalf("cap %d settings %+v", st.Cap(), ms.v)
	}
}

func TestWipePackagesAndAllGitMirrors(t *testing.T) {
	st, _ := lancache.Open(t.TempDir(), 1<<20, nil)
	for _, n := range []string{"npm/tgz/a/a-1.tgz", "pypi/file/x"} {
		k, _ := lancache.Name(lancache.NSPackages, "o__r", n)
		_, _ = st.Put(k, "", strings.NewReader(n))
	}
	act, _ := lancache.Name(lancache.NSActions, "1", "k/v")
	_, _ = st.Put(act, "", strings.NewReader("keep"))
	g := &Git{Dir: t.TempDir()}
	fakeMirror(t, g, "o/a", 10, time.Now())
	fakeMirror(t, g, "o/b", 10, time.Now())
	wp := &Wiper{Targets: TargetsFor(nil, st, g, nil, ""), Jobs: &fakeJobs{}, Poll: time.Millisecond, Wait: time.Second}
	ctx := context.Background()
	for _, id := range []string{"mirror:packages", "mirror:git_all"} {
		scope, target, ok := ParseID(id)
		if !ok {
			t.Fatalf("%s not a wipe id", id)
		}
		p, _, err := wp.Run(ctx, audit.Actor{}, true, false, scope, target)
		if err == nil || len(p.Items) == 0 {
			t.Fatalf("%s: plan %+v err %v", id, p, err)
		}
		if _, _, err := wp.Run(ctx, audit.Actor{}, true, true, scope, target); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if len(st.Entries("packages/")) != 0 || len(g.OnDisk()) != 0 {
		t.Fatal("wipe left entries")
	}
	if len(st.Entries("actions/")) != 1 {
		t.Fatal("the packages wipe touched the Actions cache")
	}
}
