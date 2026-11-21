package runnercache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func laneSet(t *testing.T, repo string, n int) LaneSet {
	t.Helper()
	return LaneSet{CacheRoot: t.TempDir(), RepoID: repo, Count: n}
}

func TestLaneLockOneJobAtATime(t *testing.T) {
	s := laneSet(t, "42", 2)
	a, err := s.Acquire("job-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Acquire("job-b")
	if err != nil {
		t.Fatal(err)
	}
	if a.Number == b.Number || a.Root == b.Root {
		t.Fatalf("two jobs share a lane: %d %d", a.Number, b.Number)
	}
	if _, err := s.Acquire("job-c"); !errors.Is(err, ErrNoFreeLane) {
		t.Fatalf("third job got a lane: %v", err)
	}
	for _, f := range a.Folders() {
		if st, err := os.Stat(f); err != nil || !st.IsDir() {
			t.Errorf("folder %s not made", f)
		}
	}
	if err := a.Release(); err != nil {
		t.Fatal(err)
	}
	if err := a.Release(); err != nil {
		t.Fatal("second release must be safe")
	}
	c, err := s.Acquire("job-c")
	if err != nil || c.Number != a.Number {
		t.Fatalf("the freed lane is first free again: %v %+v", err, c)
	}
}

func TestLaneWaitsForAFreeLane(t *testing.T) {
	s := laneSet(t, "7", 1)
	a, _ := s.Acquire("a")
	released := make(chan error, 1)
	go func() { released <- a.Release() }() // the waiter polls until the lane is free
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, err := s.AcquireWait(ctx, "b", 5*time.Millisecond)
	if err != nil || b == nil {
		t.Fatalf("waiting job: %v", err)
	}
	if err := <-released; err != nil {
		t.Fatalf("release: %v", err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel2()
	if _, err := s.AcquireWait(ctx2, "c", 5*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected the wait to end with the context: %v", err)
	}
}

func TestLaneOfADeadHolderIsTakenOver(t *testing.T) {
	s := laneSet(t, "9", 1)
	if _, err := s.Acquire("dead-job"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Acquire("job-2"); !errors.Is(err, ErrNoFreeLane) {
		t.Fatal("a nil Alive treats the holder as alive")
	}
	s.Alive = func(h string) bool { return h != "dead-job" }
	l, err := s.Acquire("job-2")
	if err != nil {
		t.Fatalf("stale lane not taken over: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(l.Root, ".lane.lock")); string(b) != "job-2" {
		t.Errorf("lock holder %q", b)
	}
}

func TestLanesNeverMixRepositories(t *testing.T) {
	root := t.TempDir()
	a := LaneSet{CacheRoot: root, RepoID: "1", Count: 1}
	b := LaneSet{CacheRoot: root, RepoID: "2", Count: 1}
	la, err := a.Acquire("x")
	if err != nil {
		t.Fatal(err)
	}
	lb, err := b.Acquire("y")
	if err != nil {
		t.Fatalf("another repository has its own lane: %v", err)
	}
	if strings.HasPrefix(la.Root, lb.Root) || strings.HasPrefix(lb.Root, la.Root) || filepath.Dir(la.Root) == filepath.Dir(lb.Root) {
		t.Fatalf("lanes overlap: %s %s", la.Root, lb.Root)
	}
	for _, bad := range []string{"", "..", ".", "a/b", `a\b`} {
		if _, err := (LaneSet{CacheRoot: root, RepoID: bad}).Acquire("z"); err == nil {
			t.Errorf("repository id %q accepted", bad)
		}
	}
}

func TestLaneVarsAreTheJobsOnlyView(t *testing.T) {
	d := LaneFor("/c", "5", 2)
	if d.Tools != filepath.Join("/c", "5", "lane-2", "tools") {
		t.Fatalf("tools %s", d.Tools)
	}
	v := LaneVars(LaneDirs{Tools: "/ci-tools", Rust: "/rust-target", ToolCache: "/tool-cache"})
	if v[EnvToolsDir] != "/ci-tools" || v[EnvRustDir] != "/rust-target" || v[EnvToolCache] != "/tool-cache" || len(v) != 3 {
		t.Fatalf("vars %v", v)
	}
}

func TestLaneQuotasAndEvictionAsARunner(t *testing.T) {
	root := t.TempDir()
	s := LaneSet{CacheRoot: root, RepoID: "3"}
	l, _ := s.Acquire("j")
	big := func(dir, name string, mod time.Time) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(p, "f")
		if err := os.WriteFile(f, make([]byte, 600), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(f, mod, mod)
		_ = os.Chtimes(p, mod, mod)
	}
	now := time.Now()
	big(l.Tools, "old", now.Add(-2*time.Hour))
	big(l.Tools, "new", now)
	caps := LaneCaps(l.LaneDirs, Quota{ToolGiB: 1, RustGiB: 1})
	if len(caps) != 2 || caps[0].Bytes != GiB || caps[0].Dir != l.Tools || caps[1].Dir != l.Rust {
		t.Fatalf("caps %+v", caps)
	}
	// Quota of 1 GiB: nothing to remove. A tiny cap by hand: oldest goes first.
	res, err := Enforce(OSFS{}, l.Tools, 700, false)
	if err != nil || len(res.Removed) != 1 || filepath.Base(res.Removed[0].Path) != "old" {
		t.Fatalf("eviction %+v %v", res, err)
	}
	// Busy lane: SweepLane removes nothing.
	sw, err := SweepLane(OSFS{}, l.LaneDirs, Quota{ToolGiB: 1}, true)
	if err != nil || sw.Tools.Skipped != "runner busy" {
		t.Fatalf("busy sweep %+v %v", sw, err)
	}
	if LaneTotal(root) < 600 {
		t.Errorf("lane bytes count in the machine total: %d", LaneTotal(root))
	}
}

func TestRemoveRepositoryLanesNeedsYesAndNoJob(t *testing.T) {
	root := t.TempDir()
	s := LaneSet{CacheRoot: root, RepoID: "8", Count: 2}
	other := LaneSet{CacheRoot: root, RepoID: "9"}
	if _, err := other.Acquire("o"); err != nil {
		t.Fatal(err)
	}
	l, _ := s.Acquire("job")
	_ = os.WriteFile(filepath.Join(l.Tools, "x"), make([]byte, 100), 0o644)
	if _, err := s.RemoveRepositoryLanes(false); !errors.Is(err, ErrNeedsConfirmation) {
		t.Fatalf("no yes: %v", err)
	}
	if _, err := s.RemoveRepositoryLanes(true); !errors.Is(err, ErrLaneBusy) {
		t.Fatalf("a held lane is never deleted: %v", err)
	}
	_ = l.Release()
	freed, err := s.RemoveRepositoryLanes(true)
	if err != nil || freed != 100 {
		t.Fatalf("freed %d %v", freed, err)
	}
	if _, err := os.Stat(LaneRoot(root, "8")); !os.IsNotExist(err) {
		t.Error("lanes of the repository remain")
	}
	if LanesOnDisk(root, "9") != 1 {
		t.Error("another repository lost its lane")
	}
}
