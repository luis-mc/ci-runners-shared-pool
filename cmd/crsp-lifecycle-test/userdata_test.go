package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

func snapshotOf(t *testing.T, dir, name string) string {
	t.Helper()
	out := filepath.Join(dir, name)
	if err := dbSnapshot(filepath.Join(dir, store.DatabaseFile), out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDBCompareKeepsAndDetects(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := store.OpenForTests(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WithTx(ctx, func(tx *store.Tx) error { return tx.SetSetting("k", `"v"`, "test") }); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before := snapshotOf(t, dir, "before.json")
	if err := dbCompare(before, snapshotOf(t, dir, "same.json")); err != nil {
		t.Fatalf("same data must compare equal: %v", err)
	}

	s, err = store.OpenForTests(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WithTx(ctx, func(tx *store.Tx) error { return tx.SetSetting("k", `"changed"`, "test") }); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	err = dbCompare(before, snapshotOf(t, dir, "after.json"))
	if err == nil || !strings.Contains(err.Error(), "settings") {
		t.Fatalf("a changed row must be named by its table, got %v", err)
	}
}

func TestCompareDataRuntimeColumn(t *testing.T) {
	cols := []string{"id", "body", "saved_at"}
	mk := func(body, saved string) map[string]store.TableData {
		return map[string]store.TableData{"repos_state": {Columns: cols, Rows: [][]string{{"1", body, saved}}}}
	}
	if d := compareData(mk("a", "t1"), mk("a", "t2")); len(d) != 0 {
		t.Fatalf("saved_at is rewritten by design and is not compared: %v", d)
	}
	d := compareData(mk("a", "t1"), mk("b", "t2"))
	if len(d) != 1 || !strings.Contains(d[0], "changed columns: body") || strings.Contains(d[0], "saved_at") {
		t.Fatalf("a changed body must be named, and only it: %v", d)
	}
	other := map[string]store.TableData{"other": {Columns: cols, Rows: [][]string{{"1", "a", "t1"}}}}
	other2 := map[string]store.TableData{"other": {Columns: cols, Rows: [][]string{{"1", "a", "t2"}}}}
	if d := compareData(other, other2); len(d) != 1 || !strings.Contains(d[0], "saved_at") {
		t.Fatalf("no other table is excluded: %v", d)
	}
}
