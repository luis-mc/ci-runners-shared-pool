package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// sweepEvery is how often the controller looks for stale registrations.
const sweepEvery = time.Hour

// startSweep runs the automatic sweep of offline runner registrations that
// crsp made (github-integration R5.33). days is `runner_sweep_days`; 0 turns
// the sweep off and no goroutine starts. It returns when ctx ends.
func startSweep(ctx context.Context, logger *slog.Logger, svc *runners.Service, days int) {
	if days <= 0 {
		logger.Info("runner sweep is off (runner_sweep_days = 0)")
		return
	}
	grace := time.Duration(days) * 24 * time.Hour
	go func() {
		t := time.NewTimer(2 * time.Minute) // let the agents report in first
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			res, err := svc.SweepOffline(ctx, grace, "sweep")
			for _, n := range res.Deleted {
				logger.Info("swept an offline runner registration", "runner", n, "older_than_days", days)
			}
			for _, n := range res.LeftBusy {
				logger.Info("sweep left a busy runner", "runner", n)
			}
			if err != nil && ctx.Err() == nil {
				logger.Warn("runner sweep", "err", err)
			}
			t.Reset(sweepEvery)
		}
	}()
}

// recoverEvery is how often the controller looks for runners whose GitHub
// registration was deleted and for set-ups that never finished.
const recoverEvery = time.Minute

// creatingAfter is how long a runner may stay creating with no plan running
// before it is settled.
const creatingAfter = 45 * time.Minute

// startRecovery registers again, once, the runners whose registration GitHub
// deleted and settles runners stuck in creating (runners R5.140, R5.141). It
// returns when ctx ends.
func startRecovery(ctx context.Context, logger *slog.Logger, svc *runners.Service) {
	go func() {
		t := time.NewTimer(45 * time.Second) // let the agents report in first
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			res, err := svc.RecoverLostRegistrations(ctx, "recovery")
			for _, n := range res.Registered {
				logger.Info("registration lost, registered again", "runner", n)
			}
			for _, n := range res.Failed {
				logger.Warn("registration lost, could not register again", "runner", n)
			}
			if err != nil && ctx.Err() == nil {
				logger.Warn("runner recovery", "err", err)
			}
			if settled, err := svc.SettleStuckCreating(ctx, creatingAfter, "recovery"); err != nil && ctx.Err() == nil {
				logger.Warn("settle creating runners", "err", err)
			} else {
				for _, n := range settled {
					logger.Info("a runner stuck in creating was settled", "runner", n)
				}
			}
			t.Reset(recoverEvery)
		}
	}()
}
