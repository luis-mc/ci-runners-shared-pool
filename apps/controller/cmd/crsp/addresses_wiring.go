package main

import (
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
	"os"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
)

// envAddresses reads the external addresses of this process. The controller
// and the agent validate them at start (config.Load, agentRun), so an error
// here can only come from a variable changed after start; the defaults are the
// safe answer then.
func envAddresses() addresses.Set {
	a, err := addresses.Load(os.Getenv)
	if err != nil {
		return addresses.Defaults()
	}
	return a
}

// realDeps is registry.RealDeps with the external addresses of this process.
func realDeps(logLine func(step, msg string), redact func(string) string, secret ...func(string)) sys.Deps {
	d := registry.RealDeps(logLine, redact, secret...)
	d.Addresses = envAddresses()
	return d
}
