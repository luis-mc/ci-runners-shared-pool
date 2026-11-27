package toolcache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
)

type fakeRun struct{ got []sys.Command }

func (f *fakeRun) Run(_ context.Context, c sys.Command) (sys.Result, error) {
	f.got = append(f.got, c)
	return sys.Result{}, nil
}

func newStore(t *testing.T, cap int64) (*Store, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	n := 0
	s := &Store{Root: filepath.Join(t.TempDir(), "tc"), CapBytes: cap, Now: func() time.Time { return now },
		Token:    func() string { n++; return "t" + strconv.Itoa(n) },
		Platform: PlatformFor(runtime.GOOS, &fakeRun{})}
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s, &now
}

func fill(size int) func(string) error {
	return func(d string) error { return os.WriteFile(filepath.Join(d, "bin"), make([]byte, size), 0o644) }
}

func TestInstallPutsAWholeEntryInPlaceAndLeavesNoStage(t *testing.T) {
	s, _ := newStore(t, 0)
	p, win, err := s.Install("node", "22.1.0", fill(10))
	if err != nil || !win {
		t.Fatalf("%v %v", win, err)
	}
	if _, err := os.Stat(filepath.Join(p, "bin")); err != nil {
		t.Error(err)
	}
	if des, _ := os.ReadDir(filepath.Join(s.Root, stageDir)); len(des) != 0 {
		t.Errorf("stage left: %v", des)
	}
	if got, ok := s.Lookup("node", "22.1.0"); !ok || got != p {
		t.Errorf("lookup %q %v", got, ok)
	}
}

func TestFailedBuildLeavesNothing(t *testing.T) {
	s, _ := newStore(t, 0)
	boom := errors.New("boom")
	_, _, err := s.Install("node", "1", func(d string) error { _ = os.WriteFile(filepath.Join(d, "half"), []byte("x"), 0o644); return boom })
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, ok := s.Lookup("node", "1"); ok {
		t.Error("half entry visible")
	}
	if des, _ := os.ReadDir(filepath.Join(s.Root, stageDir)); len(des) != 0 {
		t.Error("stage left")
	}
}

func TestSecondInstallUsesTheExistingEntry(t *testing.T) {
	s, _ := newStore(t, 0)
	_, _, _ = s.Install("go", "1.25", fill(1))
	called := false
	_, win, err := s.Install("go", "1.25", func(string) error { called = true; return nil })
	if err != nil || win || called {
		t.Errorf("win=%v called=%v err=%v", win, called, err)
	}
}

func TestRaceNeverShowsAHalfEntryAndHasOneWinner(t *testing.T) {
	s, _ := newStore(t, 0)
	s.Token = nil // real random tokens
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, win, err := s.Install("rust", "1.90", func(d string) error {
				if err := os.WriteFile(filepath.Join(d, "a"), []byte("a"), 0o644); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(d, "b"), []byte("b"), 0o644)
			})
			if err != nil {
				t.Error(err)
			}
			if p, ok := s.Lookup("rust", "1.90"); ok {
				for _, f := range []string{"a", "b"} {
					if _, err := os.Stat(filepath.Join(p, f)); err != nil {
						t.Errorf("half entry: %v", err)
					}
				}
			}
			if win {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if winners != 1 {
		t.Errorf("%d winners", winners)
	}
	if des, _ := os.ReadDir(filepath.Join(s.Root, stageDir)); len(des) != 0 {
		t.Errorf("stages left: %d", len(des))
	}
}

func TestBadNamesAreRefused(t *testing.T) {
	s, _ := newStore(t, 0)
	for _, n := range []string{"", "..", ".stage", "a/b", `a\b`, "a:b"} {
		if _, _, err := s.Install(n, "1", fill(1)); !errors.Is(err, ErrBadName) {
			t.Errorf("%q: %v", n, err)
		}
		if _, _, err := s.Install("t", n, fill(1)); !errors.Is(err, ErrBadName) {
			t.Errorf("version %q: %v", n, err)
		}
	}
}

func TestEvictRemovesLeastRecentlyUsedAndKeepsRecentOnes(t *testing.T) {
	s, now := newStore(t, 250)
	for i, v := range []string{"old", "mid", "new"} {
		if _, _, err := s.Install("t", v, fill(100)); err != nil {
			t.Fatal(err)
		}
		age := []time.Duration{-5 * time.Hour, -3 * time.Hour, -time.Minute}[i]
		_ = os.Chtimes(filepath.Join(s.Root, "t", v), now.Add(age), now.Add(age))
	}
	res, err := s.Evict()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 1 || res.Removed[0].Version != "old" || res.After != 200 {
		t.Errorf("%+v", res)
	}
	if _, ok := s.Lookup("t", "old"); ok {
		t.Error("old still there")
	}
	// Everything recent: cap exceeded but nothing goes.
	s.CapBytes = 50
	_ = os.Chtimes(filepath.Join(s.Root, "t", "mid"), *now, *now)
	res, _ = s.Evict()
	if len(res.Removed) != 0 || res.Protected != 2 {
		t.Errorf("%+v", res)
	}
	if des, _ := os.ReadDir(filepath.Join(s.Root, trashDir)); len(des) != 0 {
		t.Error("trash left")
	}
}

