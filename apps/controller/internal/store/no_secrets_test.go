package store

import (
	"context"
	"strings"
	"testing"
)

// R6.1: no table has a column that could hold a secret. A column that names a
// token, secret, key, password or credential must say it is a hash (or be a
// plain id or key name listed here).
func TestIntegrationSchemaHasNoSecretColumn(t *testing.T) {
	s := openTemp(t)
	rows, err := s.db.QueryContext(context.Background(), `SELECT m.name, p.name FROM sqlite_master m, pragma_table_info(m.name) p WHERE m.type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	// Columns that are names or public ids, not secret values.
	allowed := map[string]bool{
		"settings.key": true, "machine_keys.hash": true,
		"notification_channels.smtp_has_password": true, // a yes/no flag; the password is in the store
	}
	words := []string{"secret", "token", "private", "password", "passwd", "credential", "refresh", "pem", "api_key", "apikey", "webhook_secret"}
	n := 0
	for rows.Next() {
		var table, col string
		if err := rows.Scan(&table, &col); err != nil {
			t.Fatal(err)
		}
		n++
		c := strings.ToLower(col)
		if allowed[table+"."+c] || strings.HasSuffix(c, "_hash") || c == "hash" {
			continue
		}
		for _, w := range words {
			if strings.Contains(c, w) {
				t.Errorf("column %s.%s looks like it could hold a secret (R6.1): keep it in the secret store", table, col)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n < 50 {
		t.Fatalf("only %d columns scanned: the query is wrong", n)
	}
}
