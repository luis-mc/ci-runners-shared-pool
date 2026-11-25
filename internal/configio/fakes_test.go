package configio

import (
	"context"
	"errors"
	"sync"

	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
)

// memSection is a section kept in memory.
type memSection struct {
	name       string
	single     bool
	add, del   bool
	mu         sync.Mutex
	items      map[string]Fields
	failWrites bool
}

func (m *memSection) Name() string    { return m.name }
func (m *memSection) Singleton() bool { return m.single }
func (m *memSection) CanAdd() bool    { return m.add }
func (m *memSection) CanRemove() bool { return m.del }

func (m *memSection) Read(context.Context) (map[string]Fields, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]Fields{}
	for k, v := range m.items {
		c := Fields{}
		for fk, fv := range v {
			c[fk] = fv
		}
		out[k] = c
	}
	return out, nil
}

func (m *memSection) Write(_ context.Context, name string, want, old Fields) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failWrites {
		return errors.New("boom")
	}
	if m.items == nil {
		m.items = map[string]Fields{}
	}
	next := Fields{}
	for k, v := range old {
		next[k] = v
	}
	for k, v := range want {
		next[k] = v
	}
	m.items[name] = next
	return nil
}

func (m *memSection) Delete(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, name)
	return nil
}

type auditRow struct {
	Action  string
	Target  audit.Target
	Details map[string]any
	Refused bool
}

type fakeAudit struct {
	mu   sync.Mutex
	rows []auditRow
}

func (f *fakeAudit) Record(_ context.Context, _ audit.Actor, action string, t audit.Target, d map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, auditRow{action, t, d, false})
	return nil
}

func (f *fakeAudit) RecordRefused(_ context.Context, _ audit.Actor, action string, t audit.Target, d map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, auditRow{action, t, d, true})
	return nil
}

// newEngine returns an engine over a singleton, a machines-like section that
// can be neither added to nor removed from, and a notifications-like section.
func newEngine() (*Engine, *memSection, *memSection, *memSection, *fakeAudit) {
	pool := &memSection{name: "pool", single: true, items: map[string]Fields{"": {"budget_memory_pct": 85, "default_job_slots": 2, "size_slots_from_machine": true}}}
	machines := &memSection{name: "machines", items: map[string]Fields{
		"build-01": {"budget_cpu_pct": 60, "allowed_backends": []any{"docker"}},
		"build-02": {"budget_cpu_pct": 50, "allowed_backends": []any{}},
	}}
	chans := &memSection{name: "notifications", add: true, del: true, items: map[string]Fields{
		"ops-mail": {"kind": "email", "to": "ops@example.com"},
	}}
	a := &fakeAudit{}
	e := &Engine{Sections: []Section{pool, machines, chans}, Audit: a, Version: "test"}
	return e, pool, machines, chans, a
}
