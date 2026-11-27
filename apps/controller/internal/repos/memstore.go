package repos

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
)

// MemStore is an in-memory Store for tests and other packages' tests.
type MemStore struct {
	mu       sync.Mutex
	installs map[int64]Installation
	repos    map[int64]Repository
	policies map[int64]Policy
	vars     map[[2]any]ManagedVariable
	findings map[int64][]Finding
	checked  map[int64]time.Time
	plans    map[string]Plan
	fallback map[string]int64
	orgPools map[int64]OrgPool

	// persist, when set, writes the whole state after a change (CRSP148): the
	// controller restarts on every upgrade and must not lose the pool state.
	persist  Persister
	saveErr  error
	saveSoon *time.Timer
}

// NewMemStore returns an empty store.
func NewMemStore() *MemStore {
	return &MemStore{installs: map[int64]Installation{}, repos: map[int64]Repository{}, policies: map[int64]Policy{},
		vars: map[[2]any]ManagedVariable{}, findings: map[int64][]Finding{}, checked: map[int64]time.Time{}, plans: map[string]Plan{}, fallback: map[string]int64{}, orgPools: map[int64]OrgPool{}}
}

var _ Store = (*MemStore)(nil)

func (m *MemStore) UpsertInstallation(_ context.Context, i Installation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.installs[i.ID] = i
	m.saveLater()
	return nil
}

func (m *MemStore) MarkInstallationRemoved(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.installs[id]
	i.Removed = true
	m.installs[id] = i
	m.saveLater()
	return nil
}

func (m *MemStore) ListInstallations(context.Context) ([]Installation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Installation
	for _, i := range m.installs {
		if !i.Removed {
			out = append(out, i)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].AccountLogin < out[b].AccountLogin })
	return out, nil
}

func (m *MemStore) UpsertRepository(_ context.Context, r Repository) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.repos[r.ID]; ok {
		r.PoolOn, r.PoolChangedAt, r.PoolChangedBy = old.PoolOn, old.PoolChangedAt, old.PoolChangedBy
	}
	m.repos[r.ID] = r
	m.saveLater()
	return nil
}

func (m *MemStore) GetRepository(_ context.Context, id int64) (Repository, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.repos[id]
	if !ok {
		return Repository{}, auth.ErrNotFound
	}
	return r, nil
}

func (m *MemStore) ListRepositories(_ context.Context, f ListFilter) ([]Repository, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Repository
	for _, r := range m.repos {
		if f.InstallationID != 0 && r.InstallationID != f.InstallationID {
			continue
		}
		if f.Pool != nil && r.PoolOn != *f.Pool {
			continue
		}
		if f.Query != "" && !strings.Contains(strings.ToLower(r.FullName), strings.ToLower(f.Query)) {
			continue
		}
		if r.FullName <= f.AfterName {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].FullName < out[b].FullName })
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func (m *MemStore) SetRepositoryPool(_ context.Context, id int64, on bool, by string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.repos[id]
	if !ok {
		return auth.ErrNotFound
	}
	r.PoolOn, r.PoolChangedBy, r.PoolChangedAt = on, by, at
	m.repos[id] = r
	return m.saveNow()
}

func (m *MemStore) GetPolicy(_ context.Context, id int64) (Policy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.policies[id]
	if !ok {
		return Policy{}, auth.ErrNotFound
	}
	return p, nil
}

func (m *MemStore) UpsertPolicy(_ context.Context, id int64, p Policy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.policies[id] = p
	return m.saveNow()
}

func (m *MemStore) ListManagedVariables(_ context.Context, id int64) ([]ManagedVariable, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ManagedVariable
	for k, v := range m.vars {
		if k[0].(int64) == id {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out, nil
}

func (m *MemStore) UpsertManagedVariable(_ context.Context, v ManagedVariable) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vars[[2]any{v.RepositoryID, v.Name}] = v
	return m.saveNow()
}

func (m *MemStore) SetManagedVariableStatus(_ context.Context, id int64, name, status string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := [2]any{id, name}
	if v, ok := m.vars[k]; ok {
		v.Status, v.CheckedAt = status, at
		m.vars[k] = v
	}
	m.saveLater()
	return nil
}

func (m *MemStore) DeleteManagedVariable(_ context.Context, id int64, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.vars, [2]any{id, name})
	return m.saveNow()
}

func (m *MemStore) ReplaceFindings(_ context.Context, id int64, fs []Finding, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.findings[id], m.checked[id] = append([]Finding(nil), fs...), at
	m.saveLater()
	return nil
}

func (m *MemStore) ListFindings(_ context.Context, id int64) ([]Finding, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Finding(nil), m.findings[id]...), m.checked[id], nil
}

func (m *MemStore) InsertPlan(_ context.Context, p Plan) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.plans[p.ID] = p
	return m.saveNow()
}

func (m *MemStore) GetPlan(_ context.Context, id string) (Plan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.plans[id]
	if !ok {
		return Plan{}, auth.ErrNotFound
	}
	return p, nil
}

func (m *MemStore) UpdatePlan(_ context.Context, p Plan) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.plans[p.ID]; !ok {
		return auth.ErrNotFound
	}
	m.plans[p.ID] = p
	return m.saveNow()
}

func fallbackKey(id int64, month string) string { return strconv.FormatInt(id, 10) + "|" + month }

func (m *MemStore) FallbackSeconds(_ context.Context, id int64, month string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fallback[fallbackKey(id, month)], nil
}

func (m *MemStore) AddFallbackSeconds(_ context.Context, id int64, month string, seconds int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fallback[fallbackKey(id, month)] += seconds
	m.saveLater()
	return nil
}

func (m *MemStore) GetOrgPool(_ context.Context, installationID int64) (OrgPool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orgPools[installationID]
	if !ok {
		return OrgPool{}, auth.ErrNotFound
	}
	return o, nil
}

func (m *MemStore) SetOrgPool(_ context.Context, o OrgPool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.orgPools[o.InstallationID] = o
	return m.saveNow()
}
