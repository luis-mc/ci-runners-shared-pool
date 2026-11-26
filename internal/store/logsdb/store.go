// Package logsdb is the SQLite adapter of the log database, `logs.db`
// (observability R6.10): a separate file from the main database, with its own
// migrations and an FTS5 table for the message text (R5.21). It sits under
// internal/store because only that folder may import the SQLite driver
// (STACKSPEC 3.2).
package logsdb

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // the SQLite driver; confined to internal/store by depguard.

	"github.com/luis-mc/ci-runners-shared-pool/internal/logs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

const dayMs = int64(24 * time.Hour / time.Millisecond)

// DB is the log database.
type DB struct {
	db  *sql.DB
	rdb *sql.DB
}

var _ logs.Store = (*DB)(nil)

// Open opens (creating if needed) <dataDir>/logs.db and applies its migrations.
func Open(ctx context.Context, dataDir string) (*DB, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	return OpenPath(ctx, filepath.Join(dataDir, logs.LogsFile))
}

// OpenPath opens the log database at path.
func OpenPath(ctx context.Context, path string) (*DB, error) {
	dsn := func(readOnly bool) string {
		q := url.Values{}
		q.Add("_pragma", "busy_timeout(5000)")
		q.Add("_pragma", "journal_mode(WAL)")
		q.Add("_pragma", "synchronous(NORMAL)") // a log may lose its last lines in a power cut; it never corrupts
		if readOnly {
			q.Add("_pragma", "query_only(1)")
		}
		return (&url.URL{Scheme: "file", Opaque: filepath.ToSlash(path), RawQuery: q.Encode()}).String()
	}
	db, err := sql.Open("sqlite", dsn(false))
	if err != nil {
		return nil, fmt.Errorf("open logs.db: %w", err)
	}
	db.SetMaxOpenConns(1) // one writer; searches use the read pool
	rdb, err := sql.Open("sqlite", dsn(true))
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open logs.db read pool: %w", err)
	}
	rdb.SetMaxOpenConns(4)
	d := &DB{db: db, rdb: rdb}
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err == nil {
		var p *goose.Provider
		if p, err = goose.NewProvider(goose.DialectSQLite3, db, sub); err == nil {
			_, err = p.Up(ctx)
		}
	}
	if err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("logs.db migrations: %w", err)
	}
	return d, nil
}

// Close closes both pools.
func (d *DB) Close() error {
	rerr := d.rdb.Close()
	if err := d.db.Close(); err != nil {
		return err
	}
	return rerr
}

// Insert implements logs.Store.
func (d *DB) Insert(ctx context.Context, lines []logs.Line) error {
	if len(lines) == 0 {
		return nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	st, err := tx.PrepareContext(ctx, `INSERT INTO logs (at_ms, day, level, machine, runner, job, component, message, attrs, bytes) VALUES (?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	for _, l := range lines {
		ms := l.At.UnixMilli()
		if _, err := st.ExecContext(ctx, ms, ms/dayMs, int(l.Level), l.Machine, l.Runner, l.Job, l.Component, l.Message, l.Attrs, l.Size()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Search implements logs.Store.
func (d *DB) Search(ctx context.Context, q logs.Query) (logs.Page, error) {
	if err := q.Validate(); err != nil {
		return logs.Page{}, err
	}
	var where []string
	var args []any
	add := func(cond string, a ...any) {
		where = append(where, cond)
		args = append(args, a...)
	}
	if m := q.MatchExpr(); m != "" {
		add("id IN (SELECT rowid FROM logs_fts WHERE logs_fts MATCH ?)", m)
	}
	for _, f := range [][2]string{{"machine", q.Machine}, {"runner", q.Runner}, {"job", q.Job}} {
		if f[1] != "" {
			add(f[0]+" = ?", f[1])
		}
	}
	if q.MinLevel != nil {
		add("level >= ?", int(*q.MinLevel))
	}
	if !q.From.IsZero() {
		add("at_ms >= ?", q.From.UnixMilli())
	}
	if !q.To.IsZero() {
		add("at_ms <= ?", q.To.UnixMilli())
	}
	if q.BeforeID > 0 {
		add("id < ?", q.BeforeID)
	}
	sqlText := `SELECT id, at_ms, level, machine, runner, job, component, message, attrs FROM logs`
	if len(where) > 0 {
		sqlText += " WHERE " + strings.Join(where, " AND ")
	}
	sqlText += " ORDER BY id DESC LIMIT ?"
	args = append(args, q.Limit+1)
	rows, err := d.rdb.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return logs.Page{}, err
	}
	defer func() { _ = rows.Close() }()
	var page logs.Page
	for rows.Next() {
		var l logs.Line
		var ms int64
		var lv int
		if err := rows.Scan(&l.ID, &ms, &lv, &l.Machine, &l.Runner, &l.Job, &l.Component, &l.Message, &l.Attrs); err != nil {
			return logs.Page{}, err
		}
		l.At, l.Level = time.UnixMilli(ms).UTC(), slog.Level(lv)
		page.Lines = append(page.Lines, l)
	}
	if err := rows.Err(); err != nil {
		return logs.Page{}, err
	}
	if len(page.Lines) > q.Limit {
		page.Lines = page.Lines[:q.Limit]
		page.NextBefore = page.Lines[q.Limit-1].ID
	}
	return page, nil
}

// Prune implements logs.Store: it deletes by day (R6.8), so a delete is one
// index range and never walks single rows.
func (d *DB) Prune(ctx context.Context, cutoff time.Time, maxBytesPerMachine int64) (int64, error) {
	var total int64
	res, err := d.db.ExecContext(ctx, `DELETE FROM logs WHERE day < ?`, cutoff.UnixMilli()/dayMs)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	total += n
	if maxBytesPerMachine <= 0 {
		return total, nil
	}
	rows, err := d.db.QueryContext(ctx, `SELECT machine, day, SUM(bytes) FROM logs GROUP BY machine, day ORDER BY machine, day`)
	if err != nil {
		return total, err
	}
	type dayRow struct {
		day, bytes int64
	}
	perMachine := map[string][]dayRow{}
	for rows.Next() {
		var m string
		var r dayRow
		if err := rows.Scan(&m, &r.day, &r.bytes); err != nil {
			_ = rows.Close()
			return total, err
		}
		perMachine[m] = append(perMachine[m], r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return total, err
	}
	_ = rows.Close()
	for m, days := range perMachine {
		var sum int64
		for _, r := range days {
			sum += r.bytes
		}
		var upTo int64 = -1
		for _, r := range days {
			if sum <= maxBytesPerMachine {
				break
			}
			sum -= r.bytes
			upTo = r.day
		}
		if upTo < 0 {
			continue
		}
		res, err := d.db.ExecContext(ctx, `DELETE FROM logs WHERE machine = ? AND day <= ?`, m, upTo)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}
