package keys

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

func custom(id, stack, branch string) registry.Image {
	return registry.Image{
		ID: id, Kind: registry.KindCustom, Name: "crsp/custom/o-r/node-web", Key: registry.Key(id[:1] + "00000000000"), Profile: "node-web", Arch: "amd64",
		State: registry.StateCurrent, StackHash: stack, SourceRepository: "o/r", Branch: branch,
	}
}

func std(id string) registry.Image {
	return registry.Image{ID: id, Kind: registry.KindStandard, Name: "crsp/std/node-web", Profile: "node-web", Arch: "amd64", State: registry.StateCurrent, StackHash: id}
}

func TestUpdateOfDetected(t *testing.T) {
	ctx := context.Background()
	o, n := custom("old", "s1", ""), custom("new", "s2", "")
	w := newWorld(o, n)
	w.point("o/r", o)
	if err := w.detector().OnPromoted(ctx, n); err != nil {
		t.Fatal(err)
	}
	rec, ok := w.recs[RecommendationID("new")]
	if !ok || rec.Rule != registry.RuleReplace || rec.ImageID != "new" || rec.OtherID != "old" || rec.Decider != registry.DecidedByOwner || !rec.DueAt.IsZero() {
		t.Fatalf("recommendation = %+v, ok=%v", rec, ok)
	}
	if w.images["new"].Replaces != "old" {
		t.Fatal("lineage not recorded for REG7")
	}
	// Detection never moves a pointer (R5.25).
	if p, _ := w.Pointer(ctx, "o/r", "node-web", "amd64"); p.ImageID != "old" || w.moves != 0 {
		t.Fatalf("pointer moved: %+v", p)
	}
}

func TestNoUpdateCases(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		o, n registry.Image
		mut  func(*registry.Image, *registry.Image)
	}{
		"same stack hash":      {custom("old", "s1", ""), custom("new", "s1", ""), nil},
		"other profile":        {custom("old", "s1", ""), custom("new", "s2", ""), func(_, n *registry.Image) { n.Profile = "rust" }},
		"other arch":           {custom("old", "s1", ""), custom("new", "s2", ""), func(_, n *registry.Image) { n.Arch = "arm64" }},
		"old is standard kind": {std("old"), custom("new", "s2", ""), nil},
		"old deleted":          {custom("old", "s1", ""), custom("new", "s2", ""), func(o, _ *registry.Image) { o.State = registry.StateDeleted }},
		"staged":               {custom("old", "s1", ""), custom("new", "s2", ""), func(_, n *registry.Image) { n.State = registry.StateStaged }},
		"no repository":        {custom("old", "s1", ""), custom("new", "s2", ""), func(_, n *registry.Image) { n.SourceRepository = "" }},
	} {
		t.Run(name, func(t *testing.T) {
			o, n := tc.o, tc.n
			if tc.mut != nil {
				tc.mut(&o, &n)
			}
			w := newWorld(o, n)
			w.point("o/r", o)
			w.pointers[[3]string{"o/r", "node-web", "amd64"}] = registry.Pointer{Repository: "o/r", Profile: "node-web", Arch: "amd64", ImageID: "old"}
			if err := w.detector().OnPromoted(ctx, n); err != nil {
				t.Fatal(err)
			}
			if len(w.recs) != 0 {
				t.Fatalf("a recommendation opened: %+v", w.recs)
			}
		})
	}
}

