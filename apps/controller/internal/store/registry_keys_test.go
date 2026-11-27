package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

func addRegistryImage(t *testing.T, s *Store, id, repo, branch string) {
	t.Helper()
	err := s.q.InsertRegistryImage(context.Background(), sqlcgen.InsertRegistryImageParams{
		ID: id, Kind: "custom", Name: "crsp/custom/o-r/node-web", ImageTag: id[len(id)-12:], ProfileID: "node-web", Arch: "amd64",
		State: "current", ManifestDigest: "sha256:" + id, StackHash: id, BaseDigest: "sha256:0", SourceRepository: repo, Branch: branch,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// R5.24, R5.27: a pointer per repository; moving one keeps the old image for the other.
func TestIntegrationRegistryPointersPerRepository(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	k := s.RegistryKeys()
	addRegistryImage(t, s, "img_aaaaaaaaaaaa", "o/a", "")
	addRegistryImage(t, s, "img_bbbbbbbbbbbb", "o/a", "")
	if _, err := k.Pointer(ctx, "o/a", "node-web", "amd64"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("empty table: err = %v, want ErrNotFound", err)
	}
	for _, repo := range []string{"o/a", "o/b"} {
		if err := k.MovePointer(ctx, registry.Pointer{Repository: repo, Profile: "node-web", Arch: "amd64", ImageID: "img_aaaaaaaaaaaa", MovedBy: "luis"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := k.MovePointer(ctx, registry.Pointer{Repository: "o/a", Profile: "node-web", Arch: "amd64", ImageID: "img_bbbbbbbbbbbb", MovedAt: time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	p, err := k.Pointer(ctx, "o/a", "node-web", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if p.ImageID != "img_bbbbbbbbbbbb" || p.Previous != "img_aaaaaaaaaaaa" || p.MovedBy != "crsp" || !p.MovedAt.Equal(time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("pointer = %+v", p)
	}
	hold, err := k.PointersTo(ctx, "img_aaaaaaaaaaaa")
	if err != nil || len(hold) != 1 || hold[0].Repository != "o/b" {
		t.Fatalf("holders of the old image = %+v, %v; want only o/b (R5.27)", hold, err)
	}
	all, err := k.PointersOfProfile(ctx, "node-web", "amd64")
	if err != nil || len(all) != 2 {
		t.Fatalf("PointersOfProfile = %+v, %v", all, err)
	}
}

func TestIntegrationRegistryLineageAndBranchGone(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	k := s.RegistryKeys()
	addRegistryImage(t, s, "img_aaaaaaaaaaaa", "o/a", "")
	addRegistryImage(t, s, "img_bbbbbbbbbbbb", "o/a", "feat")
	if err := k.SetReplaces(ctx, "img_bbbbbbbbbbbb", "img_aaaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	r, err := s.q.GetRegistryImage(ctx, "img_bbbbbbbbbbbb")
	if err != nil || r.ReplacesID != "img_aaaaaaaaaaaa" {
		t.Fatalf("replaces = %q, %v", r.ReplacesID, err)
	}
	n, err := k.MarkBranchGone(ctx, "o/a", "feat", time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC))
	if err != nil || n != 1 {
		t.Fatalf("MarkBranchGone = %d, %v", n, err)
	}
	if n, _ = k.MarkBranchGone(ctx, "o/a", "feat", time.Now()); n != 0 {
		t.Fatalf("second mark changed %d rows, want 0", n)
	}
	if err := k.MovePointer(ctx, registry.Pointer{}); err == nil {
		t.Fatal("an empty pointer was accepted")
	}
}
