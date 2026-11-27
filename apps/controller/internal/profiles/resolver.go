package profiles

import (
	"context"
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// StackLookup gives the stack names of a repository (from the stack scan of
// IMG2; DetectStacks names them). A nil lookup means no stack is known.
type StackLookup interface {
	StacksOf(ctx context.Context, repository string) ([]string, error)
}

// BaseDigests resolves a base name to its digest (the mirror, REG9). A nil
// resolver means a profile must carry its digest.
type BaseDigests interface {
	Digest(ctx context.Context, baseName string) (string, error)
}

// Resolver implements registry.ProfileSource (R5.78).
type Resolver struct {
	Store  Store
	Stacks StackLookup
	Bases  BaseDigests
}

var _ registry.ProfileSource = (*Resolver)(nil)

func norm(repo string) string { return strings.ToLower(strings.TrimSpace(repo)) }

// visible is the standard profiles and the custom ones of this repository.
func visible(all []Profile, repository string) []Profile {
	var out []Profile
	for _, p := range all {
		if p.Kind == registry.KindStandard || norm(p.Repository) == repository {
			out = append(out, p)
		}
	}
	return out
}

// Pick chooses the profile. Order: a profile whose label is among the job's
// labels (the first in profile order when several), the profile the owner
// chose for the repository, the profile of the repository's stack, the base.
func (r *Resolver) Pick(ctx context.Context, repository string, labels []string) (Profile, error) {
	repo := norm(repository)
	all, err := r.Store.List(ctx)
	if err != nil {
		return Profile{}, err
	}
	cand := visible(all, repo)
	jobLabels := map[string]bool{}
	for _, l := range labels {
		jobLabels[strings.ToLower(strings.TrimSpace(l))] = true
	}
	for _, p := range cand {
		for _, l := range p.Labels {
			if jobLabels[strings.ToLower(l)] {
				return p, nil
			}
		}
	}
	if id, err := r.Store.RepositoryProfile(ctx, repo); err != nil {
		return Profile{}, err
	} else if id != "" {
		for _, p := range cand {
			if p.ID == id {
				return p, nil
			}
		}
		// A chosen profile that is gone or belongs to another repository is ignored.
	}
	var stacks []string
	if r.Stacks != nil {
		if stacks, err = r.Stacks.StacksOf(ctx, repo); err != nil {
			return Profile{}, err
		}
	}
	if p, ok := ProfileForStack(cand, stacks); ok {
		return p, nil
	}
	return Profile{}, fmt.Errorf("%w: no profile for %s", ErrNotFound, repository)
}

// ProfileFor picks the profile of a job by its repository and labels and
// returns what the key needs (R5.78). Per-arch builds of one profile get the
// same ref; the arch enters the key elsewhere (R5.79).
func (r *Resolver) ProfileFor(ctx context.Context, repository string, labels []string) (registry.ProfileRef, error) {
	p, err := r.Pick(ctx, repository, labels)
	if err != nil {
		return registry.ProfileRef{}, err
	}
	if p.BaseDigest == "" && r.Bases != nil {
		d, err := r.Bases.Digest(ctx, p.BaseName)
		if err != nil {
			return registry.ProfileRef{}, err
		}
		p.BaseDigest = d
	}
	if p.BaseDigest == "" {
		return registry.ProfileRef{}, fmt.Errorf("%w: %s", ErrBaseUnresolved, p.BaseName)
	}
	return p.Ref(), nil
}
