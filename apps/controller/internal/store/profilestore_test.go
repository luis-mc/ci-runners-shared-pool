package store

import (
	"context"
	"errors"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/profiles"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// TestProfileStore runs the real tables.
func TestProfileStore(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t).Profiles()
	if err := profiles.Seed(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := profiles.Seed(ctx, s); err != nil {
		t.Fatal(err)
	}
	all, err := s.List(ctx)
	if err != nil || len(all) != len(profiles.Defaults()) {
		t.Fatalf("list %d %v", len(all), err)
	}
	node, err := s.Get(ctx, "node-web")
	if err != nil || len(node.Toolchains) != 2 || node.Pins["gh"] == "" || !node.BuiltIn {
		t.Fatalf("get %+v %v", node, err)
	}
	custom := profiles.Profile{ID: "acme-api", Kind: registry.KindCustom, Repository: "acme/api", BaseName: "b", Services: []profiles.Service{{Name: "pg", Image: "postgres:16"}}}
	if err := s.Put(ctx, custom); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepositoryProfile(ctx, "acme/api", "acme-api"); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.RepositoryProfile(ctx, "acme/api"); id != "acme-api" {
		t.Fatalf("choice %q", id)
	}
	if id, _ := s.RepositoryProfile(ctx, "none/none"); id != "" {
		t.Fatalf("no choice gave %q", id)
	}
	if err := s.Delete(ctx, "node-web"); !errors.Is(err, profiles.ErrNotFound) {
		t.Fatalf("built-in must stay: %v", err)
	}
	if err := s.Delete(ctx, "acme-api"); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.RepositoryProfile(ctx, "acme/api"); id != "" {
		t.Fatalf("choice must cascade, got %q", id)
	}
	if _, err := s.Get(ctx, "acme-api"); !errors.Is(err, profiles.ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.Put(ctx, profiles.Profile{ID: "BAD"}); !errors.Is(err, profiles.ErrBadProfile) {
		t.Fatal(err)
	}
}
