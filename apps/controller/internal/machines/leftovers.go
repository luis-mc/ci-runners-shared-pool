package machines

import (
	"context"
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/leftovers"
)

// Runner leftovers (doctor R5.48, OPS33): services, folders, accounts and
// downloads on a machine that no runner of this controller owns. The scan reads
// only. The removal runs only on items the last scan of that machine showed, and
// the agent checks them again against a fresh scan and the runners this
// controller owns right now.

// MaxLeftoverRefs is how many items one removal may name.
const MaxLeftoverRefs = 200

// OwnedRunnersFunc lists the runners this controller owns on a machine: every
// kind, in any state except deleted.
type OwnedRunnersFunc func(ctx context.Context, machineID string) ([]leftovers.Owned, error)

type leftoverState struct {
	local, remote leftovers.Source
	owned         OwnedRunnersFunc
	last          map[string]leftovers.Scan
}

// SetLeftoverSources sets who scans and removes: local for the controller's own
// machine, remote for every other. Either may be nil. Call it before the API serves.
func (s *Service) SetLeftoverSources(local, remote leftovers.Source) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lo.local, s.lo.remote = local, remote
}

// SetOwnedRunners sets how the runners of a machine are read (the runners store).
func (s *Service) SetOwnedRunners(f OwnedRunnersFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lo.owned = f
}

// leftoverSetup returns the source of the machine and the owned runners read now.
func (s *Service) leftoverSetup(ctx context.Context, id string) (leftovers.Source, []leftovers.Owned, error) {
	m, err := s.o.Repo.GetMachine(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if !m.RemovedAt.IsZero() {
		return nil, nil, ErrNotFound
	}
	if !m.Online {
		return nil, nil, fmt.Errorf("%w: %s", ErrOffline, m.Name)
	}
	s.mu.Lock()
	src, owned := s.lo.remote, s.lo.owned
	if m.IsController {
		src = s.lo.local
	}
	s.mu.Unlock()
	if src == nil {
		return nil, nil, fmt.Errorf("%w: looking for runner leftovers is not available on %s", ErrNotSupported, m.Name)
	}
	if owned == nil {
		// Never scan or remove without knowing what this controller owns.
		return nil, nil, fmt.Errorf("%w: the runners of this controller cannot be read", ErrNotSupported)
	}
	list, err := owned(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if list == nil {
		list = []leftovers.Owned{}
	}
	return src, list, nil
}

// RunnerLeftovers scans the machine and keeps the result as its latest scan.
// It reads only.
func (s *Service) RunnerLeftovers(ctx context.Context, id string) (leftovers.Scan, error) {
	src, owned, err := s.leftoverSetup(ctx, id)
	if err != nil {
		return leftovers.Scan{}, err
	}
	scan, err := src.Scan(ctx, id, owned)
	if err != nil {
		return leftovers.Scan{}, err
	}
	s.mu.Lock()
	if s.lo.last == nil {
		s.lo.last = map[string]leftovers.Scan{}
	}
	s.lo.last[id] = scan
	s.mu.Unlock()
	return scan, nil
}

// RemoveRunnerLeftovers removes the named items. Each must be in the latest scan
// of the machine; the agent still refuses what it finds owned, outside the crsp
// folders or gone. A refused or failed item is a result, not an error.
func (s *Service) RemoveRunnerLeftovers(ctx context.Context, actor, id string, refs []leftovers.Ref) ([]leftovers.Result, error) {
	if len(refs) == 0 {
		return nil, fmt.Errorf("%w: name at least one item to remove", ErrInvalid)
	}
	if len(refs) > MaxLeftoverRefs {
		return nil, fmt.Errorf("%w: at most %d items in one call", ErrInvalid, MaxLeftoverRefs)
	}
	src, owned, err := s.leftoverSetup(ctx, id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	known := map[string]bool{}
	for _, it := range s.lo.last[id].Items {
		known[it.ID()] = true
	}
	s.mu.Unlock()
	for _, r := range refs {
		if !known[r.ID()] {
			return nil, fmt.Errorf("%w: scan again, this item was not in the last scan: %s %s", ErrConflict, r.Kind, r.Name)
		}
	}
	_ = s.o.Repo.Audit(ctx, actor, "machine.runner_leftovers.remove", id, fmt.Sprintf("%d items", len(refs)))
	res, err := src.Remove(ctx, id, owned, refs)
	for _, r := range res {
		detail := fmt.Sprintf("kind %s, name %s, outcome %s", r.Kind, r.Name, r.Outcome)
		if r.Error != "" {
			detail += ", " + strings.TrimSpace(r.Error)
		}
		_ = s.o.Repo.Audit(ctx, actor, "machine.runner_leftovers.item", id, detail)
	}
	s.dropRemoved(id, res)
	s.notifyMachine(id)
	return res, err
}

// dropRemoved takes the items that are gone out of the latest scan.
func (s *Service) dropRemoved(id string, res []leftovers.Result) {
	gone := map[string]bool{}
	for _, r := range res {
		if r.Outcome == leftovers.Removed || r.Outcome == leftovers.AlreadyGone {
			gone[string(r.Kind)+":"+r.Name] = true
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sc, ok := s.lo.last[id]
	if !ok {
		return
	}
	keep := make([]leftovers.Item, 0, len(sc.Items))
	for _, it := range sc.Items {
		if !gone[it.ID()] {
			keep = append(keep, it)
		}
	}
	sc.Items = keep
	s.lo.last[id] = sc
}
