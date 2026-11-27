//go:build windows

package registry

import (
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/container"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/windows"
)

// platform lists the Windows persistent backend and the container backend. The
// container backend runs Linux containers through the Windows `docker` command
// first (Docker Desktop on WSL2), `wsl docker` only when that cannot run
// (runners R5.27, R5.47). A machine uses it only when its allow list names it
// (machines R5.9).
func platform(d sys.Deps, ids backend.IDSource) []backend.Backend {
	cfg := container.DefaultConfig()
	cfg.IDs = ids
	return []backend.Backend{
		windows.New(d, windows.DefaultConfig()),
		container.New(d, &container.Docker{Cmd: d.Cmd, GOOS: "windows"}, cfg),
	}
}
