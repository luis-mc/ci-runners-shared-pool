package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// This file holds the store side of settings, the audit log, backups and
// retention (observability R5.24 to R5.32, operations R5.21 to R5.27).

// ErrNotFound is returned when a row asked for by key does not exist.
var ErrNotFound = errors.New("not found")

// longDeadline bounds work that legitimately takes minutes, such as VACUUM INTO
// on a large database (operations R6.6).
const longDeadline = 10 * time.Minute

// TimeFormat is how the database writes every timestamp (UTC, milliseconds).
const TimeFormat = "2006-01-02T15:04:05.000Z"

// ParseTime reads a database timestamp.
func ParseTime(s string) time.Time {
	t, err := time.Parse(TimeFormat, s)
	if err != nil {
		t, _ = time.Parse(time.RFC3339Nano, s)
	}
	return t
}

func withLongDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, longDeadline)
}

// ---------------------------------------------------------------- settings

// Setting is one stored setting. Value is JSON.
type Setting struct {
	Key       string
	Value     string
	UpdatedAt time.Time
	UpdatedBy string
}

// ListSettings returns every stored setting.
func (s *Store) ListSettings(ctx context.Context) ([]Setting, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.q.ListSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("list settings: %w", err)
	}
	out := make([]Setting, 0, len(rows))
	for _, r := range rows {
		out = append(out, Setting{Key: r.Key, Value: r.Value, UpdatedAt: ParseTime(r.UpdatedAt), UpdatedBy: r.UpdatedBy})
	}
	return out, nil
}

// SetSetting writes one setting inside the transaction.
func (t *Tx) SetSetting(key, jsonValue, by string) error {
	if err := t.q.SetSetting(t.ctx, sqlcgen.SetSettingParams{Key: key, Value: jsonValue, UpdatedBy: by}); err != nil {
		return fmt.Errorf("set setting %s: %w", key, err)
	}
	return nil
}

// ------------------------------------------------------------------- audit

// AuditEntry is one row to append (observability R5.24).
type AuditEntry struct {
	Actor      string
	ActorID    string
	Source     string
	Action     string
	TargetType string
	TargetID   string
	Detail     string // JSON, already redacted
	Result     string // "done" or "refused"
}

// AuditRow is one stored audit row.
type AuditRow struct {
	ID         int64
	At         time.Time
	Actor      string
	ActorID    string
	Source     string
	Action     string
	TargetType string
	TargetID   string
	Detail     string
	Result     string
}

// AuditFilter narrows ListAudit. Empty fields match everything.
type AuditFilter struct {
	Actor, Action, TargetType, TargetID string
	From, To                            time.Time
	BeforeID                            int64 // 0 means the newest page
	Limit                               int
}

// InsertAudit appends one audit row, chained to the row before it (R5.28). The read of the last hash and the
// insert run in one transaction, so two writers never chain to the same row.
func (s *Store) InsertAudit(ctx context.Context, e AuditEntry) (int64, error) {
	var id int64
	err := s.WithTx(ctx, func(tx *Tx) error {
		var err error
		id, err = tx.InsertAudit(e)
		return err
	})
	return id, err
}

// InsertAudit appends one audit row inside the transaction, so the row and the
// change commit together or not at all (observability R5.25). The row stores the hash of the row before it.
func (t *Tx) InsertAudit(e AuditEntry) (int64, error) {
	if e.Detail == "" {
		e.Detail = "{}"
	}
	if e.Result == "" {
		e.Result = "done"
	}
	if e.Source == "" {
		e.Source = "system"
	}
	prev, err := t.q.LastAuditHash(t.ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("audit: %w", err)
	}
	at := time.Now().UTC().Format(TimeFormat)
	id, err := t.q.InsertAuditEvent(t.ctx, sqlcgen.InsertAuditEventParams{
		At: at, Actor: e.Actor, ActorID: e.ActorID, Source: e.Source, Action: e.Action,
		TargetType: e.TargetType, Subject: e.TargetID, Detail: e.Detail, Result: e.Result,
		PrevHash: prev, Hash: AuditHash(prev, at, e),
	})
	if err != nil {
		return 0, fmt.Errorf("audit: %w", err)
	}
	return id, nil
}

