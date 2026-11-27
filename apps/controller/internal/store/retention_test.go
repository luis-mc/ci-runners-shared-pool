package store

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

// columnsOf lists a table's column names, sorted.
func columnsOf(t *testing.T, db *Store, table string) []string {
	t.Helper()
	rows, err := db.db.QueryContext(context.Background(), `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(cols)
	return cols
}

// TestUserAndSessionRowsKeepOnlyTheListedFields guards R6.10: a new column in
// either table fails here until the data inventory (stack profile section 7)
// and the spec list it.
func TestUserAndSessionRowsKeepOnlyTheListedFields(t *testing.T) { // R6.10
	db, _ := authDB(t)
	want := map[string][]string{
		// GitHub id, login, role, who added them, and dates.
		"users": {"added_at", "added_by", "github_user_id", "last_sign_in_at", "login", "role"},
		// A hashed id (and a hashed CSRF token), the user's id, the source and dates.
		"sessions": {"created_at", "csrf_hash", "ended_at", "github_user_id", "id_hash", "last_seen_at", "source"},
	}
	for table, cols := range want {
		if got := columnsOf(t, db, table); !reflect.DeepEqual(got, cols) {
			t.Errorf("%s columns %v, want %v", table, got, cols)
		}
	}
}
