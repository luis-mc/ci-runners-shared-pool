//go:build darwin

package registry

import (
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/macos"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
)

func platform(d sys.Deps, _ backend.IDSource) []backend.Backend {
	return []backend.Backend{macos.New(d, macos.DefaultConfig())}
}
