package checks

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/internal/logs"
)

func TestCrashFilesListed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "crash")
	chk := CrashFileCheck(dir)
	got := chk.Run(context.Background(), doctor.Env{})
	if len(got) != 1 || got[0].Severity != doctor.OK {
		t.Fatalf("no folder: %+v", got)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "crash-20261126T103005Z-agent.txt")
	if err := os.WriteFile(path, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got = chk.Run(context.Background(), doctor.Env{})
	if got[0].Severity != doctor.Warn || len(got[0].Items) != 1 || got[0].Items[0].Name != "crash-20261126T103005Z-agent.txt" {
		t.Fatalf("unreported file: %+v", got)
	}
	if err := logs.MarkReported(dir, path); err != nil {
		t.Fatal(err)
	}
	if got = chk.Run(context.Background(), doctor.Env{}); got[0].Severity != doctor.OK {
		t.Fatalf("reported file still listed: %+v", got)
	}
}
