package settings

import (
	"context"
	"encoding/json"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// keyListen is the settings row of the controller's listen address
// (operations R5.90): the address the owner chose in the console.
const keyListen = "listen_address"

// ActionListenSet is the audit action of a change of the listen address.
const ActionListenSet = "listen_address.set"

// ListenAddress is the address (host:port) the owner chose for the controller.
// CRSP_LISTEN_ADDR, when set, still wins over it.
type ListenAddress struct {
	Address   string    `json:"address"`
	ChangedAt time.Time `json:"changed_at"`
	ChangedBy string    `json:"changed_by"`
}

// GetListenAddress returns the stored choice; none stored is the zero value.
func (s *Service) GetListenAddress(ctx context.Context) (ListenAddress, error) {
	raw, ok, err := s.raw(ctx, keyListen)
	if err != nil || !ok {
		return ListenAddress{}, err
	}
	var v ListenAddress
	if json.Unmarshal([]byte(raw), &v) != nil {
		return ListenAddress{}, nil
	}
	return v, nil
}

// SetListenAddress stores the choice and writes one audit row in the same
// transaction. The caller has checked the address.
func (s *Service) SetListenAddress(ctx context.Context, actor audit.Actor, addr string) (ListenAddress, error) {
	before, err := s.GetListenAddress(ctx)
	if err != nil {
		return ListenAddress{}, err
	}
	after := ListenAddress{Address: addr, ChangedAt: time.Now().UTC(), ChangedBy: actor.Login}
	b, err := json.Marshal(after)
	if err != nil {
		return ListenAddress{}, err
	}
	target := audit.Target{Type: "settings", ID: "listen_address"}
	err = s.db.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.SetSetting(keyListen, string(b), actor.Login); err != nil {
			return err
		}
		return audit.RecordTx(tx, actor, ActionListenSet, target, map[string]any{
			"address": before.Address + " -> " + addr,
		})
	})
	if err != nil {
		return ListenAddress{}, err
	}
	return after, nil
}
