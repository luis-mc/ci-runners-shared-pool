package main

import (
	"context"
	"log/slog"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/mirror"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// startRegistryMirror starts the pull-through mirror (registry R5.63 to R5.67)
// from its saved settings. The returned handler is mounted under mirror.PathPrefix.
// It has no token guard until the registry core (REG1) passes one in, so it
// answers 401 to every request: the mirror is never served anonymously (R5.3).
// A mirror that cannot start is logged and left off; the controller runs without it.
func startRegistryMirror(ctx context.Context, logger *slog.Logger, dataDir string, db *store.Store, log audit.Recorder, guard mirror.Guard) (*mirror.Service, *mirror.Mirror) {
	m := mirror.New(mirror.Options{DataDir: dataDir, Logger: logger.With("component", "registry-mirror"), Guard: guard})
	svc := &mirror.Service{Store: db, Mirror: m, Audit: log, DataDir: dataDir}
	if err := svc.Load(ctx); err != nil {
		logger.Warn("the registry mirror did not start", "err", err)
	}
	return svc, m
}
