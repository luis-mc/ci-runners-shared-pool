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

func snap(version int64, tables map[string]store.TableData) snapshot {
	return snapshot{SchemaVersion: version, Tables: tables}
}

func tbl(cols []string, rows ...[]string) store.TableData {
	return store.TableData{Columns: cols, Rows: rows}
}

func declaredDiffs(before, after snapshot, decls []store.DeclaredChange) (notes, diffs []string) {
	bt, at, notes, diffs := applyDeclared(before, after, decls)
	return notes, append(diffs, compareData(bt, at)...)
}

var dropDecl = []store.DeclaredChange{{Migration: 42, Task: "CRSP227", Reason: "parity removed", DropTables: []string{"access_credentials"}}}
var rewriteDecl = []store.DeclaredChange{{Migration: 43, Task: "CRSP229", Reason: "legacy_vars removed",
	Rewrites: []store.DeclaredRewrite{{Table: "repos_state", Column: "body", JSONKey: "legacy_vars"}}}}

func TestUndeclaredDropStillFails(t *testing.T) {
	cols := []string{"id"}
	b := snap(41, map[string]store.TableData{"keep": tbl(cols, []string{"1"}), "other": tbl(cols, []string{"1"})})
	a := snap(43, map[string]store.TableData{"keep": tbl(cols, []string{"1"})})
	_, d := declaredDiffs(b, a, dropDecl)
	if len(d) != 1 || !strings.Contains(d[0], "table other is gone") {
		t.Fatalf("an undeclared drop must fail: %v", d)
	}
}

func TestUndeclaredRowChangeStillFails(t *testing.T) {
	cols := []string{"id", "v"}
	b := snap(41, map[string]store.TableData{"settings": tbl(cols, []string{"1", "a"})})
	a := snap(43, map[string]store.TableData{"settings": tbl(cols, []string{"1", "b"})})
	_, d := declaredDiffs(b, a, append(append([]store.DeclaredChange{}, dropDecl...), rewriteDecl...))
	if len(d) != 1 || !strings.Contains(d[0], "settings") {
		t.Fatalf("an undeclared row change must fail: %v", d)
	}
}

func TestDeclaredDropPassesOnlyAcrossItsMigration(t *testing.T) {
	cols := []string{"id"}
	with := map[string]store.TableData{"access_credentials": tbl(cols, []string{"1"})}
	without := map[string]store.TableData{}
	notes, d := declaredDiffs(snap(41, with), snap(42, without), dropDecl)
	if len(d) != 0 || len(notes) != 1 || notes[0] != "LC-11 note: declared by migration 42 (CRSP227): table access_credentials dropped" {
		t.Fatalf("a drop across migration 42 passes with a note: %v %v", notes, d)
	}
	for _, c := range [][2]int64{{42, 43}, {43, 44}, {41, 41}} {
		if _, d := declaredDiffs(snap(c[0], with), snap(c[1], without), dropDecl); len(d) != 1 {
			t.Fatalf("a drop from %d to %d is outside migration 42 and must fail: %v", c[0], c[1], d)
		}
	}
}

func TestDeclaredDropThatLeavesTheTableFails(t *testing.T) {
	cols := []string{"id"}
	b := snap(41, map[string]store.TableData{"access_credentials": tbl(cols, []string{"1"})})
	for name, rows := range map[string][][]string{"emptied": nil, "kept": {{"1"}}} {
		a := snap(42, map[string]store.TableData{"access_credentials": {Columns: cols, Rows: rows}})
		notes, d := declaredDiffs(b, a, dropDecl)
		if len(notes) != 0 || len(d) == 0 || !strings.Contains(d[0], "still there") {
			t.Fatalf("%s: a declared drop that leaves the table must fail: %v %v", name, notes, d)
		}
	}
}

func TestDeclaredRewrite(t *testing.T) {
	cols := []string{"id", "body", "saved_at"}
	mk := func(v int64, rows ...[]string) snapshot {
		return snap(v, map[string]store.TableData{"repos_state": tbl(cols, rows...)})
	}
	b := mk(42, []string{"=1", `={"a":1,"legacy_vars":{"x":"y"}}`, "=t1"}, []string{"=2", `={"a":2}`, "=t1"})
	ok := mk(43, []string{"=1", `={"a":1}`, "=t2"}, []string{"=2", `={"a":2}`, "=t2"})
	notes, d := declaredDiffs(b, ok, rewriteDecl)
	if len(d) != 0 || len(notes) != 1 || !strings.Contains(notes[0], "migration 43 (CRSP229)") {
		t.Fatalf("a declared rewrite passes: %v %v", notes, d)
	}
	lost := mk(43, []string{"=1", `={"a":1}`, "=t2"})
	if _, d := declaredDiffs(b, lost, rewriteDecl); len(d) != 1 || !strings.Contains(d[0], "repos_state") {
		t.Fatalf("a declared rewrite that loses a row must fail: %v", d)
	}
	other := mk(43, []string{"=1", `={"a":9}`, "=t2"}, []string{"=2", `={"a":2}`, "=t2"})
	if _, d := declaredDiffs(b, other, rewriteDecl); len(d) != 1 {
		t.Fatalf("a declared rewrite changes only the named key: %v", d)
	}
	if _, d := declaredDiffs(mk(43, []string{"=1", `={"a":1,"legacy_vars":1}`, "=t1"}), mk(43, []string{"=1", `={"a":1}`, "=t1"}), rewriteDecl); len(d) != 1 {
		t.Fatalf("a rewrite outside migration 43 must fail: %v", d)
	}
}

func TestDeclaredChangesAreForRealMigrations(t *testing.T) {
	for _, d := range store.DeclaredChanges {
		if d.Task == "" || d.Reason == "" || (len(d.DropTables) == 0 && len(d.Rewrites) == 0) {
			t.Fatalf("declared change %d needs a task, a reason and a change", d.Migration)
		}
	}
}
