package backup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/secrets"
	"github.com/luis-mc/ci-runners-shared-pool/internal/settings"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

// Backup, Drill and DB are the store types the API shows and passes along, so
// internal/api never imports internal/store (STACKSPEC 3.2).
type (
	Backup = store.Backup
	Drill  = store.Drill
	DB     = store.Store
)

// OpenDB opens the controller database in dataDir.
func OpenDB(ctx context.Context, dataDir string) (*DB, error) { return store.Open(ctx, dataDir) }

// OpenDBForTests is OpenDB without the disk flushes (store.OpenForTests), for
// tests in packages that may not import internal/store. Production code uses OpenDB.
func OpenDBForTests(ctx context.Context, dataDir string) (*DB, error) {
	return store.OpenForTests(ctx, dataDir)
}

// Errors the API maps to status codes.
var (
	ErrNotFound      = store.ErrNotFound
	ErrNewerSchema   = errors.New("this backup is from a newer version than this program; update the program first")
	ErrNoGoodBackup  = errors.New("there is no good backup yet; run a backup first")
	ErrAlreadyActive = errors.New("a backup or restore is already running")
)

// BackupsDir is the folder inside the data dir that holds the archives.
const BackupsDir = "backups"

// Backup states in the database.
const (
	StateWriting = "writing"
	StateGood    = "good"
	StateFailed  = "failed"
)

// DrillOverdueAfter is how long the doctor waits before it warns that no drill
// has passed (operations R5.27).
const DrillOverdueAfter = 90 * 24 * time.Hour

// Options are the dependencies of the service.
type Options struct {
	DB       *store.Store
	Settings *settings.Service
	Audit    audit.Recorder
	// DataDir holds the database and backups/. ConfigFile is the config.toml to include.
	DataDir    string
	ConfigFile string
	// Version is the product version written into manifests.
	Version string
	Logger  *slog.Logger
	// Restart is called after a restore has been staged. The service manager
	// brings the controller back and it applies the staged files at start. A
	// nil Restart leaves the restart to the caller.
	Restart func()
	// DrillHandler builds the controller's HTTP handler over a database, used by
	// the drill's temporary controller. It must make no GitHub call.
	DrillHandler func(db *store.Store) http.Handler
	// Now is time.Now except in tests; Local is the time zone of the backup time.
	Now   func() time.Time
	Local *time.Location

	// afterVacuum lets a test damage the copy before it is checked.
	afterVacuum func(path string)
	// gap overrides defaultMinGap in tests.
	gap time.Duration
}

// Service runs backups, restores and drills.
type Service struct {
	o    Options
	busy chan struct{}
}

// New returns the backup service.
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Local == nil {
		o.Local = time.Local
	}
	return &Service{o: o, busy: make(chan struct{}, 1)}
}

func (s *Service) minGap() time.Duration {
	if s.o.gap > 0 {
		return s.o.gap
	}
	return defaultMinGap
}

func (s *Service) dir() string { return filepath.Join(s.o.DataDir, BackupsDir) }

