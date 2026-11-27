package machines_test

import (
	"context"
	"sync/atomic"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// gatedRepo stops the first GetMachine after it read the record, so a test can
// run another write between a read and the write that follows it.
type gatedRepo struct {
	*machines.MemRepo
	gate    atomic.Bool
	reached chan struct{}
	release chan struct{}
}

func (g *gatedRepo) GetMachine(ctx context.Context, id string) (machines.Machine, error) {
	m, err := g.MemRepo.GetMachine(ctx, id)
	if g.gate.CompareAndSwap(true, false) {
		close(g.reached)
		<-g.release
	}
	return m, err
}
