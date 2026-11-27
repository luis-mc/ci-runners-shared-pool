package keys

import (
	"context"
	"errors"
	"fmt"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// Keyer is registry.Keyer over the two inputs of another task: ProfileSource
// (IMG3) and StackSource (IMG2). Both are interfaces so tests use fakes.
type Keyer struct {
	Profiles registry.ProfileSource
	Stacks   registry.StackSource
}

var _ registry.Keyer = (*Keyer)(nil)

// ErrNoSource is a Keyer built without one of its sources.
var ErrNoSource = errors.New("registry: keyer has no profile or stack source")

// KeyFor computes the key a run needs (R5.21, R5.22, R5.23): the profile of
// the job, the stack hash of the stack files read at commit, the arch in OCI
// form, and the base digest, pins and toolchains of the profile. Two runs of
// the same repository whose stack files are the same get the same key, on any
// branch. Services are not part of it.
func (k *Keyer) KeyFor(ctx context.Context, repository, commit, arch string, labels []string) (registry.Key, registry.KeyInput, registry.ProfileRef, error) {
	if k == nil || k.Profiles == nil || k.Stacks == nil {
		return "", registry.KeyInput{}, registry.ProfileRef{}, ErrNoSource
	}
	ref, err := k.Profiles.ProfileFor(ctx, repository, labels)
	if err != nil {
		return "", registry.KeyInput{}, registry.ProfileRef{}, fmt.Errorf("pick profile of %s: %w", repository, err)
	}
	files, err := k.Stacks.StackFiles(ctx, repository, commit)
	if err != nil {
		return "", registry.KeyInput{}, registry.ProfileRef{}, fmt.Errorf("read stack files of %s at %s: %w", repository, commit, err)
	}
	sum, err := registry.StackHash(files)
	if err != nil {
		return "", registry.KeyInput{}, registry.ProfileRef{}, err
	}
	in := registry.KeyInput{
		Profile:    ref.ID,
		StackHash:  sum,
		Arch:       registry.OCIArch(arch),
		BaseDigest: ref.BaseDigest,
		Pins:       ref.Pins,
		Toolchains: ref.Toolchains,
	}
	key, err := registry.ComputeKey(in)
	if err != nil {
		return "", registry.KeyInput{}, registry.ProfileRef{}, err
	}
	return key, in, ref, nil
}
