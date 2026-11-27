package store

import (
	"context"
	"strings"
	"testing"
)

func chainStore(t *testing.T, n int) *Store {
	t.Helper()
	s, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for i := 0; i < n; i++ {
		if _, err := s.InsertAudit(context.Background(), AuditEntry{Actor: "a", Action: "x.y", Detail: `{"n":1}`}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// tamper runs raw SQL with the append-only trigger and the foreign rules out of the way, the way a person with the
// database file would.
func tamper(t *testing.T, s *Store, stmts ...string) {
	t.Helper()
	all := append([]string{"DROP TRIGGER audit_log_no_update"}, stmts...)
	for _, q := range all {
		if _, err := s.db.ExecContext(context.Background(), q); err != nil {
			t.Fatal(q, err)
		}
	}
}

func TestChainIntactAndLinked(t *testing.T) {
	s := chainStore(t, 4)
	rows, _ := s.ListAuditChain(context.Background(), 0, 10)
	if rows[0].PrevHash != "" || rows[0].Hash == "" {
		t.Fatalf("first row: %+v", rows[0])
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].PrevHash != rows[i-1].Hash {
			t.Errorf("row %d does not store the hash of the row before", rows[i].ID)
		}
	}
	rep, err := s.VerifyAuditChain(context.Background())
	if err != nil || !rep.OK || rep.Checked != 4 {
		t.Fatalf("%+v %v", rep, err)
	}
}

func TestChainDetectsFirstBrokenRow(t *testing.T) {
	cases := map[string][]string{
		"edit":   {"UPDATE audit_log SET actor = 'evil' WHERE id = 3"},
		"remove": {"DELETE FROM audit_log WHERE id = 3"},
		"insert": {"INSERT INTO audit_log (id, actor, action, prev_hash, hash) VALUES (3, 'x', 'y', 'bad', 'bad')"},
	}
	for name, stmts := range cases {
		t.Run(name, func(t *testing.T) {
			s := chainStore(t, 5)
			if name == "insert" {
				stmts = append([]string{"DELETE FROM audit_log WHERE id = 3"}, stmts...)
			}
			tamper(t, s, stmts...)
			rep, err := s.VerifyAuditChain(context.Background())
			if err != nil || rep.OK {
				t.Fatalf("not caught: %+v %v", rep, err)
			}
			wantID := int64(3)
			if name == "remove" {
				wantID = 4 // the row after the gap no longer follows its neighbour
			}
			if rep.BrokenID != wantID {
				t.Errorf("first broken row = %d, want %d (%s)", rep.BrokenID, wantID, rep.Reason)
			}
			if !strings.Contains(rep.Words(), "not intact") {
				t.Errorf("words: %s", rep.Words())
			}
		})
	}
}

func TestChainOldRowsAndRetention(t *testing.T) {
	s := chainStore(t, 0)
	tamper(t, s, "INSERT INTO audit_log (actor, action) VALUES ('old', 'a'), ('old', 'b')")
	for i := 0; i < 3; i++ {
		_, _ = s.InsertAudit(context.Background(), AuditEntry{Actor: "a", Action: "n"})
	}
	rep, _ := s.VerifyAuditChain(context.Background())
	if !rep.OK || rep.Unchained != 2 || rep.Checked != 3 {
		t.Fatalf("old rows: %+v", rep)
	}
	// Retention deletes the oldest rows: the rest still checks out.
	tamper2 := "DELETE FROM audit_log WHERE id <= 3"
	if _, err := s.db.ExecContext(context.Background(), tamper2); err != nil {
		t.Fatal(err)
	}
	rep, _ = s.VerifyAuditChain(context.Background())
	if !rep.OK || rep.Checked != 2 {
		t.Fatalf("after retention: %+v", rep)
	}
}
