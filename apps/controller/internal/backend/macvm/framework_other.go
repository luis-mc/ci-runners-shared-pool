//go:build !darwin

package macvm

import (
	"context"
	"runtime"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/vmclone"
)

// NewFramework is the stub off a Mac: it reports the real OS, so Check says
// "only on a Mac", counts no VM and starts none.
func NewFramework() Framework { return stub{} }

type stub struct{}

func (stub) Host(context.Context) (Host, error) {
	return Host{OS: runtime.GOOS, Arch: runtime.GOARCH}, nil
}
func (stub) Visible(context.Context) (int, error) { return 0, nil }
func (stub) Boot(context.Context, vmclone.BootSpec) (vmclone.VM, error) {
	return nil, ErrNoBridge
}
