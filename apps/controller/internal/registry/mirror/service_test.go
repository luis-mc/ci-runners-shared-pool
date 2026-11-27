package mirror_test

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/mirror"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// The settings survive a round trip through the store, and Load applies the
// saved ones at start-up.
func TestSettingsRoundTripThroughTheStore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.OpenForTests(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	m := mirror.New(mirror.Options{DataDir: dir})
	defer func() { _ = m.Close() }()
	svc := &mirror.Service{Store: db, Mirror: m, Audit: audit.New(db), DataDir: dir}
	if err := svc.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if s := m.Settings(); !s.Enabled || len(s.Upstreams) != 4 || s.TagRecheck != time.Hour {
		t.Fatalf("defaults not applied: %+v", s)
	}
	in := registry.MirrorSettings{Enabled: true, Upstreams: []string{"docker.io", "quay.io"}, BudgetBytes: 5 << 30, TagRecheck: 30 * time.Minute}
	if _, err := svc.Put(ctx, audit.Actor{Login: "owner", Source: audit.SourceWeb}, in); err != nil {
		t.Fatal(err)
	}
	got, err := db.MirrorSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || len(got.Upstreams) != 2 || got.BudgetBytes != 5<<30 || got.TagRecheck != 30*time.Minute || got.Folder != "" {
		t.Fatalf("stored %+v", got)
	}
	// A new mirror started from the store runs the saved settings.
	m2 := mirror.New(mirror.Options{DataDir: dir})
	defer func() { _ = m2.Close() }()
	if err := (&mirror.Service{Store: db, Mirror: m2, DataDir: dir}).Load(ctx); err != nil {
		t.Fatal(err)
	}
	if s := m2.Settings(); len(s.Upstreams) != 2 || s.Upstreams[1] != "quay.io" {
		t.Fatalf("reloaded %+v", s)
	}
}
