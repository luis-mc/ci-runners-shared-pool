package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/settings"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

type env struct {
	t      *testing.T
	dir    string
	cfg    string
	db     *store.Store
	svc    *Service
	log    *audit.Log
	set    *settings.Service
	now    time.Time
	opts   Options
	closed bool
}

func healthy(db *store.Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := db.Ping(r.Context()); err != nil {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	return mux
}

func setup(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, dir: dir, cfg: filepath.Join(dir, "config.toml"), db: db, now: time.Now()}
	t.Cleanup(func() {
		if !e.closed {
			_ = db.Close()
		}
	})
	if err := os.WriteFile(e.cfg, []byte("listen_addr = \"127.0.0.1:9000\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.log = audit.New(db)
	e.set = settings.New(db)
	e.opts = Options{
		DB: db, Settings: e.set, Audit: e.log, DataDir: dir, ConfigFile: e.cfg, Version: "1.2.3",
		Now: func() time.Time { return e.now }, Local: time.UTC, DrillHandler: healthy,
	}
	e.svc = New(e.opts)
	return e
}

func (e *env) rebuild() { e.svc = New(e.opts) }

var owner = audit.Actor{Login: "octo", ID: "1", Source: audit.SourceCLI}

func gunzip(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(gz)
	return b
}

func TestArchiveHasDbConfigManifestNoSecret(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	b, err := e.svc.RunBackup(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if b.State != StateGood || b.CheckResult != "ok" || b.SizeBytes == 0 || !strings.HasPrefix(b.File, FilePrefix) || !strings.HasSuffix(b.File, FileSuffix) {
		t.Fatalf("%+v", b)
	}
	m, err := extract(filepath.Join(e.dir, BackupsDir, b.File), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if m.ProductVersion != "1.2.3" || m.SchemaVersion == 0 || len(m.Files) != 2 || m.RowCounts["backups"] != 1 {
		t.Fatalf("manifest: %+v", m)
	}
	if _, ok := m.File(ConfigName); !ok {
		t.Fatal("config missing")
	}

	// A config that holds a secret stays out of the archive, and so does the value.
	secret := "SUPER-SECRET-VALUE-123"
	if err := os.WriteFile(e.cfg, []byte("webhooksecret = \""+secret+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(time.Hour)
	b2, err := e.svc.RunBackup(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.dir, BackupsDir, b2.File)
	m2, err := extract(path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m2.File(ConfigName); ok || !strings.Contains(m2.ConfigSkipped, "secret") {
		t.Fatalf("secret config included: %+v", m2)
	}
	if bytes.Contains(gunzip(t, path), []byte(secret)) {
		t.Fatal("the secret value is in the archive")
	}
}

func TestCorruptBackupDeleted(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	e.opts.afterVacuum = func(p string) {
		f, err := os.OpenFile(p, os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		_, _ = f.WriteAt(bytes.Repeat([]byte{0xFF}, 4096), 4096)
	}
	e.rebuild()
	b, err := e.svc.RunBackup(ctx, owner)
	if err == nil {
		t.Fatal("a corrupt copy must fail the backup")
	}
	if b.State != StateFailed || b.CheckResult == "" {
		t.Fatalf("%+v", b)
	}
	entries, _ := os.ReadDir(filepath.Join(e.dir, BackupsDir))
	for _, en := range entries {
		if strings.HasSuffix(en.Name(), FileSuffix) || strings.HasSuffix(en.Name(), ".part") {
			t.Fatalf("failed backup left a file: %s", en.Name())
		}
	}
	p, _ := e.log.List(ctx, audit.Filter{Action: "backup.run"})
	if len(p.Items) != 1 || p.Items[0].Result != "refused" {
		t.Fatalf("failure not logged: %+v", p.Items)
	}
}

func TestKeep14DaysAndNewestGood(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	base := e.now
	var ids []string
	for i := 0; i < 3; i++ {
		e.now = base.Add(time.Duration(i) * 24 * time.Hour)
		b, err := e.svc.RunBackup(ctx, owner)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, b.ID)
	}
	// 10 days later, all are younger than 14 days.
	e.now = base.Add(10 * 24 * time.Hour)
	if err := e.svc.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	if l, _ := e.db.ListBackups(ctx); len(l) != 3 {
		t.Fatalf("young backups deleted: %d", len(l))
	}
	// 40 days later, all are old; the newest good one stays.
	e.now = base.Add(40 * 24 * time.Hour)
	if err := e.svc.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	l, _ := e.db.ListBackups(ctx)
	if len(l) != 1 {
		t.Fatalf("want only the newest good backup, got %d", len(l))
	}
	left, _ := os.ReadDir(filepath.Join(e.dir, BackupsDir))
	var n int
	for _, en := range left {
		if strings.HasSuffix(en.Name(), FileSuffix) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want 1 archive on disk, got %d", n)
	}
	if l[0].ID == ids[0] {
		t.Fatal("kept the oldest instead of the newest")
	}
}

func TestBackupNowAndList(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	if _, err := e.svc.RunBackup(ctx, owner); err != nil {
		t.Fatal(err)
	}
	l, drill, err := e.svc.List(ctx)
	if err != nil || len(l) != 1 || drill != nil || l[0].CheckResult != "ok" {
		t.Fatalf("l=%+v drill=%v err=%v", l, drill, err)
	}
	// An archive that has a file but no record (made before a restore) is listed.
	src := filepath.Join(e.dir, BackupsDir, l[0].File)
	extra := filepath.Join(e.dir, BackupsDir, FilePrefix+"2020-01-01-0300"+FileSuffix)
	data, _ := os.ReadFile(src)
	if err := os.WriteFile(extra, data, 0o600); err != nil {
		t.Fatal(err)
	}
	l, _, _ = e.svc.List(ctx)
	if len(l) != 2 {
		t.Fatalf("archive in the folder not listed: %d", len(l))
	}
	// A record whose file is gone is dropped from the list.
	_ = os.Remove(src)
	l, _, _ = e.svc.List(ctx)
	if len(l) != 1 || l[0].File != filepath.Base(extra) {
		t.Fatalf("%+v", l)
	}
}

func TestOnlyOneBackupOrRestoreAtATime(t *testing.T) {
	e := setup(t)
	unlock, err := e.svc.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := e.svc.RunBackup(context.Background(), owner); !errors.Is(err, ErrAlreadyActive) {
		t.Fatalf("got %v", err)
	}
}

func TestRestoreChecksManifestAndBacksUpFirst(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	// State A, then a backup of it.
	_ = e.log.Record(ctx, owner, "thing.a", audit.Target{}, nil)
	a, err := e.svc.RunBackup(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	// State B is newer than the backup.
	_ = e.log.Record(ctx, owner, "thing.b", audit.Target{}, nil)
	if err := os.WriteFile(e.cfg, []byte("listen_addr = \"127.0.0.1:9999\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Without confirm: names what changes, changes nothing.
	e.now = e.now.Add(time.Hour)
	plan, err := e.svc.Restore(ctx, owner, a.ID, false)
	var nc *NeedsConfirmation
	if !errors.As(err, &nc) || !strings.Contains(err.Error(), "the database") || !strings.Contains(err.Error(), "the config file") {
		t.Fatalf("want needs confirmation naming what goes: %v", err)
	}
	if plan.BackupID != a.ID || len(plan.Replaces) != 2 {
		t.Fatalf("%+v", plan)
	}
	if _, err := os.Stat(filepath.Join(e.dir, pendingDir)); err == nil {
		t.Fatal("staged without a yes")
	}
	if l, _ := e.db.ListBackups(ctx); len(l) != 1 {
		t.Fatalf("backed up without a yes: %d", len(l))
	}

	// With confirm: backs up the current state first, stages, restarts.
	restarted := false
	e.opts.Restart = func() { restarted = true }
	e.rebuild()
	e.now = e.now.Add(time.Hour)
	if _, err := e.svc.Restore(ctx, owner, a.ID, true); err != nil {
		t.Fatal(err)
	}
	if !restarted {
		t.Fatal("no restart asked for")
	}
	if l, _ := e.db.ListBackups(ctx); len(l) != 2 {
		t.Fatalf("current state not backed up first: %d", len(l))
	}

	// At the next start the staged files go in place.
	e.closed = true
	_ = e.db.Close()
	ok, err := ApplyPending(e.dir, e.cfg)
	if err != nil || !ok {
		t.Fatalf("apply: %v %v", ok, err)
	}
	if again, _ := ApplyPending(e.dir, e.cfg); again {
		t.Fatal("applied twice")
	}
	db2, err := store.Open(ctx, e.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db2.Close() }()
	p, _ := audit.New(db2).List(ctx, audit.Filter{Action: "thing.b"})
	if len(p.Items) != 0 {
		t.Fatal("state B survived the restore")
	}
	if p, _ := audit.New(db2).List(ctx, audit.Filter{Action: "thing.a"}); len(p.Items) != 1 {
		t.Fatal("state A is missing after the restore")
	}
	got, _ := os.ReadFile(e.cfg)
	if !strings.Contains(string(got), "9000") {
		t.Fatalf("config not restored: %s", got)
	}
	if _, err := os.Stat(e.cfg + ".before-restore"); err != nil {
		t.Fatal("old config not kept")
	}
}

func TestRestoreRefusesNewerSchemaAndTamperedArchive(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	b, err := e.svc.RunBackup(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.dir, BackupsDir, b.File)

	// Newer schema: rewrite the archive with a manifest that says 9999.
	work := t.TempDir()
	m, err := extract(path, work)
	if err != nil {
		t.Fatal(err)
	}
	m.SchemaVersion = 9999
	newer := filepath.Join(e.dir, BackupsDir, FilePrefix+"2099-01-01-0000"+FileSuffix)
	if err := writeArchive(newer, work, m); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Restore(ctx, owner, filepath.Base(newer), true)
	if !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("want ErrNewerSchema, got %v", err)
	}

	// Tampered database: the SHA-256 no longer matches.
	raw, err := os.ReadFile(filepath.Join(work, DBName))
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0xFF // same size, different bytes
	if err := os.WriteFile(filepath.Join(work, DBName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(e.dir, BackupsDir, FilePrefix+"2098-01-01-0000"+FileSuffix)
	m.SchemaVersion = 1
	if err := writeArchive(bad, work, m); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Restore(ctx, owner, filepath.Base(bad), true)
	if !errors.Is(err, ErrBadArchive) || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("want a SHA-256 refusal, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, pendingDir)); err == nil {
		t.Fatal("a refused restore left staged files")
	}
	if _, err := e.svc.Restore(ctx, owner, "nope", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	p, _ := e.log.List(ctx, audit.Filter{Action: "backup.restore"})
	if len(p.Items) < 2 || p.Items[0].Result != "refused" {
		t.Fatalf("refusals not logged: %+v", p.Items)
	}
}

type fakeSecrets struct{ have map[secrets.Key]bool }

func (f fakeSecrets) Get(_ context.Context, k secrets.Key) ([]byte, error) {
	if f.have[k] {
		return []byte("x"), nil
	}
	return nil, secrets.ErrNotFound
}
func (f fakeSecrets) Put(context.Context, secrets.Key, []byte) error { return nil }
func (f fakeSecrets) Delete(context.Context, secrets.Key) error      { return nil }
func (f fakeSecrets) Backend() string                                { return "fake" }

func TestDoctorListsMissingSecrets(t *testing.T) {
	sec := fakeSecrets{have: map[secrets.Key]bool{"a": true}}
	got, err := MissingSecrets(context.Background(), sec, []secrets.Key{"a", "b", "c"})
	if err != nil || len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestDrillRecordsResult(t *testing.T) {
	ctx := context.Background()
	e := setup(t)
	if _, err := e.svc.RunDrill(ctx, owner); !errors.Is(err, ErrNoGoodBackup) {
		t.Fatalf("got %v", err)
	}
	if _, err := e.svc.RunBackup(ctx, owner); err != nil {
		t.Fatal(err)
	}
	d, err := e.svc.RunDrill(ctx, owner)
	if err != nil || d.State != "passed" || d.FinishedAt.IsZero() || !strings.Contains(d.Detail, "healthy") {
		t.Fatalf("%+v %v", d, err)
	}
	if _, drill, _ := e.svc.List(ctx); drill == nil || drill.ID != d.ID {
		t.Fatal("last drill not listed")
	}
	left, _ := os.ReadDir(filepath.Join(e.dir, BackupsDir))
	for _, en := range left {
		if strings.HasPrefix(en.Name(), ".") {
			t.Fatalf("drill left a folder: %s", en.Name())
		}
	}

	// An unhealthy restored controller fails the drill and says why.
	e.opts.DrillHandler = func(*store.Store) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) })
	}
	e.rebuild()
	d, err = e.svc.RunDrill(ctx, owner)
	if err == nil || d.State != "failed" || !strings.Contains(d.Detail, "503") {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestDoctorWarnsAfter90Days(t *testing.T) {
	now := time.Now()
	if !DrillOverdue(now, nil) {
		t.Error("no drill must warn")
	}
	fresh := &store.Drill{State: "passed", FinishedAt: now.Add(-89 * 24 * time.Hour)}
	if DrillOverdue(now, fresh) {
		t.Error("89 days must not warn")
	}
	old := &store.Drill{State: "passed", FinishedAt: now.Add(-91 * 24 * time.Hour)}
	if !DrillOverdue(now, old) {
		t.Error("91 days must warn")
	}
	if !DrillOverdue(now, &store.Drill{State: "failed", FinishedAt: now}) {
		t.Error("a failed drill must warn")
	}
}

func TestNextRunIsTheNextLocalTime(t *testing.T) {
	loc := time.FixedZone("x", 2*3600)
	now := time.Date(2026, 11, 14, 2, 59, 0, 0, loc)
	if got := NextRun(now, "03:00", loc); !got.Equal(time.Date(2026, 11, 14, 3, 0, 0, 0, loc)) {
		t.Error(got)
	}
	now = time.Date(2026, 11, 14, 3, 0, 0, 0, loc)
	if got := NextRun(now, "03:00", loc); !got.Equal(time.Date(2026, 11, 15, 3, 0, 0, 0, loc)) {
		t.Error(got)
	}
}

func TestScheduledRunWritesBackup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := setup(t)
	// "Now" is 1.5 seconds before the configured time, so the timer fires fast.
	loc := time.UTC
	target := time.Now().UTC().Add(1500 * time.Millisecond)
	hhmm := target.Format("15:04")
	if _, err := e.set.Update(ctx, owner, settings.Patch{BackupTimeLocal: &hhmm}); err != nil {
		t.Fatal(err)
	}
	e.opts.Local = loc
	fire := time.Date(target.Year(), target.Month(), target.Day(), target.Hour(), target.Minute(), 0, 0, loc)
	e.opts.Now = func() time.Time { return fire.Add(-1500 * time.Millisecond) }
	e.opts.gap = time.Hour
	e.rebuild()
	go e.svc.Run(ctx)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	ctx, stop := context.WithTimeout(ctx, 6*time.Second)
	defer stop()
	for {
		if l, _ := e.db.ListBackups(ctx); len(l) == 1 && l[0].State == StateGood {
			return
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			t.Fatal("the scheduled backup did not run")
		}
	}
}
