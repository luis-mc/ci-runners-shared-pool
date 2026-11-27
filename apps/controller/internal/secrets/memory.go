package secrets

import (
	"context"
	"sync"
)

// Memory is an in-process Store for tests and for the fake of other packages.
type Memory struct {
	mu   sync.Mutex
	data map[Key][]byte
	// Down makes every call fail with ErrUnavailable, to test fail-closed paths.
	Down bool
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory { return &Memory{data: map[Key][]byte{}} }

// Get implements Store.
func (m *Memory) Get(_ context.Context, key Key) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Down {
		return nil, ErrUnavailable
	}
	v, ok := m.data[key]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), v...), nil
}

// Put implements Store.
func (m *Memory) Put(_ context.Context, key Key, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Down {
		return ErrUnavailable
	}
	m.data[key] = append([]byte(nil), value...)
	return nil
}

// Delete implements Store.
func (m *Memory) Delete(_ context.Context, key Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Down {
		return ErrUnavailable
	}
	delete(m.data, key)
	return nil
}

// Backend implements Store.
func (m *Memory) Backend() string { return "memory" }

// Keys lists the stored keys (never values), for tests.
func (m *Memory) Keys() []Key {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Key, 0, len(m.data))
	for k := range m.data {
		out = append(out, k)
	}
	return out
}

var _ Store = (*Memory)(nil)