func TestEvictDropsEmptyToolFolderAndOrphans(t *testing.T) {
	s, now := newStore(t, 10)
	_, _, _ = s.Install("solo", "1", fill(100))
	old := now.Add(-48 * time.Hour)
	_ = os.Chtimes(filepath.Join(s.Root, "solo", "1"), old, old)
	orphan := filepath.Join(s.Root, stageDir, "dead")
	_ = os.Mkdir(orphan, 0o755)
	_ = os.Chtimes(orphan, old, old)
	fresh := filepath.Join(s.Root, stageDir, "live")
	_ = os.Mkdir(fresh, 0o755)
	res, err := s.Evict()
	if err != nil || len(res.Removed) != 1 || res.Orphans != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "solo")); !os.IsNotExist(err) {
		t.Error("empty tool folder left")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("a running job's stage was removed")
	}
}

func TestLookupKeepsAnEntryOutOfEviction(t *testing.T) {
	s, now := newStore(t, 150)
	_, _, _ = s.Install("t", "a", fill(100))
	_, _, _ = s.Install("t", "b", fill(100))
	old := now.Add(-5 * time.Hour)
	_ = os.Chtimes(filepath.Join(s.Root, "t", "a"), old, old)
	_ = os.Chtimes(filepath.Join(s.Root, "t", "b"), old, old)
	s.Lookup("t", "a")
	res, _ := s.Evict()
	if len(res.Removed) != 1 || res.Removed[0].Version != "b" {
		t.Errorf("%+v", res)
	}
}

func TestWipe(t *testing.T) {
	s, _ := newStore(t, 0)
	_, _, _ = s.Install("a", "1", fill(1))
	_, _, _ = s.Install("b", "1", fill(1))
	n, err := s.Wipe()
	if err != nil || n != 2 {
		t.Fatal(n, err)
	}
	if es, _ := s.Entries(); len(es) != 0 {
		t.Error(es)
	}
}

func TestCheckStates(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t, 1000)
	r := s.CheckToolCache(ctx)
	if r.State != StateOK {
		t.Errorf("%+v", r)
	}
	_, _, _ = s.Install("a", "1", fill(950))
	if r = s.CheckToolCache(ctx); r.State != StateWarn || r.Bytes != 950 || r.Entries != 1 {
		t.Errorf("%+v", r)
	}
	missing := &Store{Root: filepath.Join(t.TempDir(), "none")}
	if r = missing.CheckToolCache(ctx); r.State != StateFail || !strings.Contains(r.Message, "does not exist") {
		t.Errorf("%+v", r)
	}
	s2, _ := newStore(t, 1000)
	_ = os.Mkdir(filepath.Join(s2.Root, stageDir, "x"), 0o755)
	if r = s2.CheckToolCache(ctx); r.State != StateWarn || r.Leftovers != 1 {
		t.Errorf("%+v", r)
	}
	var _ Checker = s
}

func TestUnixModeIsStickyAndVerifyCatchesAWideOpenRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits are not the access control of Windows (see TestWindowsPlan)")
	}
	s, _ := newStore(t, 0)
	fi, _ := os.Stat(s.Root)
	if fi.Mode()&os.ModeSticky == 0 || fi.Mode().Perm() != 0o777 {
		t.Errorf("root %v", fi.Mode())
	}
	_, _, _ = s.Install("t", "1", fill(1))
	fi, _ = os.Stat(filepath.Join(s.Root, "t"))
	if fi.Mode()&os.ModeSticky == 0 {
		t.Errorf("tool folder %v", fi.Mode())
	}
	_ = os.Chmod(s.Root, 0o755)
	if r := s.CheckToolCache(context.Background()); r.State != StateFail {
		t.Errorf("%+v", r)
	}
}

func TestPlatformsBehindInterfaces(t *testing.T) {
	// The Windows and macOS paths run on any host through fakes.
	run := &fakeRun{}
	w := PlatformFor("windows", run)
	if _, ok := w.(Windows); !ok {
		t.Fatalf("%T", w)
	}
	root := filepath.Join(t.TempDir(), "w")
	if err := w.Secure(context.Background(), OSFS{}, root); err != nil {
		t.Fatal(err)
	}
	if len(run.got) != 1 || run.got[0].Name != "icacls" {
		t.Fatalf("%v", run.got)
	}
	text := strings.Join(run.got[0].Args, " ")
	for _, want := range []string{"/inheritance:r", "CREATOR OWNER:(OI)(CI)(IO)(F)", RunnersGroup + ":(OI)(CI)(RX,WD,AD)"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(text, RunnersGroup+":(OI)(CI)(M)") || strings.Contains(text, RunnersGroup+":(OI)(CI)(F)") {
		t.Error("runners may change or delete")
	}
	if _, ok := PlatformFor("darwin", nil).(Unix); !ok {
		t.Error("macOS is not the Unix platform")
	}
	if err := (Windows{}).Secure(context.Background(), OSFS{}, root); err == nil {
		t.Error("no runner for icacls but no error")
	}
	for goos, want := range map[string]string{"windows": `C:\ProgramData\crsp\tool-cache`, "darwin": "/Users/Shared/crsp/tool-cache", "linux": "/var/lib/crsp/tool-cache"} {
		if DefaultRoot(goos) != want {
			t.Errorf("%s: %s", goos, DefaultRoot(goos))
		}
	}
	if err := checkUnixMode(os.ModeDir | 0o755); err == nil {
		t.Error("755 accepted")
	}
	if err := checkUnixMode(SharedMode); err != nil {
		t.Error(err)
	}
}
