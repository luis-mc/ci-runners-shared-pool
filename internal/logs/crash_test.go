package logs

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/redact"
)

func newCrasher(t *testing.T) *Crasher {
	t.Helper()
	return &Crasher{Dir: filepath.Join(t.TempDir(), "crash"), Mode: "controller", Version: "9.9.9",
		Now: func() time.Time { return time.Date(2026, 11, 26, 10, 30, 5, 0, time.UTC) }}
}

func TestPanicWritesCrashFile(t *testing.T) {
	c := newCrasher(t)
	r := redact.New()
	r.Add("sekrit-value-123")
	c.R = r
	if !c.Recover(func() { panic("boom with sekrit-value-123") }) {
		t.Fatal("panic not caught")
	}
	if c.Recover(func() {}) {
		t.Fatal("no panic reported as one")
	}
	path := filepath.Join(c.Dir, "crash-20261126T103005Z-controller.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{"version: 9.9.9", "os: " + runtime.GOOS, "arch: " + runtime.GOARCH, "mode: controller", "message: boom with ", "TestPanicWritesCrashFile"} {
		if !strings.Contains(s, want) {
			t.Errorf("crash file lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "sekrit-value-123") {
		t.Error("secret in the crash file")
	}
	// a second crash in the same second gets its own file
	c.Recover(func() { panic("again") })
	if _, err := os.Stat(filepath.Join(c.Dir, "crash-20261126T103005Z-controller-2.txt")); err != nil {
		t.Error(err)
	}
}

func TestSetCrashOutputFolder(t *testing.T) {
	c := newCrasher(t)
	if err := c.InstallFatal(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.Dir, "crash-20261126T103005Z-controller.txt")
	if _, err := os.Stat(path); err != nil {
		t.Fatal("fatal output file not in the crash folder:", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("an empty fatal file was left behind on a clean exit")
	}
}

func TestUnreportedCrashShownOnce(t *testing.T) {
	c := newCrasher(t)
	c.Recover(func() { panic("one") })
	if err := os.WriteFile(filepath.Join(c.Dir, "crash-20260101T000000Z-agent.txt"), nil, 0o600); err != nil {
		t.Fatal(err) // an empty file is not a crash
	}
	var got []string
	n, err := ShowOnce(c.Dir, func(f []string) error { got = f; return nil })
	if err != nil || n != 1 || len(got) != 1 {
		t.Fatalf("n=%d err=%v got=%v", n, err, got)
	}
	n, err = ShowOnce(c.Dir, func([]string) error { t.Fatal("shown twice"); return nil })
	if err != nil || n != 0 {
		t.Fatalf("second start: n=%d err=%v", n, err)
	}
	// a notice that failed to show comes again
	c.Recover(func() { panic("two") })
	if _, err := ShowOnce(c.Dir, func([]string) error { return errors.New("console down") }); err == nil {
		t.Fatal("want the show error")
	}
	if left, _ := Unreported(c.Dir); len(left) != 1 {
		t.Fatalf("unreported after a failed show: %v", left)
	}
}
