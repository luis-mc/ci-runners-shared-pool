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
