package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agentlink"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/leftovers"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// localLeftoverSource is the runner leftovers source of the controller's own
// machine (doctor R5.48, OPS33): the scan and removal run in this process, on
// this host.
func localLeftoverSource(log *slog.Logger) leftovers.Source {
	return leftovers.NewLocalSource(log)
}

// remoteLeftoverSource is the agent link as a source; nil without a link (not an
// interface holding a nil pointer).
func remoteLeftoverSource(l *agentlink.Link) leftovers.Source {
	if l == nil {
		return nil
	}
	return l
}

// ownedRunners lists the runners this controller owns on a machine: every
// record of the store on that machine, of any kind and state, by name and
// service name. The agent never touches what is listed here.
func ownedRunners(store runners.Store) machines.OwnedRunnersFunc {
	return func(ctx context.Context, machineID string) ([]leftovers.Owned, error) {
		rs, err := store.ListRunners(ctx, runners.Filter{MachineID: machineID})
		if err != nil {
			return nil, err
		}
		out := make([]leftovers.Owned, 0, len(rs))
		for _, r := range rs {
			out = append(out, leftovers.Owned{Name: r.Name, ServiceName: r.ServiceName})
		}
		return out, nil
	}
}

var _ doctor.LeftoverFleet = doctorFleet{}

// Leftovers scans one machine for runner leftovers through the machines service,
// which keeps the scan for the removal that follows. An agent that does not
// answer is unreachable (the check skips); any other error is the reason shown.
func (f doctorFleet) Leftovers(ctx context.Context, machineID string) (leftovers.Scan, error) {
	sc, err := f.machines.RunnerLeftovers(ctx, machineID)
	switch {
	case errors.Is(err, machines.ErrOffline), errors.Is(err, agentlink.ErrNoAnswer):
		return leftovers.Scan{}, doctor.ErrUnreachable
	case err != nil:
		return leftovers.Scan{}, err
	}
	return sc, nil
}
