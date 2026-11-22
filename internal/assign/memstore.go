package assign

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemStore is a Store in memory, for tests and for a controller that runs
// before the database is ready. It has the same compare-and-set rules as the
// database store.
type MemStore struct {
	mu     sync.Mutex
	leases map[int64]Lease
	warm   map[string]Warm
	sets   map[string]Set
}

// NewMemStore makes an empty store.
func NewMemStore() *MemStore {
	return &MemStore{leases: map[int64]Lease{}, warm: map[string]Warm{}, sets: map[string]Set{}}
}

var _ Store = (*MemStore)(nil)

// Claim implements Store.
func (m *MemStore) Claim(_ context.Context, l Lease) (Lease, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.leases[l.JobID]; ok {
		return cur, false, nil
	}
	m.leases[l.JobID] = l
	return l, true, nil
}

// Get implements Store.
func (m *MemStore) Get(_ context.Context, id int64) (Lease, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.leases[id]
	return l, ok, nil
}

// Save implements Store.
func (m *MemStore) Save(_ context.Context, l Lease) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.leases[l.JobID] = l
	return nil
}

// Begin implements Store.
func (m *MemStore) Begin(_ context.Context, id int64, machineID, runnerName string, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.leases[id]
	if !ok || l.State != Waiting {
		return false, nil
	}
	l.State, l.MachineID, l.RunnerName, l.StartedAt, l.UpdatedAt = Starting, machineID, runnerName, now, now
	m.leases[id] = l
	return true, nil
}

// List implements Store.
func (m *MemStore) List(_ context.Context, states ...State) ([]Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Lease
	for _, l := range m.leases {
		if len(states) == 0 {
			out = append(out, l)
			continue
		}
		for _, s := range states {
			if l.State == s {
				out = append(out, l)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].QueuedAt.Equal(out[j].QueuedAt) {
			return out[i].QueuedAt.Before(out[j].QueuedAt)
		}
		return out[i].JobID < out[j].JobID
	})
	return out, nil
}

// ByRunner implements Store.
func (m *MemStore) ByRunner(_ context.Context, name string) (Lease, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if name == "" {
		return Lease{}, false, nil
	}
	for _, l := range m.leases {
		if l.RunnerName == name && l.State.Open() {
			return l, true, nil
		}
	}
	return Lease{}, false, nil
}

// ListWarm implements Store.
func (m *MemStore) ListWarm(_ context.Context) ([]Warm, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Warm
	for _, w := range m.warm {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt.Before(out[j].StartedAt) || (out[i].StartedAt.Equal(out[j].StartedAt) && out[i].ID < out[j].ID)
	})
	return out, nil
}

// PutWarm implements Store.
func (m *MemStore) PutWarm(_ context.Context, w Warm) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.warm[w.ID] = w
	return nil
}

// DeleteWarm implements Store.
func (m *MemStore) DeleteWarm(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.warm, id)
	return nil
}

// TouchSet implements Store.
func (m *MemStore) TouchSet(_ context.Context, s Set) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sets[s.Key] = s
	return nil
}

// ListSets implements Store.
func (m *MemStore) ListSets(_ context.Context) ([]Set, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Set
	for _, s := range m.sets {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}
