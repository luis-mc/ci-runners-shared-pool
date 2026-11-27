package settings

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// ActionImageLayersSet is the audit action of a change of the layers of an image.
const ActionImageLayersSet = "image.layers.set"

func keyImageLayers(machineID, os string) string { return "image_layers:" + machineID + ":" + os }

// GetImageLayers returns the layers chosen for the image of one machine and OS
// (runners R5.32); none stored is an empty list.
func (s *Service) GetImageLayers(ctx context.Context, machineID, os string) ([]string, error) {
	raw, ok, err := s.raw(ctx, keyImageLayers(machineID, os))
	if err != nil || !ok {
		return []string{}, err
	}
	var v []string
	if json.Unmarshal([]byte(raw), &v) != nil {
		return []string{}, nil
	}
	return v, nil
}

// SetImageLayers stores the chosen layers (sorted) and writes one audit row in
// the same transaction. The caller has checked the layer names.
func (s *Service) SetImageLayers(ctx context.Context, actor audit.Actor, machineID, os string, layers []string) ([]string, error) {
	out := append([]string{}, layers...)
	sort.Strings(out)
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	target := audit.Target{Type: "image", ID: machineID + ":" + os}
	err = s.db.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.SetSetting(keyImageLayers(machineID, os), string(b), actor.Login); err != nil {
			return err
		}
		return audit.RecordTx(tx, actor, ActionImageLayersSet, target, map[string]any{"layers": string(b)})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
