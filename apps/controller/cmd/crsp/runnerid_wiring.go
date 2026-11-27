package main

import (
	"log/slog"
	"os"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnerid"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// runnerIDRangeEnv sets the reserved uid/gid range, "low-high" (runners R5.72).
const runnerIDRangeEnv = "CRSP_RUNNER_ID_RANGE"

// runnerIDSource builds the allocator of persistent runner ids over the store.
// A bad range is logged and the default is used, so a typo does not stop the
// agent.
func runnerIDSource(logger *slog.Logger, db *store.Store) backend.IDSource {
	if db == nil {
		return nil
	}
	r, err := runnerid.Parse(os.Getenv(runnerIDRangeEnv))
	if err != nil {
		if logger != nil {
			logger.Warn("runner id range ignored, default used", "error", err)
		}
		r = runnerid.Default()
	}
	a, err := runnerid.New(r, store.RunnerIDs{S: db}, runnerid.HostDefault())
	if err != nil {
		return nil
	}
	return a
}
