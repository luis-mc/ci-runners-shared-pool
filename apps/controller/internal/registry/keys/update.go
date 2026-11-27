package keys

import (
	"context"
	"errors"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// Detector is registry.UpdateDetector (R5.25, R5.26). It also keeps the
// pointer rules: a first custom image of a repository becomes its pointer, and
// a new standard image moves the standard pointers alone. It never moves a
// custom pointer that exists.
type Detector struct {
	Pointers *Pointers
	Images   registry.ImageStore
	Recs     registry.RecommendationStore
	Now      func() time.Time
}

var _ registry.UpdateDetector = (*Detector)(nil)

func (d *Detector) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// RecommendationID is the id of the replace recommendation for a new image.
func RecommendationID(newImageID string) string { return "rec_replace_" + newImageID }

// OnPromoted is the Promoted hook of REG3 (registry.PromotedHook).
func (d *Detector) OnPromoted(ctx context.Context, img registry.Image) error {
	if img.State != registry.StateCurrent && img.State != registry.StateVariant {
		return nil
	}
	switch img.Kind {
	case registry.KindStandard:
		_, err := d.Pointers.FollowStandard(ctx, img)
		return err
	case registry.KindCustom:
		return d.detectCustom(ctx, img)
	}
	return nil
}

// detectCustom applies R5.25: image N is an update of O when same kind,
// profile and arch; O is the pointer of a repository N was built for; the
// stack hashes differ; N came from the default branch or a merged branch; and
// O has a pointer (which it has, being the pointer's image). Then a replace
// recommendation opens and the lineage is recorded for REG7.
func (d *Detector) detectCustom(ctx context.Context, n registry.Image) error {
	repo := n.SourceRepository
	if repo == "" {
		return nil
	}
	ptr, err := d.Pointers.Lineage.Pointer(ctx, repo, n.Profile, n.Arch)
	if errors.Is(err, registry.ErrNotFound) {
		// A first image of the lineage from the default branch is the
		// repository's first use, not an update. A branch run waits as a variant.
		if n.Branch == "" && n.State == registry.StateCurrent {
			_, err = d.Pointers.EnsurePointer(ctx, repo, n)
			return err
		}
		return nil
	}
	if err != nil {
		return err
	}
	if ptr.ImageID == n.ID {
		return nil
	}
	o, err := d.Images.Image(ctx, ptr.ImageID)
	if errors.Is(err, registry.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if o.Kind != n.Kind || o.Profile != n.Profile || o.Arch != n.Arch || o.State == registry.StateDeleted {
		return nil
	}
	if o.StackHash == n.StackHash {
		return nil
	}
	// R5.26: a branch run is a variant, never an update, until it merges.
	if n.Branch != "" && n.BranchGoneAt.IsZero() {
		return nil
	}
	if err := d.Pointers.Lineage.SetReplaces(ctx, n.ID, o.ID); err != nil {
		return err
	}
	return d.Recs.Upsert(ctx, registry.Recommendation{
		ID: RecommendationID(n.ID), Rule: registry.RuleReplace, ImageID: n.ID, OtherID: o.ID,
		Kind: n.Kind, Decider: registry.DecidedByOwner, CreatedAt: d.now(),
	})
}

// OnBranchMerged is told that a branch of repository merged into the default
// branch. Its variants become current with no new build, since after a merge
// the key is the same image, and are checked as updates (R5.26). It returns
// the images it changed.
func (d *Detector) OnBranchMerged(ctx context.Context, repository, branch string) ([]registry.Image, error) {
	at := d.now()
	if _, err := d.Pointers.Lineage.MarkBranchGone(ctx, repository, branch, at); err != nil {
		return nil, err
	}
	vars, err := d.Images.Images(ctx, registry.ImageFilter{Kind: registry.KindCustom, States: []registry.State{registry.StateVariant}, Repository: repository})
	if err != nil {
		return nil, err
	}
	var out []registry.Image
	for _, v := range vars {
		if v.Branch != branch || v.SourceRepository != repository {
			continue
		}
		if err := d.Images.SetState(ctx, v.ID, registry.StateCurrent, time.Time{}); err != nil {
			return out, err
		}
		v.State = registry.StateCurrent
		if v.BranchGoneAt.IsZero() {
			v.BranchGoneAt = at
		}
		out = append(out, v)
		if err := d.detectCustom(ctx, v); err != nil {
			return out, err
		}
	}
	return out, nil
}
