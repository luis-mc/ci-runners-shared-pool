package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// This file is the store half of REG3 (registry R5.24 to R5.27): the pointer
// table per (repository, profile, arch) and the lineage columns of an image.
// The queries are in queries/registry_keys.sql. Rules about who may move a
// pointer are in internal/registry/keys; this file only reads and writes.

// RegistryKeysStore is the pointer and lineage view of the store. It
// implements registry.PointerStore.
type RegistryKeysStore struct{ *Store }

var _ registry.PointerStore = (*RegistryKeysStore)(nil)

// RegistryKeys returns the pointer and lineage view of the store.
func (s *Store) RegistryKeys() *RegistryKeysStore { return &RegistryKeysStore{s} }

const registryTimeLayout = "2006-01-02T15:04:05.000Z"

func registryTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(registryTimeLayout)
}

func parseRegistryTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func pointerOf(r sqlcgen.RegistryPointer) registry.Pointer {
	return registry.Pointer{
		Repository: r.Repository, Profile: r.ProfileID, Arch: r.Arch, ImageID: r.ImageID,
		Previous: r.PreviousImageID, MovedAt: parseRegistryTime(r.MovedAt), MovedBy: r.MovedBy,
	}
}

func pointersOf(rows []sqlcgen.RegistryPointer) []registry.Pointer {
	out := make([]registry.Pointer, 0, len(rows))
	for _, r := range rows {
		out = append(out, pointerOf(r))
	}
	return out
}

// Pointer returns the pointer of (repository, profile, arch), or
// registry.ErrNotFound.
func (s *RegistryKeysStore) Pointer(ctx context.Context, repository, profile, arch string) (registry.Pointer, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	r, err := s.q.GetRegistryPointer(ctx, sqlcgen.GetRegistryPointerParams{Repository: repository, ProfileID: profile, Arch: arch})
	if errors.Is(err, sql.ErrNoRows) {
		return registry.Pointer{}, registry.ErrNotFound
	}
	if err != nil {
		return registry.Pointer{}, err
	}
	return pointerOf(r), nil
}

// PointersTo returns every pointer that refers to the image.
func (s *RegistryKeysStore) PointersTo(ctx context.Context, imageID string) ([]registry.Pointer, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.q.ListRegistryPointersTo(ctx, imageID)
	if err != nil {
		return nil, err
	}
	return pointersOf(rows), nil
}

// PointersOfProfile returns every pointer of a profile and arch, all repositories.
func (s *RegistryKeysStore) PointersOfProfile(ctx context.Context, profile, arch string) ([]registry.Pointer, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.q.ListRegistryPointersOfProfile(ctx, sqlcgen.ListRegistryPointersOfProfileParams{ProfileID: profile, Arch: arch})
	if err != nil {
		return nil, err
	}
	return pointersOf(rows), nil
}

// MovePointer sets the pointer and keeps the image it had as Previous. Only
// the row of this repository changes, so another repository's pointer keeps
// the old image (R5.27). Zero MovedAt is now; empty MovedBy is "crsp". The
// Previous field of p is ignored: the store takes it from the row.
func (s *RegistryKeysStore) MovePointer(ctx context.Context, p registry.Pointer) error {
	if p.Repository == "" || p.Profile == "" || p.Arch == "" || p.ImageID == "" {
		return errors.New("registry: a pointer needs repository, profile, arch and image")
	}
	if p.MovedAt.IsZero() {
		p.MovedAt = time.Now()
	}
	if p.MovedBy == "" {
		p.MovedBy = "crsp"
	}
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return s.q.UpsertRegistryPointer(ctx, sqlcgen.UpsertRegistryPointerParams{
		Repository: p.Repository, ProfileID: p.Profile, Arch: p.Arch, ImageID: p.ImageID,
		MovedAt: registryTime(p.MovedAt), MovedBy: p.MovedBy,
	})
}

// SetReplaces records that image newID is an update of oldID (R5.25). REG7
// reads it. It does not touch any pointer.
func (s *RegistryKeysStore) SetReplaces(ctx context.Context, newID, oldID string) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return s.q.SetRegistryImageLineage(ctx, sqlcgen.SetRegistryImageLineageParams{ReplacesID: oldID, ID: newID})
}

// MarkBranchGone records that a branch merged, for every image of it that has
// no date yet; it returns how many rows changed.
func (s *RegistryKeysStore) MarkBranchGone(ctx context.Context, repository, branch string, at time.Time) (int64, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return s.q.SetRegistryBranchGone(ctx, sqlcgen.SetRegistryBranchGoneParams{BranchGoneAt: registryTime(at), SourceRepository: repository, Branch: branch})
}
