package repos

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// Persister keeps the repository state between runs. The database package
// implements it (CRSP148).
type Persister interface {
	LoadReposState(ctx context.Context) ([]byte, error)
	SaveReposState(ctx context.Context, body []byte) error
}

// saveDelay coalesces the many small changes of a sync into one write.
const saveDelay = 200 * time.Millisecond

// snapshot is the stored form of the MemStore.
type snapshot struct {
	Installs []Installation      `json:"installations"`
	Repos    []Repository        `json:"repositories"`
	Policies map[int64]Policy    `json:"policies"`
	Vars     []ManagedVariable   `json:"variables"`
	Findings map[int64][]Finding `json:"findings"`
	Checked  map[int64]time.Time `json:"checked"`
	Plans    map[string]Plan     `json:"plans"`
	Fallback map[string]int64    `json:"fallback"`
	OrgPools map[int64]OrgPool   `json:"org_pools"`
}

// NewPersistentStore returns a store that is read from p at once and written
// back after each change. The pool state, the policies, the variables and the
// plans survive a restart of the controller, such as an upgrade.
func NewPersistentStore(ctx context.Context, p Persister) (*MemStore, error) {
	m := NewMemStore()
	body, err := p.LoadReposState(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the saved repositories: %w", err)
	}
	if len(body) > 0 {
		if err := m.restore(body); err != nil {
			return nil, fmt.Errorf("read the saved repositories: %w", err)
		}
	}
	m.persist = p
	return m, nil
}

func (m *MemStore) restore(body []byte) error {
	var sn snapshot
	if err := json.Unmarshal(body, &sn); err != nil {
		return err
	}
	for _, i := range sn.Installs {
		m.installs[i.ID] = i
	}
	for _, r := range sn.Repos {
		m.repos[r.ID] = r
	}
	for k, v := range sn.Policies {
		m.policies[k] = v
	}
	for _, v := range sn.Vars {
		m.vars[[2]any{v.RepositoryID, v.Name}] = v
	}
	for k, v := range sn.Findings {
		m.findings[k] = v
	}
	for k, v := range sn.Checked {
		m.checked[k] = v
	}
	for k, v := range sn.Plans {
		m.plans[k] = v
	}
	for k, v := range sn.Fallback {
		m.fallback[k] = v
	}
	for k, v := range sn.OrgPools {
		m.orgPools[k] = v
	}
	return nil
}

// encode makes the stored form; the caller holds the lock. Removed
// installations are kept: a removal is a state too.
func (m *MemStore) encode() ([]byte, error) {
	sn := snapshot{Policies: m.policies, Findings: m.findings, Checked: m.checked, Plans: m.plans,
		Fallback: m.fallback, OrgPools: m.orgPools}
	for _, i := range m.installs {
		sn.Installs = append(sn.Installs, i)
	}
	for _, r := range m.repos {
		sn.Repos = append(sn.Repos, r)
	}
	for _, v := range m.vars {
		sn.Vars = append(sn.Vars, v)
	}
	// The same state gives the same bytes: a map has no order, and a save that differs only in order is a change.
	sort.Slice(sn.Installs, func(i, j int) bool { return sn.Installs[i].ID < sn.Installs[j].ID })
	sort.Slice(sn.Repos, func(i, j int) bool { return sn.Repos[i].ID < sn.Repos[j].ID })
	sort.Slice(sn.Vars, func(i, j int) bool {
		if sn.Vars[i].RepositoryID != sn.Vars[j].RepositoryID {
			return sn.Vars[i].RepositoryID < sn.Vars[j].RepositoryID
		}
		return sn.Vars[i].Name < sn.Vars[j].Name
	})
	return json.Marshal(sn)
}

// saveNow writes the state before the change returns; the caller holds the
// lock. A write that failed is returned to the caller of the change.
func (m *MemStore) saveNow() error {
	if m.persist == nil {
		return nil
	}
	if m.saveSoon != nil {
		m.saveSoon.Stop()
		m.saveSoon = nil
	}
	body, err := m.encode()
	if err != nil {
		return fmt.Errorf("save the repositories: %w", err)
	}
	if err := m.persist.SaveReposState(context.Background(), body); err != nil {
		return fmt.Errorf("save the repositories: %w", err)
	}
	return nil
}

// saveLater writes the state soon, in one write for a burst of changes; the
// caller holds the lock.
func (m *MemStore) saveLater() {
	if m.persist == nil || m.saveSoon != nil {
		return
	}
	m.saveSoon = time.AfterFunc(saveDelay, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.saveSoon = nil
		m.saveErr = m.saveNow()
	})
}

// Flush writes any change still waiting and returns the last write error.
func (m *MemStore) Flush() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.persist == nil {
		return nil
	}
	if err := m.saveNow(); err != nil {
		return err
	}
	return m.saveErr
}