func (s *Service) lock() (func(), error) {
	select {
	case s.busy <- struct{}{}:
		return func() { <-s.busy }, nil
	default:
		return nil, ErrAlreadyActive
	}
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// archiveName picks a file name that no file and no record uses yet: minute
// resolution first, then seconds, then a random suffix.
func (s *Service) archiveName(ctx context.Context) string {
	t := s.o.Now().In(s.o.Local)
	taken := func(name string) bool {
		if _, err := os.Stat(filepath.Join(s.dir(), name)); err == nil {
			return true
		}
		_, err := s.o.DB.GetBackup(ctx, idOf(name))
		return err == nil
	}
	name := FilePrefix + t.Format("2006-01-02-1504") + FileSuffix
	if !taken(name) {
		return name
	}
	name = FilePrefix + t.Format("2006-01-02-150405") + FileSuffix
	for taken(name) {
		name = FilePrefix + t.Format("2006-01-02-150405") + "-" + newID()[:4] + FileSuffix
	}
	return name
}

func idOf(file string) string { return strings.TrimSuffix(file, FileSuffix) }

// RunBackup writes one backup now (operations R5.21, R5.22, R5.24). A backup
// whose copy fails the integrity check is deleted and reported as failed.
func (s *Service) RunBackup(ctx context.Context, actor audit.Actor) (store.Backup, error) {
	unlock, err := s.lock()
	if err != nil {
		return store.Backup{}, err
	}
	defer unlock()
	return s.runBackupLocked(ctx, actor)
}

func (s *Service) runBackupLocked(ctx context.Context, actor audit.Actor) (store.Backup, error) {
	if err := os.MkdirAll(s.dir(), 0o700); err != nil {
		return store.Backup{}, fmt.Errorf("backup folder: %w", err)
	}
	name := s.archiveName(ctx)
	id := idOf(name)
	final := filepath.Join(s.dir(), name)
	schema, err := s.o.DB.SchemaVersion(ctx)
	if err != nil {
		return store.Backup{}, err
	}
	if err := s.o.DB.InsertBackup(ctx, id, name, schema); err != nil {
		return store.Backup{}, fmt.Errorf("record backup: %w", err)
	}
	fail := func(cause error) (store.Backup, error) {
		_ = os.Remove(final)
		_ = os.Remove(final + ".part")
		_ = s.o.DB.FinishBackup(context.WithoutCancel(ctx), id, StateFailed, 0, cause.Error())
		s.o.Logger.Error("backup failed", "id", id, "err", cause)
		_ = s.o.Audit.RecordRefused(context.WithoutCancel(ctx), actor, "backup.run", audit.Target{Type: "backup", ID: id},
			map[string]any{"summary": "backup failed: " + cause.Error()})
		b, _ := s.o.DB.GetBackup(context.WithoutCancel(ctx), id)
		return b, fmt.Errorf("backup failed: %w", cause)
	}

	work, err := os.MkdirTemp(s.dir(), ".work-")
	if err != nil {
		return fail(err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	dbCopy := filepath.Join(work, DBName)
	if err := s.o.DB.VacuumInto(ctx, dbCopy); err != nil {
		return fail(err)
	}
	if s.o.afterVacuum != nil {
		s.o.afterVacuum(dbCopy)
	}
	insp, err := store.Inspect(ctx, dbCopy)
	if err != nil {
		return fail(err)
	}
	if insp.IntegrityCheck != "ok" {
		return fail(fmt.Errorf("integrity check: %s", insp.IntegrityCheck))
	}

	m := Manifest{ProductVersion: s.o.Version, SchemaVersion: insp.SchemaVersion, CreatedAt: s.o.Now().UTC().Truncate(time.Second), RowCounts: insp.RowCounts}
	names := []string{DBName}
	if s.o.ConfigFile != "" {
		if data, rerr := os.ReadFile(s.o.ConfigFile); rerr == nil {
			if cerr := configIsClean(data); cerr != nil {
				m.ConfigSkipped = "config left out: " + cerr.Error()
			} else if werr := os.WriteFile(filepath.Join(work, ConfigName), data, 0o600); werr != nil {
				return fail(werr)
			} else {
				names = append(names, ConfigName)
			}
		}
	}
	for _, n := range names {
		sum, size, herr := hashFile(filepath.Join(work, n))
		if herr != nil {
			return fail(herr)
		}
		m.Files = append(m.Files, ManifestFile{Name: n, SHA256: sum, Size: size})
	}
	part := final + ".part"
	if err := writeArchive(part, work, m); err != nil {
		return fail(err)
	}
	if err := os.Rename(part, final); err != nil {
		return fail(err)
	}
	// R5.22: open the written archive and check the database inside it.
	check := filepath.Join(work, "check")
	if _, err := extract(final, check); err != nil {
		return fail(err)
	}
	post, err := store.Inspect(ctx, filepath.Join(check, DBName))
	if err != nil {
		return fail(err)
	}
	if post.IntegrityCheck != "ok" {
		return fail(fmt.Errorf("integrity check of the written archive: %s", post.IntegrityCheck))
	}
	st, err := os.Stat(final)
	if err != nil {
		return fail(err)
	}
	if err := s.o.DB.FinishBackup(ctx, id, StateGood, st.Size(), "ok"); err != nil {
		return fail(err)
	}
	if err := s.o.Audit.Record(ctx, actor, "backup.run", audit.Target{Type: "backup", ID: id},
		map[string]any{"summary": "backup written: " + name, "size_bytes": st.Size()}); err != nil {
		s.o.Logger.Error("backup audit", "err", err)
	}
	if err := s.prune(ctx); err != nil {
		s.o.Logger.Warn("backup prune", "err", err)
	}
	return s.o.DB.GetBackup(ctx, id)
}

// Prune deletes archives older than the keep days after a good one exists. The
// newest good backup always stays (operations R5.23).
func (s *Service) Prune(ctx context.Context) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	return s.prune(ctx)
}

func (s *Service) prune(ctx context.Context) error {
	st, err := s.o.Settings.Get(ctx)
	if err != nil {
		return err
	}
	list, err := s.o.DB.ListBackups(ctx)
	if err != nil {
		return err
	}
	cutoff := s.o.Now().Add(-time.Duration(st.BackupKeepDays) * 24 * time.Hour)
	newestGood := ""
	for _, b := range list { // newest first
		if b.State == StateGood {
			newestGood = b.ID
			break
		}
	}
	if newestGood == "" {
		return nil
	}
	for _, b := range list {
		if b.ID == newestGood || b.State == StateWriting || !b.CreatedAt.Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(s.dir(), b.File)); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.o.Logger.Warn("delete old backup", "file", b.File, "err", err)
			continue
		}
		if err := s.o.DB.MarkBackupDeleted(ctx, b.ID); err != nil {
			return err
		}
	}
	return nil
}

