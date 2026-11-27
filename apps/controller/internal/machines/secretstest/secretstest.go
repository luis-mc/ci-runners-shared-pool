// Package secretstest is an in-memory secrets.Store for tests.
package secretstest

import (
	"context"
	"sync"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

// Mem is an in-memory secrets.Store.
type Mem struct {
	mu   sync.Mutex
	data map[secrets.Key][]byte
	// Down makes every call fail with ErrUnavailable.
	Down bool
}

var _ secrets.Store = (*Mem)(nil)

// New returns an empty store.
func New() *Mem { return &Mem{data: map[secrets.Key][]byte{}} }

// Get implements secrets.Store.
func (m *Mem) Get(_ context.Context, k secrets.Key) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Down {
		return nil, secrets.ErrUnavailable
	}
	v, ok := m.data[k]
	if !ok {
		return nil, secrets.ErrNotFound
	}
	return append([]byte(nil), v...), nil
}

// Put implements secrets.Store.
func (m *Mem) Put(_ context.Context, k secrets.Key, v []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Down {
		return secrets.ErrUnavailable
	}
	m.data[k] = append([]byte(nil), v...)
	return nil
}

// Delete implements secrets.Store.
func (m *Mem) Delete(_ context.Context, k secrets.Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Down {
		return secrets.ErrUnavailable
	}
	delete(m.data, k)
	return nil
}

// Backend implements secrets.Store.
func (m *Mem) Backend() string { return "memory" }

// Keys lists the stored keys (tests only).
func (m *Mem) Keys() []secrets.Key {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]secrets.Key, 0, len(m.data))
	for k := range m.data {
		out = append(out, k)
	}
	return out
}
