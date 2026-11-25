package settings

import (
	"context"
	"encoding/json"

	"github.com/luis-mc/ci-runners-shared-pool/internal/mirror"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

// keyMirrors is the settings row of the cache switches (caches R5.66).
const keyMirrors = "cache_mirrors"

// GetMirrorSettings returns the stored switches; none stored is all off.
func (s *Service) GetMirrorSettings(ctx context.Context) (mirror.Settings, error) {
	raw, ok, err := s.raw(ctx, keyMirrors)
	if err != nil || !ok {
		return mirror.Settings{}, err
	}
	var v mirror.Settings
	if json.Unmarshal([]byte(raw), &v) != nil {
		return mirror.Settings{}, nil
	}
	return v, nil
}

// SetMirrorSettings stores the switches.
func (s *Service) SetMirrorSettings(ctx context.Context, v mirror.Settings) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.db.WithTx(ctx, func(tx *store.Tx) error {
		return tx.SetSetting(keyMirrors, string(b), "system")
	})
}

var _ mirror.SettingsStore = (*Service)(nil)