// Only a repository whose pointer holds O gets the update: another repository's
// image is not compared (O is the pointer of a repository N was built for).
func TestOtherRepositoryPointerIsNotCompared(t *testing.T) {
	o, n := custom("old", "s1", ""), custom("new", "s2", "")
	w := newWorld(o, n)
	w.point("o/other", o)
	if err := w.detector().OnPromoted(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if len(w.recs) != 0 {
		t.Fatal("update opened for a repository that does not use O")
	}
	// It is the first image of o/r from the default branch: its pointer is created.
	if p, err := w.Pointer(context.Background(), "o/r", "node-web", "amd64"); err != nil || p.ImageID != "new" || p.MovedBy != CrspMover {
		t.Fatalf("first pointer = %+v, %v", p, err)
	}
}

// R5.26: a branch image is a variant and never an update until it merges; then
// the variant becomes the update with no new build.
func TestBranchVariantThenMerge(t *testing.T) {
	ctx := context.Background()
	o, v := custom("old", "s1", ""), custom("var", "s2", "feat")
	v.State = registry.StateVariant
	w := newWorld(o, v)
	w.point("o/r", o)
	d := w.detector()
	if err := d.OnPromoted(ctx, v); err != nil {
		t.Fatal(err)
	}
	if len(w.recs) != 0 || w.images["var"].Replaces != "" {
		t.Fatal("a branch variant was treated as an update")
	}
	// A variant with no pointer yet does not get one either.
	w2 := newWorld(v)
	if err := w2.detector().OnPromoted(ctx, v); err != nil || len(w2.pointers) != 0 {
		t.Fatalf("variant got a pointer: %v %v", w2.pointers, err)
	}

	changed, err := d.OnBranchMerged(ctx, "o/r", "feat")
	if err != nil || len(changed) != 1 {
		t.Fatalf("OnBranchMerged = %v, %v", changed, err)
	}
	if got := w.images["var"]; got.State != registry.StateCurrent || got.BranchGoneAt.IsZero() || got.Replaces != "old" {
		t.Fatalf("variant after merge = %+v", got)
	}
	if rec, ok := w.recs[RecommendationID("var")]; !ok || rec.OtherID != "old" {
		t.Fatalf("no replace recommendation after the merge: %+v", w.recs)
	}
	if w.moves != 0 {
		t.Fatal("merge moved a pointer")
	}
	// Another branch is left alone.
	other := custom("oth", "s3", "other")
	other.State = registry.StateVariant
	w.images["oth"] = other
	if _, err := d.OnBranchMerged(ctx, "o/r", "feat"); err != nil || w.images["oth"].State != registry.StateVariant {
		t.Fatalf("other branch touched: %+v %v", w.images["oth"], err)
	}
}

// R5.24: a standard pointer moves alone; a custom one never does.
func TestStandardPointerMovesAloneCustomNever(t *testing.T) {
	ctx := context.Background()
	s1, s2 := std("s1"), std("s2")
	c1, c2 := custom("c1", "a", ""), custom("c2", "b", "")
	w := newWorld(s1, s2, c1, c2)
	w.pointers[[3]string{"o/a", "node-web", "amd64"}] = registry.Pointer{Repository: "o/a", Profile: "node-web", Arch: "amd64", ImageID: "s1"}
	w.pointers[[3]string{"o/b", "node-web", "amd64"}] = registry.Pointer{Repository: "o/b", Profile: "node-web", Arch: "amd64", ImageID: "c1"}
	d := w.detector()
	if err := d.OnPromoted(ctx, s2); err != nil {
		t.Fatal(err)
	}
	a, _ := w.Pointer(ctx, "o/a", "node-web", "amd64")
	b, _ := w.Pointer(ctx, "o/b", "node-web", "amd64")
	if a.ImageID != "s2" || a.Previous != "s1" || a.MovedBy != CrspMover {
		t.Fatalf("standard pointer = %+v", a)
	}
	if b.ImageID != "c1" {
		t.Fatalf("custom pointer moved alone: %+v", b)
	}
	if err := d.Pointers.MoveAlone(ctx, "o/b", "node-web", "amd64", "c2"); !errors.Is(err, ErrCustomPointer) {
		t.Fatalf("MoveAlone custom: %v", err)
	}
	if err := d.Pointers.MoveAlone(ctx, "o/a", "node-web", "amd64", "c2"); !errors.Is(err, ErrCustomPointer) {
		t.Fatalf("MoveAlone standard to custom: %v", err)
	}
	if err := d.Pointers.MoveAlone(ctx, "o/none", "node-web", "amd64", "s2"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("MoveAlone without pointer: %v", err)
	}
}

// R5.27: moving one repository's pointer leaves the old image held by the others.
func TestPointerPerRepository(t *testing.T) {
	ctx := context.Background()
	s1, s2 := std("s1"), std("s2")
	w := newWorld(s1, s2)
	w.pointers[[3]string{"o/a", "node-web", "amd64"}] = registry.Pointer{Repository: "o/a", Profile: "node-web", Arch: "amd64", ImageID: "s1"}
	w.pointers[[3]string{"o/b", "node-web", "amd64"}] = registry.Pointer{Repository: "o/b", Profile: "node-web", Arch: "amd64", ImageID: "s1"}
	p := &Pointers{Lineage: w, Images: w, Now: time.Now}
	if err := p.MoveAlone(ctx, "o/a", "node-web", "amd64", "s2"); err != nil {
		t.Fatal(err)
	}
	hold, _ := w.PointersTo(ctx, "s1")
	if len(hold) != 1 || hold[0].Repository != "o/b" {
		t.Fatalf("holders of s1 = %+v", hold)
	}
	// EnsurePointer never changes an existing pointer.
	if created, err := p.EnsurePointer(ctx, "o/b", s2); err != nil || created {
		t.Fatalf("EnsurePointer over existing: %v %v", created, err)
	}
	if created, err := p.EnsurePointer(ctx, "o/c", s2); err != nil || !created {
		t.Fatalf("EnsurePointer new: %v %v", created, err)
	}
	if _, err := p.EnsurePointer(ctx, "", s2); err == nil {
		t.Fatal("empty repository accepted")
	}
}
