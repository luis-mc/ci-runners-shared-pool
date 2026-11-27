package records

import (
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// The join's ImageStore: put, list with a filter, change a state, read back.
func TestImageStorePutListSetState(t *testing.T) {
	e := newEnv(t)
	is := NewImageStore(e.svc)
	put := func(id string, st registry.State, kind registry.Kind, repo string, key registry.Key) {
		t.Helper()
		err := is.PutImage(e.ctx, registry.Image{
			ID: id, Kind: kind, Name: "crsp/std/base", Key: key, Profile: "base", Arch: "amd64", State: st,
			Manifest:         "sha256:" + string(key[:1]) + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"[:63],
			StackHash:        "3ce86298410301e478baa185c81f75e9d8e02dcf22633e16d02d5ee627d67eb0",
			BaseDigest:       "sha256:1111111111111111111111111111111111111111111111111111111111111111",
			SourceRepository: repo, PushedBy: "m1", PushedAt: t0, PromotedAt: t0,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	put("img_a", registry.StateCurrent, registry.KindStandard, "", "aaaaaaaaaaaa")
	put("img_b", registry.StateVariant, registry.KindCustom, "acme/docs", "bbbbbbbbbbbb")
	put("img_c", registry.StateStaged, registry.KindCustom, "acme/docs", "cccccccccccc")

	got, err := is.Image(e.ctx, "img_b")
	if err != nil || got.State != registry.StateVariant || got.SourceRepository != "acme/docs" || !got.PromotedAt.Equal(t0) {
		t.Fatalf("image %+v %v", got, err)
	}
	if c, _ := is.Image(e.ctx, "img_c"); c.State != registry.StateStaged {
		t.Fatalf("a staged image stays staged: %+v", c)
	}

	vars, err := is.Images(e.ctx, registry.ImageFilter{Kind: registry.KindCustom, States: []registry.State{registry.StateVariant}, Repository: "acme/docs"})
	if err != nil || len(vars) != 1 || vars[0].ID != "img_b" {
		t.Fatalf("variants %+v %v", vars, err)
	}
	all, _ := is.Images(e.ctx, registry.ImageFilter{})
	if len(all) != 3 {
		t.Fatalf("all %d", len(all))
	}

	// The branch merged: the variant becomes current (REG3 uses SetState so).
	if err := is.SetState(e.ctx, "img_b", registry.StateCurrent, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := is.Image(e.ctx, "img_b"); b.State != registry.StateCurrent {
		t.Fatalf("state %s", b.State)
	}
	keep := t0.Add(48 * time.Hour)
	if err := is.SetState(e.ctx, "img_a", registry.StateSuperseded, keep); err != nil {
		t.Fatal(err)
	}
	if a, _ := is.Image(e.ctx, "img_a"); a.State != registry.StateSuperseded || !a.KeepUntil.Equal(keep) {
		t.Fatalf("superseded %+v", a)
	}
}
