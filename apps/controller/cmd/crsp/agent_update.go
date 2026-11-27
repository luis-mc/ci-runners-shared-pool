package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"runtime"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update"
)

// newAgentUpdater builds the update manager of an agent-only machine. It
// follows the controller's version: the controller asks over the agent link,
// the manager downloads and verifies that release, the drain holds new jobs
// out and waits for running ones (the machine's drain timeout, machines R5.33, then it postpones), and the
// install is the per-OS apply of the controller (operations R5.39 to R5.41).
// An agent without the right to install says so instead of trying.
func newAgentUpdater(cfg agent.Config, g *agent.Gate, logger *slog.Logger) agent.Updater {
	exe, err := update.ExecutablePath()
	if err != nil {
		logger.Warn("agent update: cannot find the running binary", "err", err)
	}
	service := agent.ServiceName
	if runtime.GOOS == "darwin" {
		service = agent.LaunchdLabel
	}
	var mgr *update.Manager
	drain := &agent.UpdateDrainer{Gate: g, Poll: cfg.DrainPollEvery, OnIdle: func(ctx context.Context, k update.Kind) error { return mgr.OnIdle(ctx, k) }}
	mgr = update.New(update.Options{
		Current:  cfg.Version,
		Source:   &update.PinnedSource{},
		Attester: update.SigstoreAttester{APIBase: envAddresses().GitHubAPIURL},
		Drainer:  drain,
		Dir:      filepath.Join(cfg.DataDir, "updates"),
		Service:  service,
		Exe:      exe,
		Logger:   logger.With("component", "update"),
	})
	return mgr
}
