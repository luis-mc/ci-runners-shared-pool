package main

import (
	"context"
	"runtime"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/macvm"
)

// agentMacVMs gives the agent the macOS VM count of a Mac (machines R5.14);
// nil on any other OS.
func agentMacVMs() agent.MacVMSource {
	if runtime.GOOS != "darwin" {
		return nil
	}
	lim := &macvm.Limiter{FW: macvm.NewFramework()}
	return func(ctx context.Context) (macvm.Report, error) { return lim.Report(ctx) }
}
