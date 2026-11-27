package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// MirrorSettings reads the pull-through mirror settings (registry R5.63 to
// R5.67); it implements registry.MirrorSettingsStore with PutMirrorSettings.
func (s *Store) MirrorSettings(ctx context.Context) (registry.MirrorSettings, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	row, err := s.q.GetRegistryMirrorSettings(ctx)
	if err != nil {
		return registry.MirrorSettings{}, fmt.Errorf("read the mirror settings: %w", err)
	}
	var ups []string
	if err := json.Unmarshal([]byte(row.Upstreams), &ups); err != nil {
		return registry.MirrorSettings{}, fmt.Errorf("read the mirror upstreams: %w", err)
	}
	return registry.MirrorSettings{
		Enabled:     row.Enabled != 0,
		Upstreams:   ups,
		Folder:      row.Folder,
		BudgetBytes: row.BudgetBytes,
		TagRecheck:  time.Duration(row.TagRecheckMinutes) * time.Minute,
	}, nil
}

// PutMirrorSettings replaces the mirror settings. by is the login of the actor.
func (s *Store) PutMirrorSettings(ctx context.Context, m registry.MirrorSettings, by string) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	ups, err := json.Marshal(append([]string{}, m.Upstreams...))
	if err != nil {
		return fmt.Errorf("write the mirror upstreams: %w", err)
	}
	var enabled int64
	if m.Enabled {
		enabled = 1
	}
	if err := s.q.UpdateRegistryMirrorSettings(ctx, sqlcgen.UpdateRegistryMirrorSettingsParams{
		Enabled: enabled, Upstreams: string(ups), Folder: m.Folder, BudgetBytes: m.BudgetBytes,
		TagRecheckMinutes: int64(m.TagRecheck / time.Minute), UpdatedBy: by,
	}); err != nil {
		return fmt.Errorf("write the mirror settings: %w", err)
	}
	return nil
}

var _ registry.MirrorSettingsStore = (*Store)(nil)
