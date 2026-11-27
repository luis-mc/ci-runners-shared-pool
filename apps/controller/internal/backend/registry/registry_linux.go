//go:build linux

package registry

import (
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/container"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/linux"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
)

// platform lists the container backend before the persistent one (decision
// 0003, runners R5.31). The container backend is built in but a machine uses
// it only when its allow list names it; the allow list is off by default
// (machines R5.9).
func platform(d sys.Deps, ids backend.IDSource) []backend.Backend {
	eng := &container.Docker{Cmd: d.Cmd, GOOS: "linux"}
	return []backend.Backend{
		container.New(d, eng, withIDs(container.DefaultConfig(), ids)),
		linux.New(d, withLinuxIDs(linux.DefaultConfig(), ids)),
	}
}

func withIDs(c container.Config, ids backend.IDSource) container.Config {
	c.IDs = ids
	return c
}

func withLinuxIDs(c linux.Config, ids backend.IDSource) linux.Config {
	c.IDs = ids
	return c
}
