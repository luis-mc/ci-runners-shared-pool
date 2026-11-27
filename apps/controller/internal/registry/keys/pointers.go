package keys

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// ErrCustomPointer is a custom pointer asked to move without the owner's yes.
// A custom pointer moves only through REG7's replace plan (R5.24).
var ErrCustomPointer = errors.New("registry: a custom pointer moves only with the owner's yes")

// CrspMover is the moved_by of a move crsp makes alone.
const CrspMover = "crsp"

// LineageStore is what the rules need from the store: the pointer table (REG3's
// own queries/registry_keys.sql) and the lineage columns of an image.
// store.RegistryKeysStore implements it.
type LineageStore interface {
	registry.PointerStore
	// PointersOfProfile lists the pointers of a profile and arch, all repositories.
	PointersOfProfile(ctx context.Context, profile, arch string) ([]registry.Pointer, error)
	// SetReplaces records that newID is an update of oldID.
	SetReplaces(ctx context.Context, newID, oldID string) error
	// MarkBranchGone dates the images of a merged branch; it returns the rows changed.
	MarkBranchGone(ctx context.Context, repository, branch string, at time.Time) (int64, error)
}

// Pointers applies the pointer rules of R5.24 and R5.27 over a Lineage.
type Pointers struct {
	Lineage LineageStore
	Images  registry.ImageStore
	Now     func() time.Time
}

func (p *Pointers) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// EnsurePointer makes img the pointer of (repository, img.Profile, img.Arch)
// when the repository has none yet; that is a first use, not a move. It
// returns whether it created one. A pointer that exists is never changed.
func (p *Pointers) EnsurePointer(ctx context.Context, repository string, img registry.Image) (bool, error) {
	if repository == "" {
		return false, fmt.Errorf("registry: a pointer needs a repository")
	}
	_, err := p.Lineage.Pointer(ctx, repository, img.Profile, img.Arch)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, registry.ErrNotFound) {
		return false, err
	}
	err = p.Lineage.MovePointer(ctx, registry.Pointer{
		Repository: repository, Profile: img.Profile, Arch: img.Arch, ImageID: img.ID,
		MovedAt: p.now(), MovedBy: CrspMover,
	})
	return err == nil, err
}

// MoveAlone moves a pointer without the owner. It does so only when both the
// image the pointer holds and the new image are standard (R5.24: crsp updates
// its own pins); otherwise it returns ErrCustomPointer and changes nothing.
// Only the row of this repository changes, so the old image stays held by
// every other repository's pointer (R5.27).
func (p *Pointers) MoveAlone(ctx context.Context, repository, profile, arch, newImageID string) error {
	cur, err := p.Lineage.Pointer(ctx, repository, profile, arch)
	if err != nil {
		return err
	}
	if cur.ImageID == newImageID {
		return nil
	}
	for _, id := range []string{cur.ImageID, newImageID} {
		img, err := p.Images.Image(ctx, id)
		if err != nil {
			return err
		}
		if img.Kind != registry.KindStandard {
			return ErrCustomPointer
		}
	}
	return p.Lineage.MovePointer(ctx, registry.Pointer{
		Repository: repository, Profile: profile, Arch: arch, ImageID: newImageID,
		MovedAt: p.now(), MovedBy: CrspMover,
	})
}

// FollowStandard moves every pointer that holds an older standard image of
// the new standard image's profile and arch to it. It returns how many moved.
// Custom pointers are left alone.
func (p *Pointers) FollowStandard(ctx context.Context, img registry.Image) (int, error) {
	if img.Kind != registry.KindStandard || img.State != registry.StateCurrent {
		return 0, nil
	}
	ptrs, err := p.Lineage.PointersOfProfile(ctx, img.Profile, img.Arch)
	if err != nil {
		return 0, err
	}
	moved := 0
	for _, ptr := range ptrs {
		if ptr.ImageID == img.ID {
			continue
		}
		err := p.MoveAlone(ctx, ptr.Repository, ptr.Profile, ptr.Arch, img.ID)
		if errors.Is(err, ErrCustomPointer) {
			continue
		}
		if err != nil {
			return moved, err
		}
		moved++
	}
	return moved, nil
}