// List returns the backups newest first and the last drill. It first matches
// the folder with the records, so archives made before a restore show up
// (operations R5.24).
func (s *Service) List(ctx context.Context) ([]store.Backup, *store.Drill, error) {
	if err := s.reconcile(ctx); err != nil {
		s.o.Logger.Warn("backup reconcile", "err", err)
	}
	list, err := s.o.DB.ListBackups(ctx)
	if err != nil {
		return nil, nil, err
	}
	var drill *store.Drill
	if d, err := s.o.DB.LastDrill(ctx); err == nil {
		drill = &d
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, nil, err
	}
	return list, drill, nil
}

func (s *Service) reconcile(ctx context.Context) error {
	entries, err := os.ReadDir(s.dir())
	if errors.Is(err, os.ErrNotExist) {
		entries = nil
	} else if err != nil {
		return err
	}
	list, err := s.o.DB.ListBackups(ctx)
	if err != nil {
		return err
	}
	known := map[string]store.Backup{}
	for _, b := range list {
		known[b.File] = b
	}
	onDisk := map[string]bool{}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, FilePrefix) || !strings.HasSuffix(n, FileSuffix) {
			continue
		}
		onDisk[n] = true
		if _, ok := known[n]; ok {
			continue
		}
		tmp, err := os.MkdirTemp(s.dir(), ".work-")
		if err != nil {
			return err
		}
		m, err := extract(filepath.Join(s.dir(), n), tmp)
		_ = os.RemoveAll(tmp)
		if err != nil {
			continue // not a valid archive; leave it alone
		}
		st, _ := os.Stat(filepath.Join(s.dir(), n))
		if err := s.o.DB.InsertBackup(ctx, idOf(n), n, m.SchemaVersion); err != nil {
			return err
		}
		if err := s.o.DB.FinishBackup(ctx, idOf(n), StateGood, st.Size(), "ok (found in the folder)"); err != nil {
			return err
		}
	}
	for _, b := range list {
		if b.State != StateWriting && !onDisk[b.File] && b.State == StateGood {
			_ = s.o.DB.MarkBackupDeleted(ctx, b.ID)
		}
	}
	return nil
}

// ------------------------------------------------------------------ restore

// Plan says what a restore replaces (operations R5.25).
type Plan struct {
	BackupID      string
	File          string
	CreatedAt     time.Time
	SchemaVersion int64
	// Replaces names what goes, for the question the user answers.
	Replaces []string
	// IncludesConfig is false when the archive carries no config file.
	IncludesConfig bool
}

