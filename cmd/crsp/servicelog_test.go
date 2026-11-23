package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CRSP233: the service log must not vanish after an upgrade. A crsp.log that
// cannot be opened falls back to another file and says why.
func TestOpenLogFileUsesCrspLog(t *testing.T) {
	dir := t.TempDir()
	f, problems, err := openLogFile(dir, t.TempDir(), 7)
	if err != nil || f == nil || len(problems) != 0 {
		t.Fatalf("f=%v problems=%v err=%v", f, problems, err)
	}
	defer func() { _ = f.Close() }()
	if filepath.Base(f.Name()) != "crsp.log" {
		t.Fatalf("name %s", f.Name())
	}
}

func TestOpenLogFileFallsBackWhenCrspLogIsUnwritable(t *testing.T) {
	dir := t.TempDir()
	// A folder named crsp.log cannot be opened for writing.
	if err := os.Mkdir(filepath.Join(dir, "crsp.log"), 0o750); err != nil {
		t.Fatal(err)
	}
	f, problems, err := openLogFile(dir, t.TempDir(), 7)
	if err != nil || f == nil {
		t.Fatalf("no fallback: %v", err)
	}
	defer func() { _ = f.Close() }()
	if filepath.Base(f.Name()) != "crsp-7.log" || len(problems) != 1 {
		t.Fatalf("name %s problems %v", f.Name(), problems)
	}
}

func TestOpenLogFileFallsBackToTempFolder(t *testing.T) {
	// The logs folder is a file, so neither file in it can be made.
	base := t.TempDir()
	dir := filepath.Join(base, "logs")
	if err := os.WriteFile(dir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f, problems, err := openLogFile(dir, t.TempDir(), 9)
	if err != nil || f == nil {
		t.Fatalf("no fallback: %v", err)
	}
	defer func() { _ = f.Close() }()
	if filepath.Base(f.Name()) != "crsp-service-9.log" || len(problems) != 2 {
		t.Fatalf("name %s problems %v", f.Name(), problems)
	}
}

func TestOpenLogFileReportsWhenNothingOpens(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone", "deeper")
	blocker := filepath.Join(t.TempDir(), "f")
	_ = os.WriteFile(blocker, nil, 0o600)
	f, problems, err := openLogFile(filepath.Join(blocker, "logs"), filepath.Join(blocker, missing), 1)
	if f != nil || err == nil || len(problems) != 3 {
		t.Fatalf("f=%v problems=%v err=%v", f, problems, err)
	}
}

func TestLogServiceLogPathSaysWhereAndWhy(t *testing.T) {
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	serviceLogPath, serviceLogProblems = `C:\ProgramData\crsp\logs\crsp-7.log`, []string{"crsp.log: access denied"}
	defer func() { serviceLogPath, serviceLogProblems = "", nil }()
	logServiceLogPath(logger)
	out := buf.String()
	if !strings.Contains(out, "crsp-7.log") || !strings.Contains(out, "access denied") {
		t.Fatalf("log = %s", out)
	}
}
