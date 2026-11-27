package records

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// ImageStore is registry.ImageStore over the generated queries: the reads of
// the Service plus the listing, the insert and the state change the pointer
// rules (REG3), replace (REG7) and the registry core (REG1) need. The controller
// wires one value of it (the join, REG11).
type ImageStore struct {
	*Service
}

var _ registry.ImageStore = (*ImageStore)(nil)

// NewImageStore wraps a Service.
func NewImageStore(s *Service) *ImageStore { return &ImageStore{Service: s} }

// Images lists the images that are not deleted, newest promotion first. A
// zero field matches everything. Repository matches the repository the image
// was built for.
func (s *ImageStore) Images(ctx context.Context, f registry.ImageFilter) ([]registry.Image, error) {
	rows, err := s.q.ListRegistryImages(ctx, sqlcgen.ListRegistryImagesParams{
		Kind: string(f.Kind), Arch: f.Arch, ProfileID: f.Profile,
	})
	if err != nil {
		return nil, err
	}
	out := make([]registry.Image, 0, len(rows))
	for _, r := range rows {
		img := toImage(r)
		if len(f.States) > 0 && !slices.Contains(f.States, img.State) {
			continue
		}
		if f.Repository != "" && img.SourceRepository != f.Repository {
			continue
		}
		out = append(out, img)
	}
	return out, nil
}

// PutImage records a new image. It is inserted as staged; an image given in
// another state is promoted at once with its PromotedAt (or now).
func (s *ImageStore) PutImage(ctx context.Context, img registry.Image) error {
	// The pins and toolchains are not part of an Image; the core that has the
	// whole KeyInput records it. A canonical form that cannot be made is kept empty.
	input, cerr := registry.CanonicalJSON(registry.KeyInput{Profile: img.Profile, StackHash: img.StackHash, Arch: img.Arch, BaseDigest: img.BaseDigest})
	if cerr != nil {
		input = nil
	}
	err := s.q.InsertRegistryImage(ctx, sqlcgen.InsertRegistryImageParams{
		ID: img.ID, Kind: string(img.Kind), Name: img.Name, ImageTag: img.Key.String(), ProfileID: img.Profile, Arch: img.Arch,
		State: string(registry.StateStaged), ManifestDigest: img.Manifest, StackHash: img.StackHash, BaseName: img.BaseName,
		BaseDigest: img.BaseDigest, KeyInput: string(input), SourceRepository: img.SourceRepository, CommitSha: img.Commit,
		Branch: img.Branch, PushedBy: img.PushedBy, PushedAt: stamp(img.PushedAt),
	})
	if err != nil {
		return fmt.Errorf("records: put image %s: %w", img.ID, err)
	}
	if img.State == registry.StateStaged || img.State == "" {
		return nil
	}
	at := img.PromotedAt
	if at.IsZero() {
		at = s.now()
	}
	if _, err := s.q.PromoteRegistryImage(ctx, sqlcgen.PromoteRegistryImageParams{State: string(img.State), PromotedAt: stamp(at), ID: img.ID}); err != nil {
		return fmt.Errorf("records: promote image %s: %w", img.ID, err)
	}
	return nil
}

// SetState changes the state of an image that is not deleted; keepUntil is the
// zero time unless the image is superseded.
func (s *ImageStore) SetState(ctx context.Context, id string, st registry.State, keepUntil time.Time) error {
	return s.q.SetRegistryImageState(ctx, sqlcgen.SetRegistryImageStateParams{State: string(st), KeepUntil: stamp(keepUntil), ID: id})
}
