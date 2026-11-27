package records

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// Ensure answers "have key K for arch A?" from the records (R5.32): the promoted image of that key and arch is found
// with its full name and manifest digest. Staged, refused and deleted images are not found. The read-only window of
// the cleanup does not stop answers. A store error is "busy": the agent asks again and never builds on it (R5.15).
func (s *Service) Ensure(ctx context.Context, _ string, k registry.Key, arch string) (registry.EnsureAnswer, error) {
	if !k.Valid() {
		return registry.EnsureAnswer{}, nil
	}
	r, err := s.q.GetPromotedRegistryImageByKey(ctx, sqlcgen.GetPromotedRegistryImageByKeyParams{
		ImageTag: k.String(), Arch: registry.OCIArch(arch)})
	if noRows(err) {
		return registry.EnsureAnswer{}, nil
	}
	if err != nil {
		s.cfg.Logger.Warn("registry: ensure could not read the records", "err", err)
		return registry.EnsureAnswer{Busy: true}, nil
	}
	return registry.EnsureAnswer{Found: true, Name: r.Name, Manifest: r.ManifestDigest, Size: r.SizeBytes}, nil
}
