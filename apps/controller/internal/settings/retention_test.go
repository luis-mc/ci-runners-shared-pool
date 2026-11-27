package settings

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

func retention(t *testing.T) (*Retention, *audit.Log, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.OpenForTests(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	l := audit.New(db)
	return &Retention{DB: db, Settings: New(db), Audit: l, DataDir: dir}, l, dir
}

func TestHourlyJobDeletesPastLimit(t *testing.T) {
	ctx := context.Background()
	r, l, dir := retention(t)
	_ = l.Record(ctx, audit.System, "old.thing", audit.Target{}, nil)

	logs := filepath.Join(dir, LogsDir)
	_ = os.MkdirAll(filepath.Join(logs, "m1"), 0o755)
	old := filepath.Join(logs, "m1", "old.log")
	fresh := filepath.Join(logs, "m1", "fresh.log")
	_ = os.WriteFile(old, []byte("x"), 0o600)
	_ = os.WriteFile(fresh, []byte("y"), 0o600)
	past := time.Now().Add(-20 * 24 * time.Hour)
	_ = os.Chtimes(old, past, past)

	// Today: audit (400 days) keeps its row; logs (14 days) lose the old file.
	if err := r.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("a log older than 14 days stayed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("a fresh log was deleted")
	}
	p, _ := l.List(ctx, audit.Filter{Action: "old.thing"})
	if len(p.Items) != 1 {
		t.Fatal("audit row deleted too early")
	}

	// 401 days later the audit row is past its limit; retention writes its own row.
	r.Now = func() time.Time { return time.Now().Add(401 * 24 * time.Hour) }
	if err := r.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if p, _ := l.List(ctx, audit.Filter{Action: "old.thing"}); len(p.Items) != 0 {
		t.Error("an audit row past 400 days stayed")
	}
	p, _ = l.List(ctx, audit.Filter{Action: "audit.retention"})
	if len(p.Items) == 0 {
		t.Fatal("retention wrote no row")
	}
}

func TestRetentionUsesSettings(t *testing.T) {
	ctx := context.Background()
	r, l, _ := retention(t)
	_ = l.Record(ctx, audit.System, "thing", audit.Target{}, nil)
	days := 90 // the audit floor
	if _, err := r.Settings.Update(ctx, owner, Patch{RetentionAuditDays: &days}); err != nil {
		t.Fatal(err)
	}
	r.Now = func() time.Time { return time.Now().Add(95 * 24 * time.Hour) }
	if err := r.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if p, _ := l.List(ctx, audit.Filter{Action: "thing"}); len(p.Items) != 0 {
		t.Error("the changed limit was not used")
	}
}

func TestLogSizeCapPerMachineOldestFirst(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "m1"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "m2"), 0o755)
	mk := func(p string, size int, age time.Duration) {
		_ = os.WriteFile(p, []byte(strings.Repeat("a", size)), 0o600)
		at := time.Now().Add(-age)
		_ = os.Chtimes(p, at, at)
	}
	mk(filepath.Join(dir, "m1", "a"), 600, 3*time.Hour)
	mk(filepath.Join(dir, "m1", "b"), 600, 2*time.Hour)
	mk(filepath.Join(dir, "m1", "c"), 600, time.Hour)
	mk(filepath.Join(dir, "m2", "a"), 600, 3*time.Hour)
	mk(filepath.Join(dir, "top.log"), 5000, time.Hour)
	if err := PruneLogs(dir, time.Now().Add(-24*time.Hour), 1300); err != nil {
		t.Fatal(err)
	}
	exists := func(p string) bool { _, err := os.Stat(filepath.Join(dir, p)); return err == nil }
	if exists("m1/a") || !exists("m1/b") || !exists("m1/c") {
		t.Error("m1 must lose only its oldest file")
	}
	if !exists("m2/a") || !exists("top.log") {
		t.Error("another machine's logs and the top folder must stay")
	}
	if err := PruneLogs(filepath.Join(dir, "missing"), time.Now(), 1); err != nil {
		t.Errorf("a missing folder is not an error: %v", err)
	}
}

func TestRunRepeats(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r, _, dir := retention(t)
	r.Interval = time.Millisecond
	// Each run reads the clock once, at its start, so the clock counts the runs
	// without any sleeping or polling in the test.
	started := make(chan struct{}, 1024)
	r.Now = func() time.Time {
		select {
		case started <- struct{}{}:
		default:
		}
		return time.Now()
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Run(ctx)
	}()
	// The goroutine must end before the database and the folder are removed:
	// on Windows a run still holding the file blocks the folder's removal.
	defer func() {
		cancel()
		<-done
	}()
	await := func(n int) {
		t.Helper()
		for range n {
			select {
			case <-started:
			case <-time.After(time.Minute):
				t.Fatal("retention did not run again")
			}
		}
	}
	// The first run is over once the second starts.
	await(2)
	// A file that turns old after the first run is deleted by a later run.
	logs := filepath.Join(dir, LogsDir)
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(logs, "late.log")
	if err := os.WriteFile(old, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	// Starts that were queued before the file was old do not count: empty the
	// queue, then wait for two runs that both began after it. The second begins
	// after a whole run saw the file.
	for drained := false; !drained; {
		select {
		case <-started:
		default:
			drained = true
		}
	}
	await(2)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("retention did not delete the old file: %v", err)
	}
}

func TestRetentionPrunesJobsSection(t *testing.T) {
	ctx := context.Background()
	r, _, _ := retention(t)
	jobs := r.DB.Jobs()
	now := time.Now().UTC()
	old := now.Add(-100 * 24 * time.Hour)
	if err := jobs.UpsertJob(ctx, jobsdata.Job{
		ID: 1, RepositoryID: 1, RunID: 1, Attempt: 1, Name: "t", JobType: "t", State: jobsdata.Done, Result: jobsdata.Success,
		CreatedAt: old, StartedAt: old.Add(time.Second), CompletedAt: old.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := jobs.GetJob(ctx, 1); ok {
		t.Error("a job past 90 days stayed")
	}
	sum, err := jobs.Summaries(ctx, old.Add(-48*time.Hour), now)
	if err != nil || len(sum) != 1 || sum[0].Jobs != 1 {
		t.Errorf("summary after prune: %v %+v", err, sum)
	}
}
