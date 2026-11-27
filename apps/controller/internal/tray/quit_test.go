package tray

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeProcs struct {
	mu       sync.Mutex
	trays    []int
	obeys    map[int]bool // dies when asked
	alive    map[int]bool
	killed   []int
	asked    []int
	traysDir string
}

func newFake(obey map[int]bool) *fakeProcs {
	f := &fakeProcs{obeys: obey, alive: map[int]bool{}}
	for pid := range obey {
		f.trays = append(f.trays, pid)
		f.alive[pid] = true
	}
	return f
}

func (f *fakeProcs) Trays(dir string) ([]int, error) { f.traysDir = dir; return f.trays, nil }
func (f *fakeProcs) Ask(pid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, pid)
	if f.obeys[pid] {
		f.alive[pid] = false
	}
	return nil
}
func (f *fakeProcs) Alive(pid int) bool { f.mu.Lock(); defer f.mu.Unlock(); return f.alive[pid] }
func (f *fakeProcs) Kill(pid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killed = append(f.killed, pid)
	f.alive[pid] = false
	return nil
}

func TestQuitAllLeavesPoliteTraysAlone(t *testing.T) {
	f := newFake(map[int]bool{10: true, 11: true})
	found, forced, err := QuitAll(context.Background(), f, `C:\crsp`, time.Second)
	if err != nil || found != 2 || forced != 0 || len(f.killed) != 0 || len(f.asked) != 2 {
		t.Fatalf("found=%d forced=%d err=%v killed=%v asked=%v", found, forced, err, f.killed, f.asked)
	}
}

func TestQuitAllEndsATrayThatIgnoresTheRequest(t *testing.T) {
	f := newFake(map[int]bool{10: true, 12: false})
	start := time.Now()
	found, forced, err := QuitAll(context.Background(), f, `C:\crsp`, 300*time.Millisecond)
	if err != nil || found != 2 || forced != 1 || len(f.killed) != 1 || f.killed[0] != 12 {
		t.Fatalf("found=%d forced=%d err=%v killed=%v", found, forced, err, f.killed)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("QuitAll waited far longer than its grace")
	}
}

func TestQuitAllWithNoTrayIsQuick(t *testing.T) {
	f := newFake(nil)
	found, forced, err := QuitAll(context.Background(), f, "x", time.Minute)
	if err != nil || found != 0 || forced != 0 {
		t.Fatal(found, forced, err)
	}
}

func TestIsTrayCmdline(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "crsp")
	exe := filepath.Join(dir, "crsp.exe")
	other := filepath.Join(filepath.Dir(dir), "other", "crsp.exe")
	cases := []struct {
		argv []string
		want bool
	}{
		{[]string{exe, "tray"}, true},
		{[]string{exe, "tray", "--wait", "20s"}, true},
		{[]string{exe, "tray", "--quit"}, false},
		{[]string{exe, "controller"}, false},
		{[]string{other, "tray"}, false},
		{[]string{exe}, false},
	}
	for _, c := range cases {
		if got := isTrayCmdline(c.argv, dir); got != c.want {
			t.Errorf("%v: got %v", c.argv, got)
		}
	}
}
