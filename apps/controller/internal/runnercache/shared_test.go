package runnercache

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func newShared(t *testing.T) *Shared {
	t.Helper()
	d := t.TempDir()
	return &Shared{Root: filepath.Join(d, "shared"), Private: filepath.Join(d, "private")}
}

func TestSharedRootAndNewFoldersHaveSharedMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes are Unix; Windows uses SharedACLPlan")
	}
	old := setUmask(0o077) // a strict umask must not narrow the folders
	defer setUmask(old)
	s := newShared(t)
	if err := s.Prepare(); err != nil {
		t.Fatal(err)
	}
	if err := s.Folder(filepath.Join(s.Root, "node", "20.1.0")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{s.Root, filepath.Join(s.Root, "node"), filepath.Join(s.Root, "node", "20.1.0")} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode()&(os.ModeSticky|0o777) != os.ModeSticky|0o777 {
			t.Errorf("%s mode %v, want 1777", p, st.Mode())
		}
	}
	if err := s.Folder(filepath.Join(t.TempDir(), "x")); err == nil {
		t.Error("a folder outside the root was made")
	}
}

func TestLostReplaceRaceUsesTheWinnerFile(t *testing.T) {
	s := newShared(t)
	_ = s.Prepare()
	w1, err := s.Put("tool/1/bin", []byte("winner"))
	if err != nil || !w1 {
		t.Fatalf("first put %v %v", w1, err)
	}
	w2, err := s.Put("tool/1/bin", []byte("loser"))
	if err != nil || w2 {
		t.Fatalf("second put must lose quietly: %v %v", w2, err)
	}
	if b, _ := os.ReadFile(filepath.Join(s.Root, "tool/1/bin")); string(b) != "winner" {
		t.Errorf("content %q", b)
	}
}

func TestRacingPutsEndWithOneGoodFile(t *testing.T) {
	s := newShared(t)
	_ = s.Prepare()
	var wg sync.WaitGroup
	wins := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w, err := s.Put("t/v/file", []byte(strings.Repeat("x", 4096)))
			if err != nil {
				t.Error(err)
			}
			wins <- w
		}()
	}
	wg.Wait()
	close(wins)
	n := 0
	for w := range wins {
		if w {
			n++
		}
	}
	if n < 1 {
		t.Fatal("nobody wrote the file")
	}
	b, _ := os.ReadFile(filepath.Join(s.Root, "t/v/file"))
	if len(b) != 4096 {
		t.Fatalf("file has %d bytes", len(b))
	}
	es, _ := os.ReadDir(filepath.Join(s.Root, "t/v"))
	if len(es) != 1 {
		t.Errorf("leftover temp files: %d entries", len(es))
	}
}

func TestUnwritableSharedFolderFallsBackWithOneWarning(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs an unprivileged Unix user to make a folder unwritable")
	}
	s := newShared(t)
	_ = s.Prepare()
	var warnings []string
	s.Warn = func(m string) { warnings = append(warnings, m) }
	if err := os.Chmod(s.Root, 0o555); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(s.Root, 0o755) }()
	for i := 0; i < 3; i++ {
		p, shared := s.Resolve("node/20")
		if shared || !strings.HasPrefix(p, s.Private) {
			t.Fatalf("got %s shared=%v, want the private cache", p, shared)
		}
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings %v, want exactly one", warnings)
	}
}

func TestWritableSharedFolderIsUsed(t *testing.T) {
	s := newShared(t)
	_ = s.Prepare()
	p, shared := s.Resolve("node/20")
	if !shared || p != filepath.Join(s.Root, "node", "20") {
		t.Fatalf("got %s %v", p, shared)
	}
}

func TestSharedACLPlanGivesCreateButNotDelete(t *testing.T) {
	c := SharedACLPlan(`C:\ProgramData\crsp\cache\shared`)
	text := c.Name + " " + strings.Join(c.Args, " ")
	for _, want := range []string{"icacls", "/inheritance:r", "crsp-runners:(OI)(CI)(RX,WD,AD)", "CREATOR OWNER:(OI)(CI)(IO)(F)"} {
		if !strings.Contains(text, want) {
			t.Errorf("plan lacks %q: %s", want, text)
		}
	}
	// The runners group never gets delete (D) or full control (F) or modify (M).
	for _, a := range c.Args {
		if strings.HasPrefix(a, "crsp-runners:") && (strings.Contains(a, ",D") || strings.Contains(a, "(F)") || strings.Contains(a, "(M)") || strings.Contains(a, "DC")) {
			t.Errorf("the runners group may change or delete: %s", a)
		}
	}
}