// NeedsConfirmation is returned until the caller passes confirm=true. Its text
// names what changes.
type NeedsConfirmation struct{ Plan Plan }

func (e *NeedsConfirmation) Error() string {
	return "restoring replaces " + strings.Join(e.Plan.Replaces, " and ") +
		". The current state is backed up first. Confirm to continue."
}

// Resolve returns the archive path for a backup id or a file path.
func (s *Service) resolve(ctx context.Context, idOrFile string) (string, string, error) {
	if filepath.IsAbs(idOrFile) || strings.ContainsAny(idOrFile, `/\`) {
		if _, err := os.Stat(idOrFile); err != nil {
			return "", "", ErrNotFound
		}
		return idOrFile, idOf(filepath.Base(idOrFile)), nil
	}
	if b, err := s.o.DB.GetBackup(ctx, idOrFile); err == nil {
		return filepath.Join(s.dir(), b.File), b.ID, nil
	}
	p := filepath.Join(s.dir(), filepath.Base(idOrFile))
	if _, err := os.Stat(p); err == nil && strings.HasSuffix(p, FileSuffix) {
		return p, idOf(filepath.Base(p)), nil
	}
	return "", "", ErrNotFound
}

// Restore checks a backup, and with confirm=true backs up the current state,
// stages the backup and asks the controller to restart (operations R5.25). The
// staged files are put in place at the next start by ApplyPending, so a crash
// half way leaves the old state and the step can run again (R6.9).
func (s *Service) Restore(ctx context.Context, actor audit.Actor, idOrFile string, confirm bool) (Plan, error) {
	unlock, err := s.lock()
	if err != nil {
		return Plan{}, err
	}
	defer unlock()

	path, id, err := s.resolve(ctx, idOrFile)
	if err != nil {
		return Plan{}, err
	}
	stage := filepath.Join(s.o.DataDir, pendingDir+".tmp")
	_ = os.RemoveAll(stage)
	m, err := extract(path, stage)
	if err != nil {
		_ = os.RemoveAll(stage)
		_ = s.o.Audit.RecordRefused(ctx, actor, "backup.restore", audit.Target{Type: "backup", ID: id}, map[string]any{"summary": err.Error()})
		return Plan{}, err
	}
	refuse := func(err error) (Plan, error) {
		_ = os.RemoveAll(stage)
		_ = s.o.Audit.RecordRefused(ctx, actor, "backup.restore", audit.Target{Type: "backup", ID: id}, map[string]any{"summary": err.Error()})
		return Plan{}, err
	}
	max, err := store.MaxSchemaVersion()
	if err != nil {
		return refuse(err)
	}
	if m.SchemaVersion > max {
		return refuse(ErrNewerSchema)
	}
	insp, err := store.Inspect(ctx, filepath.Join(stage, DBName))
	if err != nil {
		return refuse(err)
	}
	if insp.IntegrityCheck != "ok" {
		return refuse(fmt.Errorf("%w: integrity check: %s", ErrBadArchive, insp.IntegrityCheck))
	}
	_, hasConfig := m.File(ConfigName)
	plan := Plan{BackupID: id, File: filepath.Base(path), CreatedAt: m.CreatedAt, SchemaVersion: m.SchemaVersion, IncludesConfig: hasConfig}
	plan.Replaces = []string{fmt.Sprintf("the database (all data after %s)", m.CreatedAt.In(s.o.Local).Format("2006-01-02 15:04"))}
	if hasConfig {
		plan.Replaces = append(plan.Replaces, "the config file")
	}
	if !confirm {
		_ = os.RemoveAll(stage)
		return plan, &NeedsConfirmation{Plan: plan}
	}

	// Back up the current state first; a failure stops the restore.
	if _, err := s.runBackupLocked(ctx, actor); err != nil {
		return refuse(fmt.Errorf("the current state could not be backed up, so nothing was changed: %w", err))
	}
	final := filepath.Join(s.o.DataDir, pendingDir)
	_ = os.RemoveAll(final)
	if err := os.Rename(stage, final); err != nil {
		return refuse(err)
	}
	if err := s.o.Audit.Record(ctx, actor, "backup.restore", audit.Target{Type: "backup", ID: id},
		map[string]any{"summary": "restore staged from " + plan.File + "; the controller restarts"}); err != nil {
		_ = os.RemoveAll(final)
		return Plan{}, err
	}
	if s.o.Restart != nil {
		s.o.Restart()
	}
	return plan, nil
}

const pendingDir = "restore-pending"

// ApplyPending puts a staged restore in place. Call it at start, before the
// database is opened. The old database and config are kept next to the new
// ones as *.before-restore until the next restore. Each step can run again.
func ApplyPending(dataDir, configFile string) (applied bool, err error) {
	stage := filepath.Join(dataDir, pendingDir)
	if _, err := os.Stat(filepath.Join(stage, DBName)); err != nil {
		return false, nil
	}
	db := filepath.Join(dataDir, store.DatabaseFile)
	// Keep the old files, then put the new ones in place with a rename.
	for _, ext := range []string{"", "-wal", "-shm"} {
		old := db + ext
		if _, serr := os.Stat(old); serr == nil {
			if err := replaceFile(old, old+".before-restore"); err != nil {
				return false, err
			}
		}
	}
	if err := copyFile(filepath.Join(stage, DBName), db); err != nil {
		return false, err
	}
	if cfg := filepath.Join(stage, ConfigName); configFile != "" {
		if _, serr := os.Stat(cfg); serr == nil {
			if _, serr := os.Stat(configFile); serr == nil {
				if err := copyFile(configFile, configFile+".before-restore"); err != nil {
					return false, err
				}
			}
			if err := os.MkdirAll(filepath.Dir(configFile), 0o755); err != nil {
				return false, err
			}
			if err := copyFile(cfg, configFile); err != nil {
				return false, err
			}
		}
	}
	return true, os.RemoveAll(stage)
}

func replaceFile(from, to string) error {
	_ = os.Remove(to)
	return os.Rename(from, to)
}

func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	tmp := to + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, to)
}

// MissingSecrets lists the secrets the controller needs that the OS store does
// not hold, for the doctor after a restore on a new machine (operations R5.26).
func MissingSecrets(ctx context.Context, sec secrets.Store, wanted []secrets.Key) ([]secrets.Key, error) {
	var missing []secrets.Key
	for _, k := range wanted {
		if _, err := sec.Get(ctx, k); err != nil {
			if errors.Is(err, secrets.ErrNotFound) {
				missing = append(missing, k)
				continue
			}
			return nil, err
		}
	}
	return missing, nil
}

// -------------------------------------------------------------------- drill

// RunDrill restores the newest good backup into a temporary folder, starts a
// controller there on a free local port, checks its health and the row counts
// against the manifest, records the result and deletes the folder
// (operations R5.27). The temporary controller makes no GitHub call.
func (s *Service) RunDrill(ctx context.Context, actor audit.Actor) (store.Drill, error) {
	unlock, err := s.lock()
	if err != nil {
		return store.Drill{}, err
	}
	defer unlock()

	list, _, err := s.List(ctx)
	if err != nil {
		return store.Drill{}, err
	}
	var newest *store.Backup
	for i := range list {
		if list[i].State == StateGood {
			newest = &list[i]
			break
		}
	}
	if newest == nil {
		return store.Drill{}, ErrNoGoodBackup
	}
	id := newID()
	if err := s.o.DB.InsertDrill(ctx, id, newest.ID); err != nil {
		return store.Drill{}, err
	}
	detail, derr := s.drill(ctx, filepath.Join(s.dir(), newest.File))
	state := "passed"
	if derr != nil {
		state, detail = "failed", derr.Error()
	}
	if err := s.o.DB.FinishDrill(context.WithoutCancel(ctx), id, state, detail); err != nil {
		return store.Drill{}, err
	}
	res := audit.ResultDone
	if derr != nil {
		res = audit.ResultRefused
	}
	rec := s.o.Audit.Record
	if res == audit.ResultRefused {
		rec = s.o.Audit.RecordRefused
	}
	_ = rec(context.WithoutCancel(ctx), actor, "backup.drill", audit.Target{Type: "backup", ID: newest.ID},
		map[string]any{"summary": "restore drill " + state + ": " + detail})
	d, err := s.o.DB.LastDrill(ctx)
	if err != nil {
		return store.Drill{}, err
	}
	return d, derr
}

func (s *Service) drill(ctx context.Context, archive string) (string, error) {
	tmp, err := os.MkdirTemp(s.dir(), ".drill-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	m, err := extract(archive, tmp)
	if err != nil {
		return "", err
	}
	insp, err := store.Inspect(ctx, filepath.Join(tmp, DBName))
	if err != nil {
		return "", err
	}
	if insp.IntegrityCheck != "ok" {
		return "", fmt.Errorf("integrity check: %s", insp.IntegrityCheck)
	}
	for table, want := range m.RowCounts {
		if got := insp.RowCounts[table]; got != want {
			return "", fmt.Errorf("table %s has %d rows, the manifest says %d", table, got, want)
		}
	}
	for table := range insp.RowCounts {
		if _, ok := m.RowCounts[table]; !ok {
			return "", fmt.Errorf("table %s is not in the manifest", table)
		}
	}
	if s.o.DrillHandler == nil {
		return "", errors.New("no controller to start for the drill")
	}
	data := filepath.Join(tmp, "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		return "", err
	}
	if err := copyFile(filepath.Join(tmp, DBName), filepath.Join(data, store.DatabaseFile)); err != nil {
		return "", err
	}
	db, err := store.Open(ctx, data)
	if err != nil {
		return "", fmt.Errorf("the restored database does not open: %w", err)
	}
	defer func() { _ = db.Close() }()
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	srv := &http.Server{Handler: s.o.DrillHandler(db), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()
	hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(hctx, http.MethodGet, "http://"+ln.Addr().String()+"/healthz", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("the restored controller did not answer: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the restored controller is not healthy: HTTP %d", resp.StatusCode)
	}
	return fmt.Sprintf("restored %s, healthy, %d tables match the manifest", filepath.Base(archive), len(m.RowCounts)), nil
}

// DrillOverdue reports whether the doctor should warn: no passed drill in 90
// days (operations R5.27). last may be nil.
func DrillOverdue(now time.Time, last *store.Drill) bool {
	return last == nil || last.State != "passed" || now.Sub(last.FinishedAt) > DrillOverdueAfter
}

// ----------------------------------------------------------------- schedule

// defaultMinGap is the shortest wait between two scheduled runs.
const defaultMinGap = time.Minute

// NextRun returns the next time of day hhmm (local) after now.
func NextRun(now time.Time, hhmm string, loc *time.Location) time.Time {
	var h, m int
	_, _ = fmt.Sscanf(hhmm, "%d:%d", &h, &m)
	n := now.In(loc)
	t := time.Date(n.Year(), n.Month(), n.Day(), h, m, 0, 0, loc)
	if !t.After(n) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

// Run writes the daily backup at the configured local time until ctx ends
// (operations R5.21). The time is read again before each wait, so a change in
// Settings applies to the next run.
func (s *Service) Run(ctx context.Context) {
	for {
		st, err := s.o.Settings.Get(ctx)
		if err != nil {
			s.o.Logger.Error("backup schedule: settings", "err", err)
			st.BackupTimeLocal = settings.Defaults.BackupTimeLocal
		}
		next := NextRun(s.o.Now(), st.BackupTimeLocal, s.o.Local)
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if _, err := s.RunBackup(ctx, audit.System); err != nil {
			s.o.Logger.Error("daily backup", "err", err)
		}
		// Never run twice for the same minute, whatever the clock does.
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.minGap()):
		}
	}
}

// Newest returns the newest good backup, for the doctor's RPO check (R6.4).
func (s *Service) Newest(ctx context.Context) (*store.Backup, error) {
	list, err := s.o.DB.ListBackups(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
	for i := range list {
		if list[i].State == StateGood {
			return &list[i], nil
		}
	}
	return nil, nil
}

// Get returns one backup record.
func (s *Service) Get(ctx context.Context, id string) (store.Backup, error) {
	return s.o.DB.GetBackup(ctx, id)
}
