package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// TableData is one user table as text: its columns and its rows, in the order of the columns.
type TableData struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

// UserData reads every user table of the database file, read-only, without migrating it (the lifecycle
// lane compares what the user keeps across a reinstall or an upgrade). Migration bookkeeping and SQLite's
// own tables are not user data. Rows come back sorted, so two reads of equal data are equal.
func UserData(ctx context.Context, path string) (map[string]TableData, error) {
	ctx, cancel := withLongDeadline(ctx)
	defer cancel()
	slash := filepath.ToSlash(path)
	if !strings.HasPrefix(slash, "/") {
		slash = "/" + slash // Windows drive paths
	}
	db, err := sql.Open("sqlite", "file:"+slash+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("user data: %w", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	names, err := tableNames(ctx, db)
	if err != nil {
		return nil, err
	}
	out := make(map[string]TableData, len(names))
	for _, n := range names {
		td, err := readTable(ctx, db, n)
		if err != nil {
			return nil, err
		}
		out[n] = td
	}
	return out, nil
}

func readTable(ctx context.Context, db *sql.DB, name string) (TableData, error) {
	rows, err := db.QueryContext(ctx, `SELECT * FROM "`+strings.ReplaceAll(name, `"`, `""`)+`"`)
	if err != nil {
		return TableData{}, fmt.Errorf("read %s: %w", name, err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return TableData{}, fmt.Errorf("read %s: %w", name, err)
	}
	td := TableData{Columns: cols, Rows: [][]string{}}
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return TableData{}, fmt.Errorf("read %s: %w", name, err)
		}
		row := make([]string, len(cols))
		for i, v := range vals {
			if v.Valid {
				row[i] = "=" + v.String // NULL (no prefix) differs from an empty string
			}
		}
		td.Rows = append(td.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return TableData{}, fmt.Errorf("read %s: %w", name, err)
	}
	sort.Slice(td.Rows, func(i, j int) bool { return strings.Join(td.Rows[i], "\x00") < strings.Join(td.Rows[j], "\x00") })
	return td, nil
}