// ListAudit returns audit rows newest first.
func (s *Store) ListAudit(ctx context.Context, f AuditFilter) ([]AuditRow, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	p := sqlcgen.ListAuditEventsParams{BeforeID: f.BeforeID, PageSize: int64(f.Limit)}
	if p.BeforeID <= 0 {
		p.BeforeID = math.MaxInt64
	}
	if p.PageSize <= 0 {
		p.PageSize = 50
	}
	str := func(v string) interface{} {
		if v == "" {
			return nil
		}
		return v
	}
	p.Actor, p.Action, p.TargetType, p.TargetID = str(f.Actor), str(f.Action), str(f.TargetType), str(f.TargetID)
	if !f.From.IsZero() {
		p.FromAt = f.From.UTC().Format(TimeFormat)
	}
	if !f.To.IsZero() {
		p.ToAt = f.To.UTC().Format(TimeFormat)
	}
	rows, err := s.q.ListAuditEvents(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("list audit: %w", err)
	}
	out := make([]AuditRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, AuditRow{
			ID: r.ID, At: ParseTime(r.At), Actor: r.Actor, ActorID: r.ActorID, Source: r.Source,
			Action: r.Action, TargetType: r.TargetType, TargetID: r.Subject, Detail: r.Detail, Result: r.Result,
		})
	}
	return out, nil
}

// DeleteAuditBefore deletes audit rows older than cutoff. Only the retention
// job calls it (observability R5.26).
func (s *Store) DeleteAuditBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	n, err := s.q.DeleteAuditBefore(ctx, cutoff.UTC().Format(TimeFormat))
	if err != nil {
		return 0, fmt.Errorf("delete audit: %w", err)
	}
	return n, nil
}

// DeleteJobHistoryBefore deletes finished job history older than cutoff.
func (s *Store) DeleteJobHistoryBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	n, err := s.q.DeleteJobHistoryBefore(ctx, sql.NullString{String: cutoff.UTC().Format(TimeFormat), Valid: true})
	if err != nil {
		return 0, fmt.Errorf("delete job history: %w", err)
	}
	return n, nil
}

// ----------------------------------------------------------------- backups

// Backup is one backup record.
type Backup struct {
	ID            string
	File          string
	CreatedAt     time.Time
	SizeBytes     int64
	SchemaVersion int64
	State         string // writing, good, failed
	CheckResult   string
}

// Drill is one restore drill record.
type Drill struct {
	ID         string
	BackupID   string
	StartedAt  time.Time
	FinishedAt time.Time // zero while running
	State      string    // running, passed, failed
	Detail     string
}

func toBackup(b sqlcgen.Backup) Backup {
	return Backup{ID: b.ID, File: b.File, CreatedAt: ParseTime(b.CreatedAt), SizeBytes: b.SizeBytes,
		SchemaVersion: b.SchemaVersion, State: b.State, CheckResult: b.CheckResult}
}

// InsertBackup records a backup that is being written.
func (s *Store) InsertBackup(ctx context.Context, id, file string, schemaVersion int64) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return s.q.InsertBackup(ctx, sqlcgen.InsertBackupParams{ID: id, File: file, SchemaVersion: schemaVersion})
}

// FinishBackup sets a backup's final state, size and check result.
func (s *Store) FinishBackup(ctx context.Context, id, state string, size int64, check string) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return s.q.FinishBackup(ctx, sqlcgen.FinishBackupParams{ID: id, State: state, SizeBytes: size, CheckResult: check})
}

// GetBackup returns one backup record, or ErrNotFound.
func (s *Store) GetBackup(ctx context.Context, id string) (Backup, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	b, err := s.q.GetBackup(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Backup{}, ErrNotFound
	}
	if err != nil {
		return Backup{}, fmt.Errorf("get backup: %w", err)
	}
	return toBackup(b), nil
}

// ListBackups returns backups that are not deleted, newest first.
func (s *Store) ListBackups(ctx context.Context) ([]Backup, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.q.ListBackups(ctx)
	if err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}
	out := make([]Backup, 0, len(rows))
	for _, r := range rows {
		out = append(out, toBackup(r))
	}
	return out, nil
}

// MarkBackupDeleted records that a backup's file was deleted.
func (s *Store) MarkBackupDeleted(ctx context.Context, id string) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return s.q.MarkBackupDeleted(ctx, id)
}

// InsertDrill records a drill that starts now.
func (s *Store) InsertDrill(ctx context.Context, id, backupID string) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return s.q.InsertRestoreDrill(ctx, sqlcgen.InsertRestoreDrillParams{
		ID: id, BackupID: sql.NullString{String: backupID, Valid: backupID != ""},
	})
}

// FinishDrill records a drill's result.
func (s *Store) FinishDrill(ctx context.Context, id, state, detail string) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return s.q.FinishRestoreDrill(ctx, sqlcgen.FinishRestoreDrillParams{ID: id, State: state, Detail: detail})
}

