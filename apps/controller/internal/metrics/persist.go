package metrics

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

const (
	settingKey = "metrics_export"
	// TokenKey is the secret-store key of the bearer token.
	TokenKey secrets.Key = "crsp/metrics/token"
	// Audit actions.
	ActionChanged   = "metrics.changed"
	ActionTokenMade = "metrics.token_made"
	targetType      = "settings"
)

// DBStore keeps the Config in the controller's settings table and writes one
// audit row per change, in the same transaction.
type DBStore struct{ DB *store.Store }

// Load returns the stored Config, or Default when none was stored.
func (d DBStore) Load(ctx context.Context) (Config, error) {
	rows, err := d.DB.ListSettings(ctx)
	if err != nil {
		return Config{}, err
	}
	c := Default()
	for _, r := range rows {
		if r.Key == settingKey {
			if json.Unmarshal([]byte(r.Value), &c) != nil {
				return Default(), nil
			}
		}
	}
	return c, nil
}

// Save implements Store. The audit row names what changed and never the token.
func (d DBStore) Save(ctx context.Context, actor audit.Actor, c Config) error {
	before, err := d.Load(ctx)
	if err != nil {
		return err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return d.DB.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.SetSetting(settingKey, string(b), actor.Login); err != nil {
			return err
		}
		return audit.RecordTx(tx, actor, ActionChanged, audit.Target{Type: targetType, ID: settingKey}, map[string]any{
			"enabled":      flip(before.Enabled, c.Enabled),
			"expose":       flip(before.Expose, c.Expose),
			"page_auth":    flip(before.RequireToken, c.RequireToken),
			"push_enabled": flip(before.PushEnabled, c.PushEnabled),
		})
	})
}

func flip(a, b bool) string {
	f := func(v bool) string {
		if v {
			return "on"
		}
		return "off"
	}
	return f(a) + " -> " + f(b)
}

// SecretToken keeps the token in the OS secret store.
type SecretToken struct{ Secrets secrets.Store }

// Get implements TokenStore; no token is "".
func (t SecretToken) Get(ctx context.Context) (string, error) {
	b, err := t.Secrets.Get(ctx, TokenKey)
	if errors.Is(err, secrets.ErrNotFound) {
		return "", nil
	}
	return string(b), err
}

// Put implements TokenStore.
func (t SecretToken) Put(ctx context.Context, token string) error {
	return t.Secrets.Put(ctx, TokenKey, []byte(token))
}
