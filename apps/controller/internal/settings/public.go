package settings

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// keyPublic is the settings row of the public repositories switch
// (github-integration R5.54).
const keyPublic = "public_repositories"

// Audit actions of the switch (github-integration R5.57).
const (
	ActionPublicOn  = "public_repositories.on"
	ActionPublicOff = "public_repositories.off"
)

// PublicRepositories says whether crsp serves public repositories. It is off
// until the owner turns it on: crsp offers private repositories by default.
type PublicRepositories struct {
	Enabled   bool      `json:"enabled"`
	ChangedAt time.Time `json:"changed_at"`
	ChangedBy string    `json:"changed_by"`
}

// GetPublicRepositories returns the switch; a missing or unreadable row is off.
func (s *Service) GetPublicRepositories(ctx context.Context) (PublicRepositories, error) {
	raw, ok, err := s.raw(ctx, keyPublic)
	if err != nil || !ok {
		return PublicRepositories{}, err
	}
	var v PublicRepositories
	if json.Unmarshal([]byte(raw), &v) != nil {
		return PublicRepositories{}, nil
	}
	return v, nil
}

// PublicRepositoriesOn is the plain question the other areas ask.
func (s *Service) PublicRepositoriesOn(ctx context.Context) (bool, error) {
	v, err := s.GetPublicRepositories(ctx)
	return v.Enabled, err
}

// SetPublicRepositories stores the switch and writes one audit row
// (public_repositories.on or .off) in the same transaction. It returns the new
// value and whether it changed; asking for the value it already has writes
// nothing.
func (s *Service) SetPublicRepositories(ctx context.Context, actor audit.Actor, enabled bool) (PublicRepositories, bool, error) {
	before, err := s.GetPublicRepositories(ctx)
	if err != nil {
		return PublicRepositories{}, false, err
	}
	if before.Enabled == enabled {
		return before, false, nil
	}
	after := PublicRepositories{Enabled: enabled, ChangedAt: time.Now().UTC(), ChangedBy: actor.Login}
	b, err := json.Marshal(after)
	if err != nil {
		return PublicRepositories{}, false, err
	}
	action := ActionPublicOff
	if enabled {
		action = ActionPublicOn
	}
	target := audit.Target{Type: "settings", ID: "public_repositories"}
	err = s.db.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.SetSetting(keyPublic, string(b), actor.Login); err != nil {
			return err
		}
		return audit.RecordTx(tx, actor, action, target, map[string]any{
			"enabled": strconv.FormatBool(before.Enabled) + " -> " + strconv.FormatBool(enabled),
		})
	})
	if err != nil {
		return PublicRepositories{}, false, err
	}
	return after, true, nil
}
