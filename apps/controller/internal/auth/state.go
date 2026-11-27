package auth

import (
	"sync"
	"time"
)

// StateTTL is how long a sign-in or manifest state lives (R5.2, R5.8).
const StateTTL = 10 * time.Minute

type stateEntry struct {
	kind    string
	data    map[string]string
	expires time.Time
}

// states holds single-use, short-lived values in memory. A restart voids them,
// which is safe: the person starts again.
type states struct {
	mu  sync.Mutex
	m   map[string]stateEntry
	now func() time.Time
}

func newStates(now func() time.Time) *states { return &states{m: map[string]stateEntry{}, now: now} }

// put stores data under a fresh random key and returns the key.
func (s *states) put(kind string, ttl time.Duration, data map[string]string) string {
	key := RandomToken(24)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gc()
	s.m[key] = stateEntry{kind: kind, data: data, expires: s.now().Add(ttl)}
	return key
}

// take returns the data once. A wrong kind, an old or a used key fails.
func (s *states) take(kind, key string) (map[string]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[key]
	if !ok {
		return nil, false
	}
	delete(s.m, key)
	if e.kind != kind || !s.now().Before(e.expires) {
		return nil, false
	}
	return e.data, true
}

// peek reads without using up the key.
func (s *states) peek(kind, key string) (map[string]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[key]
	if !ok || e.kind != kind || !s.now().Before(e.expires) {
		return nil, false
	}
	return e.data, true
}

func (s *states) gc() {
	now := s.now()
	for k, e := range s.m {
		if !now.Before(e.expires) {
			delete(s.m, k)
		}
	}
}
