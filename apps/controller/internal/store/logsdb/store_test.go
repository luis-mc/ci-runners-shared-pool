package logsdb

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/logs"
)

func open(t *testing.T) (*DB, string) {
	t.Helper()
	dir := t.TempDir()
	d, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d, dir
}

// TestSeparateDatabaseFile: the log database is its own file next to, not in,
// the main database (R6.10): opening it never creates controller.db.
func TestSeparateDatabaseFile(t *testing.T) {
	d, dir := open(t)
	if _, err := os.Stat(filepath.Join(dir, logs.LogsFile)); err != nil {
		t.Fatalf("logs.db missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "controller.db")); err == nil {
		t.Fatal("opening the log database must not create the main database")
	}
	err := d.Insert(context.Background(), []logs.Line{{At: time.Now(), Level: slog.LevelInfo, Machine: "m1", Message: "hello"}})
	if err != nil {
		t.Fatal(err)
	}
}

// TestPruneByDayAndSize: whole days older than the cutoff go first, then the
// oldest days of a machine that is over its cap (R6.8).
func TestPruneByDayAndSize(t *testing.T) {
	ctx := context.Background()
	d, _ := open(t)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	var lines []logs.Line
	for day := 0; day < 5; day++ {
		for _, m := range []string{"a", "b"} {
			lines = append(lines, logs.Line{At: now.AddDate(0, 0, -day), Machine: m, Message: "line"})
		}
	}
	if err := d.Insert(ctx, lines); err != nil {
		t.Fatal(err)
	}
	n, err := d.Prune(ctx, now.AddDate(0, 0, -3), 0)
	if err != nil || n != 2 {
		t.Fatalf("age prune: n=%d err=%v, want 2 (day 4 of two machines)", n, err)
	}
	one := lines[0].Size()
	n, err = d.Prune(ctx, now.AddDate(0, 0, -30), 2*one)
	if err != nil || n != 4 {
		t.Fatalf("size prune: n=%d err=%v, want 4 (two oldest days of two machines)", n, err)
	}
	page, err := d.Search(ctx, logs.Query{})
	if err != nil || len(page.Lines) != 4 {
		t.Fatalf("left %d lines, err %v, want 4", len(page.Lines), err)
	}
}
