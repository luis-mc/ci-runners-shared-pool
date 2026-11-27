package persistent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func diag(t *testing.T, folder, name, text string) {
	t.Helper()
	d := filepath.Join(folder, "_diag")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, name), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiagStart(t *testing.T) {
	got, ok := DiagStart("Runner_20261120-101502-utc.log")
	if !ok || !got.Equal(time.Date(2026, 11, 20, 10, 15, 2, 0, time.UTC)) {
		t.Fatalf("got %v %v", got, ok)
	}
	if _, ok := DiagStart("Worker_20261120-101502-utc.log"); ok {
		t.Fatal("a worker log is not a listener log")
	}
}

func TestSessionWaitCountsFromTheFirstRefusingLog(t *testing.T) {
	f := t.TempDir()
	diag(t, f, "Runner_20261120-100000-utc.log", "Runner connected.\n")
	diag(t, f, "Runner_20261120-100100-utc.log", "Error: A session for this runner already exists.\n")
	diag(t, f, "Runner_20261120-100300-utc.log", "Error: A session for this runner already exists.\n")
	now := time.Date(2026, 11, 20, 10, 4, 0, 0, time.UTC)
	refused, until := SessionWait(f, now, 0)
	if !refused || !until.Equal(time.Date(2026, 11, 20, 10, 7, 0, 0, time.UTC)) {
		t.Fatalf("refused %v until %v", refused, until)
	}
	// Past the window: still refused, but no end time, a real fault.
	refused, until = SessionWait(f, time.Date(2026, 11, 20, 10, 8, 0, 0, time.UTC), 0)
	if !refused || !until.IsZero() {
		t.Fatalf("refused %v until %v", refused, until)
	}
}

func TestSessionWaitNothingRefused(t *testing.T) {
	f := t.TempDir()
	if r, u := SessionWait(f, time.Now(), 0); r || !u.IsZero() {
		t.Fatal("no _diag folder is not a refusal")
	}
	diag(t, f, "Runner_20261120-100000-utc.log", "Runner connected.\n")
	if r, _ := SessionWait(f, time.Date(2026, 11, 20, 10, 1, 0, 0, time.UTC), 0); r {
		t.Fatal("a connected listener is not refused")
	}
}
