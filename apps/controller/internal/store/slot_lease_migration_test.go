package store

import (
	"context"
	"io/fs"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestIntegrationSlotLeaseMigrationKeepsHeldSlots proves migration 14 is safe
// on a database that already holds a slot: the row keeps its claim as holder
// id and its age limit as lease end, so the migration frees nothing early
// (scheduling R5.70).
func TestIntegrationSlotLeaseMigrationKeepsHeldSlots(t *testing.T) {
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
	if _, err := p.DownTo(ctx, 13); err != nil {
		t.Fatalf("down to 13: %v", err)
	}
	if err := s.SeedMachine(ctx, "m1", "linux"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO slots (machine_id, kind, number, claim, repository, run_id, run_attempt,
		job, holder_machine, holder_pid, queued_at, taken_at, expires_at)
		VALUES ('m1', 'ci', 1, 'claim-1', 'o/r', 1, 1, 'build', 'm1', 4242, '2026-11-14T10:00:00.000Z',
		'2026-11-14T10:00:00.000Z', '2026-11-14T12:00:00.000Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 14); err != nil {
		t.Fatalf("up to 14: %v", err)
	}
	var holder, lease string
	if err := s.db.QueryRowContext(ctx, `SELECT holder_id, lease_expires_at FROM slots`).Scan(&holder, &lease); err != nil {
		t.Fatal(err)
	}
	if holder != "claim-1" || lease != "2026-11-14T12:00:00.000Z" {
		t.Fatalf("holder %q lease %q", holder, lease)
	}
	// down and up again drops and re-adds the columns without losing the slot
	if _, err := p.DownTo(ctx, 13); err != nil {
		t.Fatalf("down again: %v", err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM slots WHERE holder_id = 'claim-1'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("slots after round trip: %d %v", n, err)
	}
}
