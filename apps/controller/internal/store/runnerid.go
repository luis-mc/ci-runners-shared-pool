package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnerid"
)

// runnerIDPrefix starts the setting key of one runner's uid/gid. The allocation
// lives in the settings table so it survives a restart and a re-created runner
// (runners R5.72).
const runnerIDPrefix = "runner_id."

// RunnerIDs is the runnerid.Table of the store.
type RunnerIDs struct{ S *Store }

var _ runnerid.Table = RunnerIDs{}

// Get implements runnerid.Table.
func (r RunnerIDs) Get(ctx context.Context, name string) (int, bool, error) {
	all, err := r.All(ctx)
	if err != nil {
		return 0, false, err
	}
	id, ok := all[name]
	return id, ok, nil
}

// All implements runnerid.Table.
func (r RunnerIDs) All(ctx context.Context) (map[string]int, error) {
	list, err := r.S.ListSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, s := range list {
		name, ok := strings.CutPrefix(s.Key, runnerIDPrefix)
		if !ok {
			continue
		}
		id, err := strconv.Atoi(strings.TrimSpace(s.Value))
		if err != nil {
			return nil, fmt.Errorf("setting %s: %w", s.Key, err)
		}
		out[name] = id
	}
	return out, nil
}

// Put implements runnerid.Table.
func (r RunnerIDs) Put(ctx context.Context, name string, id int) error {
	v, err := json.Marshal(id)
	if err != nil {
		return err
	}
	return r.S.WithTx(ctx, func(tx *Tx) error {
		return tx.SetSetting(runnerIDPrefix+name, string(v), "runnerid")
	})
}
