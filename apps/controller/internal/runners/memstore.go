package runners

import (
	"context"
	"sort"
	"sync"
)

// MemStore is an in-memory Store. The controller uses it until the SQLite
// adapter lands in internal/store, so runner records and plans last until the
// controller restarts (the machine service does the same).
type MemStore struct {
	mu      sync.Mutex
	runners map[string]Record
	plans   map[string]Plan
}

// NewMemStore makes an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{runners: map[string]Record{}, plans: map[string]Plan{}}
}

var _ Store = (*MemStore)(nil)

// InsertRunner implements Store.
func (m *MemStore) InsertRunner(_ context.Context, r Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runners[r.ID] = r
	return nil
}

// GetRunner implements Store.
func (m *MemStore) GetRunner(_ context.Context, id string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runners[id]
	if !ok {
		return Record{}, ErrNotFound
	}
	return r, nil
}

// ListRunners implements Store.
func (m *MemStore) ListRunners(_ context.Context, f Filter) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Record
	for _, r := range m.runners {
		if f.MachineID != "" && r.MachineID != f.MachineID {
			continue
		}
		if f.Managed != nil && r.Managed != *f.Managed {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SetRunnerState implements Store.
func (m *MemStore) SetRunnerState(_ context.Context, id string, to State, detail string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runners[id]
	if !ok {
		return ErrNotFound
	}
	r.State = to
	r.FailedReason = ""
	if to == Failed {
		r.FailedReason = detail
	}
	r.Reason = detail
	m.runners[id] = r
	return nil
}

// SetRunnerService implements Store.
func (m *MemStore) SetRunnerService(_ context.Context, id, svc, acct string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runners[id]
	if !ok {
		return ErrNotFound
	}
	r.ServiceName, r.Account = svc, acct
	m.runners[id] = r
	return nil
}

// SetRunnerRegistration implements Store.
func (m *MemStore) SetRunnerRegistration(_ context.Context, id string, githubRunnerID int64, version string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runners[id]
	if !ok {
		return ErrNotFound
	}
	r.GitHubRunnerID, r.RunnerVersion = githubRunnerID, version
	m.runners[id] = r
	return nil
}

// DeleteRunner implements Store.
func (m *MemStore) DeleteRunner(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.runners, id)
	return nil
}

// SavePlan implements Store.
func (m *MemStore) SavePlan(_ context.Context, p Plan) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p.Steps = append([]Step(nil), p.Steps...)
	m.plans[p.ID] = p
	return nil
}

// GetPlan implements Store.
func (m *MemStore) GetPlan(_ context.Context, id string) (Plan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.plans[id]
	if !ok {
		return Plan{}, ErrNotFound
	}
	p.Steps = append([]Step(nil), p.Steps...)
	return p, nil
}

// SetRunnerHygiene implements Store.
func (m *MemStore) SetRunnerHygiene(_ context.Context, id string, h Hygiene) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runners[id]
	if !ok {
		return ErrNotFound
	}
	r.KeepDependencies, r.DependencyCacheBytes, r.UncleanReason = h.KeepDependencies, h.DependencyCacheBytes, h.UncleanReason
	m.runners[id] = r
	return nil
}

// SetRunnerPool implements Store.
func (m *MemStore) SetRunnerPool(_ context.Context, id, poolRunnerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runners[id]
	if !ok {
		return ErrNotFound
	}
	r.PoolRunnerID = poolRunnerID
	m.runners[id] = r
	return nil
}
