package main

import (
	"context"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/repos"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/trial"
)

// settingsKV keeps the trial state in the settings table as JSON, so the
// trial needs no migration of its own.
type settingsKV struct{ db *store.Store }

func (k settingsKV) Get(ctx context.Context, key string) (string, bool, error) {
	rows, err := k.db.ListSettings(ctx)
	if err != nil {
		return "", false, err
	}
	for _, r := range rows {
		if r.Key == key {
			return r.Value, true, nil
		}
	}
	return "", false, nil
}

func (k settingsKV) Set(ctx context.Context, key, value string) error {
	return k.db.WithTx(ctx, func(tx *store.Tx) error { return tx.SetSetting(key, value, "trial") })
}

// newTrial builds the trial modes (github-integration R5.66). The job hook
// (trial.Manager.OnJob) is not wired to the webhook here: that code belongs to
// RES14.
func newTrial(db *store.Store, rsvc *repos.Service, gh *github.REST, capy machineCapacity) *trial.Manager {
	tv := rsvc.Trial(gh)
	return trial.New(trial.Config{Vars: tv, Dispatcher: tv, KV: settingsKV{db: db},
		Serves: func(ctx context.Context, os string) bool {
			m, err := capy.OSLabels(ctx)
			if err != nil {
				return false
			}
			for k := range m {
				if strings.EqualFold(k, os) {
					return true
				}
			}
			return false
		}})
}
