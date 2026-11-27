package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnerpin"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// pinStatus gives the doctor the last result of the daily runner-pin check.
type pinStatus struct{ m *runnerpin.Manager }

func (p pinStatus) RunnerPin() doctor.RunnerPinStatus {
	s := p.m.Status()
	return doctor.RunnerPinStatus{Known: s.Known, Pinned: s.Pinned, Latest: s.Latest, BehindDays: s.BehindDays}
}

// pinKV keeps the moved runner pins in the settings table.
type pinKV struct{ db *store.Store }

func (k pinKV) Get(ctx context.Context, key string) (string, bool, error) {
	return settingsKV(k).Get(ctx, key)
}

func (k pinKV) Set(ctx context.Context, key, value string) error {
	return k.db.WithTx(ctx, func(tx *store.Tx) error { return tx.SetSetting(key, value, "runnerpin") })
}

// wireRunnerPin puts the stored pins in force and starts the daily check that
// moves the pin to the newest runner release (operations R5.20). Persistent
// runners are replaced with drain after a move, when automatic runner updates
// are on. The same code runs on Windows, Linux and macOS.
func wireRunnerPin(ctx context.Context, db *store.Store, logger *slog.Logger, apiBase string, rsvc *runners.Service) *runnerpin.Manager {
	log := audit.New(db)
	m := &runnerpin.Manager{
		KV: pinKV{db: db},
		Fetch: func(ctx context.Context) (runnerpin.Release, error) {
			return runnerpin.Fetch(ctx, &http.Client{Timeout: 30 * time.Second}, apiBase)
		},
		Audit: func(ctx context.Context, action, detail string) {
			_ = log.Record(ctx, audit.System, action, audit.Target{Type: "runner_pin", ID: "actions/runner"}, map[string]any{"summary": detail})
		},
		Moved: func(ctx context.Context) {
			if _, err := rsvc.UpdateAll(ctx, "system", true); err != nil {
				logger.Warn("runner pin: update of the persistent runners", "err", err)
			}
		},
	}
	if err := m.Load(ctx); err != nil {
		logger.Warn("runner pin: stored pins not read", "err", err)
	}
	go m.Run(ctx, 2*time.Minute, func(err error) { logger.Warn("runner pin: daily check", "err", err) })
	return m
}
