package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/profiles"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

var _ profiles.Store = (*ProfileStore)(nil)

// ProfileStore is profiles.Store over the image_profiles and repository_profiles tables.
type ProfileStore struct{ s *Store }

// Profiles returns the keeper of the image profiles (IMG3).
func (s *Store) Profiles() *ProfileStore { return &ProfileStore{s: s} }

// NewProfileStore wraps a database whose migrations are applied.

func encode(p profiles.Profile) (pins, tc, svc, labels, stacks string, err error) {
	pm := p.Pins
	if pm == nil {
		pm = map[string]string{}
	}
	tcs := p.Toolchains
	if tcs == nil {
		tcs = []registry.Toolchain{}
	}
	svs := p.Services
	if svs == nil {
		svs = []profiles.Service{}
	}
	ls := p.Labels
	if ls == nil {
		ls = []string{}
	}
	ss := p.Stacks
	if ss == nil {
		ss = []string{}
	}
	var parts [5][]byte
	for i, v := range []any{pm, tcs, svs, ls, ss} {
		if parts[i], err = json.Marshal(v); err != nil {
			return
		}
	}
	return string(parts[0]), string(parts[1]), string(parts[2]), string(parts[3]), string(parts[4]), nil
}

func decode(r sqlcgen.ImageProfile) (profiles.Profile, error) {
	p := profiles.Profile{
		ID: r.ID, Kind: registry.Kind(r.Kind), Repository: r.Repository, Title: r.Title, BaseName: r.BaseName,
		BaseDigest: r.BaseDigest, BuiltIn: r.BuiltIn != 0,
	}
	for _, f := range []struct {
		src string
		dst any
	}{{r.Pins, &p.Pins}, {r.Toolchains, &p.Toolchains}, {r.Services, &p.Services}, {r.Labels, &p.Labels}, {r.Stacks, &p.Stacks}} {
		if err := json.Unmarshal([]byte(f.src), f.dst); err != nil {
			return profiles.Profile{}, fmt.Errorf("profiles: profile %s: %w", r.ID, err)
		}
	}
	return p, nil
}

func (s *ProfileStore) Get(ctx context.Context, id string) (profiles.Profile, error) {
	r, err := s.s.q.GetImageProfile(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return profiles.Profile{}, profiles.ErrNotFound
	}
	if err != nil {
		return profiles.Profile{}, err
	}
	return decode(r)
}

func (s *ProfileStore) List(ctx context.Context) ([]profiles.Profile, error) {
	rows, err := s.s.q.ListImageProfiles(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]profiles.Profile, 0, len(rows))
	for _, r := range rows {
		p, err := decode(r)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *ProfileStore) Put(ctx context.Context, p profiles.Profile) error {
	if err := p.Validate(); err != nil {
		return err
	}
	pins, tc, svc, labels, stacks, err := encode(p)
	if err != nil {
		return err
	}
	return s.s.q.UpsertImageProfile(ctx, sqlcgen.UpsertImageProfileParams{
		ID: p.ID, Kind: string(p.Kind), Repository: p.Repository, Title: p.Title, BaseName: p.BaseName,
		BaseDigest: p.BaseDigest, Pins: pins, Toolchains: tc, Services: svc, Labels: labels, Stacks: stacks,
		BuiltIn: b2i(p.BuiltIn),
	})
}

func (s *ProfileStore) SeedIfAbsent(ctx context.Context, p profiles.Profile) error {
	if err := p.Validate(); err != nil {
		return err
	}
	pins, tc, svc, labels, stacks, err := encode(p)
	if err != nil {
		return err
	}
	return s.s.q.InsertImageProfileIfAbsent(ctx, sqlcgen.InsertImageProfileIfAbsentParams{
		ID: p.ID, Kind: string(p.Kind), Repository: p.Repository, Title: p.Title, BaseName: p.BaseName,
		BaseDigest: p.BaseDigest, Pins: pins, Toolchains: tc, Services: svc, Labels: labels, Stacks: stacks,
		BuiltIn: b2i(p.BuiltIn),
	})
}

// Delete removes a custom profile; a built-in one stays (profiles.ErrNotFound).
func (s *ProfileStore) Delete(ctx context.Context, id string) error {
	n, err := s.s.q.DeleteImageProfile(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return profiles.ErrNotFound
	}
	return nil
}

func (s *ProfileStore) SetRepositoryProfile(ctx context.Context, repository, profileID string) error {
	return s.s.q.SetRepositoryProfile(ctx, sqlcgen.SetRepositoryProfileParams{Repository: repository, ProfileID: profileID})
}

func (s *ProfileStore) RepositoryProfile(ctx context.Context, repository string) (string, error) {
	id, err := s.s.q.GetRepositoryProfile(ctx, repository)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (s *ProfileStore) ClearRepositoryProfile(ctx context.Context, repository string) error {
	_, err := s.s.q.ClearRepositoryProfile(ctx, repository)
	return err
}

func (s *ProfileStore) RepositoryProfiles(ctx context.Context) (map[string]string, error) {
	rows, err := s.s.q.ListRepositoryProfiles(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Repository] = r.ProfileID
	}
	return out, nil
}
