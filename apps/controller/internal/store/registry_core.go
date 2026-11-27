package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// RegistryCore is the store half of REG1 (registry R5.5, R5.6, R5.10): the
// staged and promoted image rows, the immutable tag rows and the token
// leases. The queries are in queries/registry_core.sql. It implements the
// Ledger and the Leases of internal/registry/core.
type RegistryCore struct{ s *Store }

// RegistryCore returns the registry core view of the store.
func (s *Store) RegistryCore() *RegistryCore { return &RegistryCore{s: s} }

// TagExists says whether (name, tag) has a row.
func (r *RegistryCore) TagExists(ctx context.Context, name, tag string) (bool, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	_, err := r.s.q.GetRegistryTag(ctx, sqlcgen.GetRegistryTagParams{Name: name, Tag: tag})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Stage inserts the staged image row.
func (r *RegistryCore) Stage(ctx context.Context, img registry.Image, keyInputJSON string) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	if keyInputJSON == "" {
		keyInputJSON = "{}"
	}
	return r.s.q.InsertRegistryImage(ctx, sqlcgen.InsertRegistryImageParams{
		ID: img.ID, Kind: string(img.Kind), Name: img.Name, ImageTag: img.Key.String(), ProfileID: img.Profile,
		Arch: img.Arch, State: string(registry.StateStaged), ManifestDigest: img.Manifest, StagedName: registry.IncomingName(img.PushedBy, img.Name),
		StackHash: img.StackHash, BaseName: img.BaseName, BaseDigest: img.BaseDigest, KeyInput: keyInputJSON,
		SourceRepository: img.SourceRepository, CommitSha: img.Commit, Branch: img.Branch, PushedBy: img.PushedBy,
		PushedAt: registryTime(img.PushedAt),
	})
}

// Refuse marks a staged image refused with the reason.
func (r *RegistryCore) Refuse(ctx context.Context, id, why string) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	_, err := r.s.q.RefuseRegistryImage(ctx, sqlcgen.RefuseRegistryImageParams{RefusedWhy: why, ID: id})
	return err
}

// Promoted writes the tag row and moves the staged image to img.State, in one
// transaction. The tag row is the one that cannot be written twice (R5.6).
func (r *RegistryCore) Promoted(ctx context.Context, img registry.Image) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return r.s.inTx(ctx, func(ctx context.Context, _ *sql.Tx, q *sqlcgen.Queries) error {
		if err := q.InsertRegistryTag(ctx, sqlcgen.InsertRegistryTagParams{
			Name: img.Name, Tag: img.Key.String(), ImageID: img.ID, ManifestDigest: img.Manifest,
		}); err != nil {
			return fmt.Errorf("tag row: %w", err)
		}
		n, err := q.PromoteRegistryImage(ctx, sqlcgen.PromoteRegistryImageParams{
			State: string(img.State), PromotedAt: registryTime(img.PromotedAt), BaseName: img.BaseName, SizeBytes: img.SizeBytes, ID: img.ID,
		})
		if err != nil {
			return err
		}
		if n != 1 {
			return registry.ErrNotFound
		}
		return nil
	})
}

// Lease records a token: its id, machine and names, never its value.
func (r *RegistryCore) Lease(ctx context.Context, jti, machineID string, names []string, expires time.Time) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	b, err := json.Marshal(names)
	if err != nil {
		return err
	}
	return r.s.q.InsertRegistryTokenLease(ctx, sqlcgen.InsertRegistryTokenLeaseParams{
		Jti: jti, MachineID: machineID, Names: string(b), ExpiresAt: registryTime(expires),
	})
}

// Live says whether the lease of a token exists and has not ended.
func (r *RegistryCore) Live(ctx context.Context, jti string, now time.Time) (bool, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	row, err := r.s.q.GetRegistryTokenLease(ctx, jti)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return parseRegistryTime(row.ExpiresAt).After(now), nil
}

// Revoke deletes a lease, which ends its token.
func (r *RegistryCore) Revoke(ctx context.Context, jti string) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	_, err := r.s.q.DeleteRegistryTokenLease(ctx, jti)
	return err
}

// PruneLeases deletes the leases that ended before now.
func (r *RegistryCore) PruneLeases(ctx context.Context, now time.Time) (int64, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return r.s.q.DeleteEndedRegistryTokenLeases(ctx, registryTime(now))
}
