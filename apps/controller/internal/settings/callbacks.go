package settings

import (
	"context"
	"encoding/json"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// keyCallbacks is the settings row of the console origins the GitHub App has
// callback URLs for (OPS16). GitHub has no API to read them, so the controller
// keeps the list the App was made with and every address a dry sign-in
// confirmed.
const keyCallbacks = "github_callback_origins"

// GetCallbackOrigins returns the stored origins; none stored is nil.
func (s *Service) GetCallbackOrigins(ctx context.Context) ([]string, error) {
	raw, ok, err := s.raw(ctx, keyCallbacks)
	if err != nil || !ok {
		return nil, err
	}
	var v []string
	if json.Unmarshal([]byte(raw), &v) != nil {
		return nil, nil
	}
	return v, nil
}

// SetCallbackOrigins stores the origins.
func (s *Service) SetCallbackOrigins(ctx context.Context, origins []string) error {
	b, err := json.Marshal(origins)
	if err != nil {
		return err
	}
	return s.db.WithTx(ctx, func(tx *store.Tx) error {
		return tx.SetSetting(keyCallbacks, string(b), "system")
	})
}

// keyWebhookAddress is the settings row of the public https address that
// forwards to the controller's /webhooks/github (OPS25). Empty when webhooks
// are off.
const keyWebhookAddress = "github_webhook_address"

// GetWebhookAddress returns the stored webhook address, "" when none.
func (s *Service) GetWebhookAddress(ctx context.Context) (string, error) {
	raw, ok, err := s.raw(ctx, keyWebhookAddress)
	if err != nil || !ok {
		return "", err
	}
	return raw, nil
}

// SetWebhookAddress stores the webhook address; "" clears it.
func (s *Service) SetWebhookAddress(ctx context.Context, address string) error {
	return s.db.WithTx(ctx, func(tx *store.Tx) error {
		return tx.SetSetting(keyWebhookAddress, address, "system")
	})
}
