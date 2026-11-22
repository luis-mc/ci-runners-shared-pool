package memcap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCgroupWritesTheCapAndSeesTheKill(t *testing.T) {
	root := t.TempDir()
	g, err := CgroupEnforcer{Root: root}.Start(Job{ID: "j1", PID: 42}, LimitBytes(16))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "crsp-job-j1")
	read := func(n string) string { b, _ := os.ReadFile(filepath.Join(dir, n)); return string(b) }
	if read("memory.max") != "17179869184" || read("memory.swap.max") != "0" || read("memory.oom.group") != "1" || read("cgroup.procs") != "42" {
		t.Fatal("cgroup files wrong")
	}
	if g.Exceeded() {
		t.Fatal("not over yet")
	}
	_ = os.WriteFile(filepath.Join(dir, "memory.events"), []byte("low 0\nhigh 0\nmax 3\noom 1\noom_kill 1\n"), 0o600)
	if !g.Exceeded() {
		t.Fatal("oom_kill 1 is over")
	}
}

func TestCgroupRefusesNoLimitAndBadID(t *testing.T) {
	e := CgroupEnforcer{Root: t.TempDir()}
	if _, err := e.Start(Job{ID: "a", PID: 1}, 0); err != ErrNoLimit {
		t.Fatalf("got %v", err)
	}
	for _, id := range []string{"", "../x", "a/b", ".."} {
		if _, err := e.Start(Job{ID: id, PID: 1}, 1); err == nil {
			t.Errorf("id %q accepted", id)
		}
	}
}

func TestOOMKillsReadsTheCount(t *testing.T) {
	if OOMKills("oom 2\noom_kill 3\n") != 3 || OOMKills("") != 0 || OOMKills("oom_kill x") != 0 {
		t.Fatal("count wrong")
	}
}

type fakeTree map[int]struct {
	pids []int
	rss  int64
}

func (f fakeTree) Members(root int) ([]int, int64) { return f[root].pids, f[root].rss }

func TestWatcherStopsATreeOverTheLimit(t *testing.T) {
	tree := fakeTree{10: {[]int{10, 11}, 3 * GiB}}
	var killed []int
	w := &Watcher{Tree: tree, Kill: func(p []int) { killed = append(killed, p...) }}
	g, _ := w.Start(Job{ID: "j", PID: 10}, 4*GiB)
	if got := w.Poll(); len(got) != 0 || g.Exceeded() {
		t.Fatal("under the limit")
	}
	tree[10] = struct {
		pids []int
		rss  int64
	}{[]int{10, 11, 12}, 5 * GiB}
	if got := w.Poll(); len(got) != 1 || got[0] != "j" {
		t.Fatalf("stopped %v", got)
	}
	if !g.Exceeded() || len(killed) != 3 {
		t.Fatalf("over=%v killed=%v", g.Exceeded(), killed)
	}
	if got := w.Poll(); len(got) != 0 {
		t.Fatal("stopped twice")
	}
	_ = g.Release()
}

func TestWatcherIgnoresAGoneTreeAndReleasedJobs(t *testing.T) {
	w := &Watcher{Tree: fakeTree{}, Kill: func([]int) { t.Fatal("killed") }}
	g, _ := w.Start(Job{ID: "j", PID: 9}, GiB)
	w.Poll()
	_ = g.Release()
	w.Poll()
	if _, err := w.Start(Job{ID: "k"}, 0); err != ErrNoLimit {
		t.Fatal("no limit")
	}
}

func TestFakeTripsAndHostPicksTheMechanism(t *testing.T) {
	f := &Fake{}
	g, _ := f.Start(Job{ID: "a"}, GiB)
	f.Trip("a")
	if !g.Exceeded() {
		t.Fatal("tripped")
	}
	w := &Watcher{}
	if forOS("darwin", "", w) != Enforcer(w) {
		t.Fatal("darwin uses the watcher")
	}
	if forOS("linux", "/x", w).Name() != "cgroup" {
		t.Fatal("linux uses cgroup")
	}
}

func TestOverMemoryTextNamesTheClassToRaise(t *testing.T) {
	s := OverMemoryText("large", 16, "xl")
	if !strings.Contains(s, "over memory") || !strings.Contains(s, "large") || !strings.Contains(s, "to xl") {
		t.Fatal(s)
	}
	if !strings.Contains(OverMemoryText("xl", 32, ""), "largest") {
		t.Fatal("xl text")
	}
}
