package main

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

// classFacts tells the class settings which runners exist and how big each
// machine's pool is (scheduling R5.91).
type classFacts struct {
	runners *runners.Service
	fleet   *machines.Service
}

func (f classFacts) pools(ctx context.Context) (map[string]runners.PoolRoom, []runners.PoolRoom, error) {
	views, err := f.fleet.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	byID := map[string]runners.PoolRoom{}
	var all []runners.PoolRoom
	for _, v := range views {
		p := runners.PoolRoom{Machine: v.Machine.Name, CPUs: v.Settings.PoolCPUs, MemoryGiB: int(v.Settings.PoolMemoryGiB)}
		byID[v.Machine.ID] = p
		if p.CPUs > 0 && p.MemoryGiB > 0 {
			all = append(all, p)
		}
	}
	return byID, all, nil
}

// Pools implements runners.ClassFacts.
func (f classFacts) Pools(ctx context.Context) ([]runners.PoolRoom, error) {
	_, all, err := f.pools(ctx)
	return all, err
}

// ClassRunners implements runners.ClassFacts.
func (f classFacts) ClassRunners(ctx context.Context) ([]runners.ClassRunner, error) {
	byID, _, err := f.pools(ctx)
	if err != nil {
		return nil, err
	}
	recs, err := f.runners.List(ctx, runners.Filter{})
	if err != nil {
		return nil, err
	}
	out := make([]runners.ClassRunner, 0, len(recs))
	for _, r := range recs {
		out = append(out, runners.ClassRunner{Name: r.Name, Class: runners.SizeOf(r), Pool: byID[r.MachineID]})
	}
	return out, nil
}

// newClassService is the class settings service over the store (scheduling R5.89).
func newClassService(db *store.Store, rs *runners.Service, fleet *machines.Service) *runners.ClassService {
	return &runners.ClassService{Store: db, Facts: classFacts{runners: rs, fleet: fleet}}
}
