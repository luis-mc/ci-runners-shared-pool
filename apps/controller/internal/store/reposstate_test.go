package store

import (
	"context"
	"io/fs"
	"testing"

	"github.com/pressly/goose/v3"
)

// The database of v0.1.2 (migrations to 15) is opened by v0.1.3: the
// migration adds the repository state without touching a repository row, and
// the saved state is read back after a reopen (CRSP148).
func TestIntegrationReposStateMigrationKeepsPoolOn(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, s.db, sub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 15); err != nil {
		t.Fatalf("down to 15: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO installations (id, account_login, account_type) VALUES (10, 'alice', 'User')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO repositories (id, installation_id, full_name, owner, name, pool_on)
		VALUES (100, 10, 'alice/app', 'alice', 'app', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	var on int
	if err := s.db.QueryRowContext(ctx, `SELECT pool_on FROM repositories WHERE id = 100`).Scan(&on); err != nil || on != 1 {
		t.Fatalf("pool_on %d %v", on, err)
	}
	rs := s.ReposState()
	if b, err := rs.LoadReposState(ctx); err != nil || len(b) != 0 {
		t.Fatalf("a new database has no saved state: %q %v", b, err)
	}
	if err := rs.SaveReposState(ctx, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := rs.SaveReposState(ctx, []byte(`{"a":2}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	if b, err := s2.ReposState().LoadReposState(ctx); err != nil || string(b) != `{"a":2}` {
		t.Fatalf("after reopen: %q %v", b, err)
	}
}
