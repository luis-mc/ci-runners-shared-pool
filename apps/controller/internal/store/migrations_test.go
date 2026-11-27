package store

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// TestIntegrationMigrationsDownAndUp proves every migration's Down undoes its
// Up: all the way down to zero, then up again, on a real SQLite file.
func TestIntegrationMigrationsDownAndUp(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, s.db, sub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("down: %v", err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	v, err := p.GetDBVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v < 8 {
		t.Fatalf("schema version %d, want at least 8", v)
	}
}

// TestIntegrationAuditLogIsAppendOnly checks observability R5.26: no row of
// the audit log can be edited.
func TestIntegrationAuditLogIsAppendOnly(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	if err := s.WithTx(ctx, func(tx *Tx) error {
		_, err := tx.Audit("system", "test", "", "{}")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE audit_log SET action = 'changed'`)
	if err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("update of audit_log: err = %v, want append-only refusal", err)
	}
}

// TestIntegrationEveryQueryPrepares checks every sqlc query against the
// migrated schema of a real SQLite file, so a query that names a missing
// table or column fails here, not in a feature branch.
func TestIntegrationEveryQueryPrepares(t *testing.T) {
	s := openTemp(t)
	q, err := sqlcgen.Prepare(context.Background(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestIntegrationRunnerRouteMigration proves HND6 and HND12 (runners R5.70,
// R5.71): rows made before migration 13 get their route derived once, other
// data is kept, and a Down then Up neither loses rows nor breaks the route.
func TestIntegrationRunnerRouteMigration(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, s.db, sub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 12); err != nil {
		t.Fatalf("down to 12: %v", err)
	}
	if err := s.SeedMachine(ctx, "m1", "linux"); err != nil {
		t.Fatal(err)
	}
	ins := func(id, name, labels string) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, `INSERT INTO runners (id, name, machine_id, os, arch, kind, backend, isolation, labels, scope_kind)
			VALUES (?, ?, 'm1', 'linux', 'x64', 'persistent', 'persistent', 'persistent', ?, 'repository')`, id, name, labels); err != nil {
			t.Fatal(err)
		}
	}
	ins("a", "crsp-light-demo-linux", `["self-hosted"]`) // legacy name only
	ins("b", "box-2", `["self-hosted","crsp-light"]`)    // light label, plain name
	ins("c", "box-3", `["self-hosted","linux"]`)         // plain
	if _, err := p.UpTo(ctx, 13); err != nil {
		t.Fatalf("up to 13: %v", err)
	}
	check := func(when string) {
		t.Helper()
		want := map[string]string{"a": "light", "b": "light", "c": "ci"}
		rows, err := s.db.QueryContext(ctx, `SELECT id, route_queue, size_class, route_pool FROM runners ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		n := 0
		for rows.Next() {
			var id, q, size, pool string
			if err := rows.Scan(&id, &q, &size, &pool); err != nil {
				t.Fatal(err)
			}
			if q != want[id] || size != "medium" || pool != "" {
				t.Errorf("%s: runner %s route %q size %q pool %q", when, id, q, size, pool)
			}
			n++
		}
		if err := rows.Err(); err != nil || n != 3 {
			t.Fatalf("%s: %d rows, %v", when, n, err)
		}
	}
	check("after up")
	// The route is stored: a renamed runner keeps it.
	if _, err := s.db.ExecContext(ctx, `UPDATE runners SET name = 'renamed' WHERE id = 'a'`); err != nil {
		t.Fatal(err)
	}
	var q string
	if err := s.db.QueryRowContext(ctx, `SELECT route_queue FROM runners WHERE id = 'a'`).Scan(&q); err != nil || q != "light" {
		t.Fatalf("rename changed the route: %q %v", q, err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE runners SET name = 'crsp-light-demo-linux' WHERE id = 'a'`); err != nil {
		t.Fatal(err)
	}
	// A value outside the classes and queues is refused.
	if _, err := s.db.ExecContext(ctx, `UPDATE runners SET size_class = 'huge' WHERE id = 'c'`); err == nil {
		t.Fatal("unknown size class accepted")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE runners SET route_queue = 'x' WHERE id = 'c'`); err == nil {
		t.Fatal("unknown queue accepted")
	}
	// Down keeps the rows and the old columns; Up derives the route again.
	if _, err := p.DownTo(ctx, 12); err != nil {
		t.Fatalf("down: %v", err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM runners`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("down lost rows: %d %v", n, err)
	}
	if _, err := p.UpTo(ctx, 13); err != nil {
		t.Fatalf("up again: %v", err)
	}
	check("after down and up")
}
