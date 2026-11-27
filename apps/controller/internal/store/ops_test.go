package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAuditIsAppendOnly(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	id, err := s.InsertAudit(ctx, AuditEntry{Actor: "a", Action: "x"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.ExecContext(ctx, "UPDATE audit_log SET actor = 'b' WHERE id = ?", id)
	if err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("an audit row was editable: %v", err)
	}
}

func TestInspectAndVacuumInto(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	_, _ = s.InsertAudit(ctx, AuditEntry{Actor: "a", Action: "x"})
	dest := t.TempDir() + "/copy.db"
	if err := s.VacuumInto(ctx, dest); err != nil {
		t.Fatal(err)
	}
	in, err := Inspect(ctx, dest)
	if err != nil || in.IntegrityCheck != "ok" || in.RowCounts["audit_log"] != 1 {
		t.Fatalf("%+v %v", in, err)
	}
	want, _ := MaxSchemaVersion()
	if v, _ := s.SchemaVersion(ctx); v != want || in.SchemaVersion != want {
		t.Fatalf("schema version %d / %d, want %d", v, in.SchemaVersion, want)
	}
	if err := s.VacuumInto(ctx, dest); err == nil {
		t.Fatal("VACUUM INTO must not overwrite a file")
	}
}

func TestJobHistoryRetention(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ins := func(id int, at string) {
		_, err := s.db.ExecContext(ctx, `INSERT INTO job_history (repository_id, repository, run_id, run_attempt, github_job_id, job_name, state, completed_at)
			VALUES (1, 'o/r', ?, 1, ?, 'j', 'done', ?)`, id, id, at)
		if err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	ins(1, now.Add(-100*24*time.Hour).Format(TimeFormat))
	ins(2, now.Add(-10*24*time.Hour).Format(TimeFormat))
	ins(3, now.Format(TimeFormat))
	n, err := s.DeleteJobHistoryBefore(ctx, now.Add(-90*24*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	n, err = s.TrimJobHistoryToRows(ctx, 1)
	if err != nil || n != 1 {
		t.Fatalf("trim n=%d err=%v", n, err)
	}
}
