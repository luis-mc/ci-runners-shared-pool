package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

func coreImage(id, key string) registry.Image {
	return registry.Image{
		ID: id, Kind: registry.KindStandard, Name: "crsp/std/base", Key: registry.Key(key), Profile: "base", Arch: "amd64",
		Manifest: "sha256:" + strings.Repeat("a", 64), StackHash: strings.Repeat("b", 64), BaseDigest: "sha256:" + strings.Repeat("c", 64),
		PushedBy: "m1", PushedAt: time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC),
	}
}

// R5.5, R5.6: stage, promote with a tag row that cannot be written twice; a refused image has no tag.
func TestRegistryCoreStagePromoteRefuse(t *testing.T) {
	ctx := context.Background()
	s, err := OpenForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	c := s.RegistryCore()

	a := coreImage("img_a", "aaaaaaaaaaaa")
	if err := c.Stage(ctx, a, ""); err != nil {
		t.Fatal(err)
	}
	if ok, _ := c.TagExists(ctx, a.Name, "aaaaaaaaaaaa"); ok {
		t.Fatal("a staged image has a tag")
	}
	a.State, a.PromotedAt, a.BaseName, a.SizeBytes = registry.StateCurrent, time.Now(), "ubuntu:24.04", 1234
	if err := c.Promoted(ctx, a); err != nil {
		t.Fatal(err)
	}
	if ok, _ := c.TagExists(ctx, a.Name, "aaaaaaaaaaaa"); !ok {
		t.Fatal("no tag row")
	}
	row, err := s.q.GetRegistryImage(ctx, "img_a")
	if err != nil || row.State != "current" || row.BaseName != "ubuntu:24.04" || row.SizeBytes != 1234 || row.StagedName != "crsp/incoming/m1/std/base" {
		t.Fatalf("row = %+v %v", row, err)
	}

	// The same (name, tag) from another image cannot be written, and its image stays staged.
	b := coreImage("img_b", "aaaaaaaaaaaa")
	b.Manifest = "sha256:" + strings.Repeat("d", 64)
	if err := c.Stage(ctx, b, `{"profile":"base"}`); err != nil {
		t.Fatal(err)
	}
	b.State, b.PromotedAt = registry.StateCurrent, time.Now()
	if err := c.Promoted(ctx, b); err == nil {
		t.Fatal("a second tag row was written")
	}
	row, _ = s.q.GetRegistryImage(ctx, "img_b")
	if row.State != "staged" {
		t.Fatalf("state = %s", row.State)
	}
	if err := c.Refuse(ctx, "img_b", "the tag exists"); err != nil {
		t.Fatal(err)
	}
	row, _ = s.q.GetRegistryImage(ctx, "img_b")
	if row.State != "refused" || row.RefusedWhy != "the tag exists" {
		t.Fatalf("row = %+v", row)
	}
	// The registry tag trigger holds the digest still.
	if _, err := s.db.ExecContext(ctx, `UPDATE registry_image_tags SET manifest_digest = 'sha256:x' WHERE name = ?`, a.Name); err == nil {
		t.Fatal("a tag was updated")
	}
}

// R5.10: a lease records the token id, machine and names; revoked or ended leases are not live.
func TestRegistryCoreLeases(t *testing.T) {
	ctx := context.Background()
	s, err := OpenForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	c := s.RegistryCore()
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	if err := c.Lease(ctx, "j1", "m1", []string{"crsp/std/base"}, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if ok, err := c.Live(ctx, "j1", now); err != nil || !ok {
		t.Fatalf("live = %v %v", ok, err)
	}
	if ok, _ := c.Live(ctx, "j1", now.Add(11*time.Minute)); ok {
		t.Fatal("an ended lease is live")
	}
	if ok, _ := c.Live(ctx, "nope", now); ok {
		t.Fatal("an unknown lease is live")
	}
	if n, err := c.PruneLeases(ctx, now.Add(11*time.Minute)); err != nil || n != 1 {
		t.Fatalf("prune = %d %v", n, err)
	}
	_ = c.Lease(ctx, "j2", "m1", []string{"crsp/std/base"}, now.Add(10*time.Minute))
	if err := c.Revoke(ctx, "j2"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := c.Live(ctx, "j2", now); ok {
		t.Fatal("a revoked lease is live")
	}
}