// LastDrill returns the newest drill, or ErrNotFound.
func (s *Store) LastDrill(ctx context.Context) (Drill, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	d, err := s.q.LastRestoreDrill(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return Drill{}, ErrNotFound
	}
	if err != nil {
		return Drill{}, fmt.Errorf("last drill: %w", err)
	}
	out := Drill{ID: d.ID, BackupID: d.BackupID.String, StartedAt: ParseTime(d.StartedAt), State: d.State, Detail: d.Detail}
	if d.FinishedAt.Valid {
		out.FinishedAt = ParseTime(d.FinishedAt.String)
	}
	return out, nil
}

// VacuumInto writes a consistent copy of the live database to dest with
// VACUUM INTO. WAL mode keeps writers going while it runs (operations R6.6).
// dest must not exist.
func (s *Store) VacuumInto(ctx context.Context, dest string) error {
	ctx, cancel := withLongDeadline(ctx)
	defer cancel()
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", filepath.ToSlash(dest)); err != nil {
		return fmt.Errorf("vacuum into: %w", err)
	}
	return nil
}

// SchemaVersion is the highest applied migration.
func (s *Store) SchemaVersion(ctx context.Context) (int64, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return schemaVersion(ctx, s.db)
}

func schemaVersion(ctx context.Context, db *sql.DB) (int64, error) {
	var v sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT MAX(version_id) FROM goose_db_version WHERE is_applied").Scan(&v); err != nil {
		return 0, fmt.Errorf("schema version: %w", err)
	}
	return v.Int64, nil
}

// Inspection is what a database file holds, read without migrating it.
type Inspection struct {
	IntegrityCheck string           // "ok" or the first problem
	SchemaVersion  int64            // highest applied migration
	RowCounts      map[string]int64 // rows per table
}

// Inspect opens the database file read-only and runs PRAGMA integrity_check
// (operations R5.22). It never migrates or changes the file.
func Inspect(ctx context.Context, path string) (Inspection, error) {
	ctx, cancel := withLongDeadline(ctx)
	defer cancel()
	dsn := "file:" + filepath.ToSlash(path) + "?mode=ro"
	if !strings.HasPrefix(filepath.ToSlash(path), "/") {
		dsn = "file:/" + filepath.ToSlash(path) + "?mode=ro" // Windows drive paths
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return Inspection{}, fmt.Errorf("inspect: %w", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	var in Inspection
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&in.IntegrityCheck); err != nil {
		return Inspection{}, fmt.Errorf("integrity check: %w", err)
	}
	if in.SchemaVersion, err = schemaVersion(ctx, db); err != nil {
		return Inspection{}, err
	}
	names, err := tableNames(ctx, db)
	if err != nil {
		return Inspection{}, err
	}
	sort.Strings(names)
	in.RowCounts = make(map[string]int64, len(names))
	for _, n := range names {
		var c int64
		q := `SELECT COUNT(*) FROM "` + strings.ReplaceAll(n, `"`, `""`) + `"`
		if err := db.QueryRowContext(ctx, q).Scan(&c); err != nil {
			return Inspection{}, fmt.Errorf("count %s: %w", n, err)
		}
		in.RowCounts[n] = c
	}
	return in, nil
}

// MaxSchemaVersion is the newest migration this binary carries.
func MaxSchemaVersion() (int64, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return 0, err
	}
	var max int64
	for _, e := range entries {
		var n int64
		if _, err := fmt.Sscanf(e.Name(), "%d_", &n); err == nil && n > max {
			max = n
		}
	}
	return max, nil
}

// Checkpoint folds the WAL into the main file, so a file copy is complete.
func (s *Store) Checkpoint(ctx context.Context) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	_, err := s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

// DeleteMachineLoadBefore deletes raw load samples older than cutoff.
func (s *Store) DeleteMachineLoadBefore(ctx context.Context, cutoff time.Time) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	if err := s.q.DeleteMachineLoadBefore(ctx, cutoff.UTC().Format(TimeFormat)); err != nil {
		return fmt.Errorf("delete machine load: %w", err)
	}
	return nil
}

// MaxJobHistoryRows is the ceiling on job history rows (observability R6.8).
const MaxJobHistoryRows = 1_000_000

// TrimJobHistoryToRows deletes the oldest finished job history rows beyond max.
func (s *Store) TrimJobHistoryToRows(ctx context.Context, max int64) (int64, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM job_history WHERE state = 'done' AND id NOT IN (SELECT id FROM job_history ORDER BY id DESC LIMIT ?)`, max)
	if err != nil {
		return 0, fmt.Errorf("trim job history: %w", err)
	}
	return res.RowsAffected()
}

func tableNames(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name != 'goose_db_version' ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("inspect tables: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}
