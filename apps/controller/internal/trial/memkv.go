package trial

import (
	"context"
	"sync"
)

// MemKV is a KV in memory, for tests and for a controller without a database.
type MemKV struct {
	mu sync.Mutex
	m  map[string]string
}

// Get implements KV.
func (k *MemKV) Get(_ context.Context, key string) (string, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[key]
	return v, ok, nil
}

// Set implements KV.
func (k *MemKV) Set(_ context.Context, key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil {
		k.m = map[string]string{}
	}
	k.m[key] = value
	return nil
}
