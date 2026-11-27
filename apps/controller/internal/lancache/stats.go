package lancache

import (
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// RepoStats are the numbers of one repository's Actions entries (caches R5.10,
// R5.70).
type RepoStats struct {
	Repo        string
	Entries     int
	Size        int64
	LastHit     time.Time
	Hits        int64
	Misses      int64
	BytesServed int64
}

type counter struct {
	hits, misses, bytes int64
	lastHit             time.Time
}

// Stats counts hits, misses and bytes served per repository.
type Stats struct {
	mu sync.Mutex
	m  map[string]*counter
}

func (st *Stats) get(scope string) *counter {
	if st.m == nil {
		st.m = map[string]*counter{}
	}
	c := st.m[scope]
	if c == nil {
		c = &counter{}
		st.m[scope] = c
	}
	return c
}

// Hit records a restore that found an entry.
func (st *Stats) Hit(scope string, at time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()
	c := st.get(scope)
	c.hits++
	c.lastHit = at
}

// Miss records a restore that found nothing.
func (st *Stats) Miss(scope string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.get(scope).misses++
}

// Served records bytes sent for a download.
func (st *Stats) Served(scope string, n int64) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.get(scope).bytes += n
}

func (st *Stats) forget(scope string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.m, scope)
}

// Entries lists the stored names under a prefix with their rows.
func (s *Store) Entries(prefix string) map[string]Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]Entry{}
	for n, e := range s.index {
		if strings.HasPrefix(n, prefix) {
			out[n] = e
		}
	}
	return out
}

// RemovePrefix deletes every name under prefix and the blobs no other name
// holds; it returns the bytes freed and the entries removed (R5.70). A restore
// of a removed name is a miss afterwards (R5.33).
func (s *Store) RemovePrefix(prefix string) (int64, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var gone []Entry
	for n, e := range s.index {
		if strings.HasPrefix(n, prefix) {
			gone = append(gone, e)
			delete(s.index, n)
		}
	}
	held := map[string]bool{}
	for _, e := range s.index {
		held[e.Digest] = true
	}
	var freed int64
	seen := map[string]bool{}
	for _, e := range gone {
		if held[e.Digest] || seen[e.Digest] {
			continue
		}
		seen[e.Digest] = true
		if err := os.Remove(s.blob(e.Digest)); err == nil || os.IsNotExist(err) {
			freed += e.Size
		}
	}
	return freed, len(gone), s.saveLocked()
}

// RepoStats lists every repository that has entries or counters, by name.
func (s *Server) RepoStats() []RepoStats {
	byRepo := map[string]*RepoStats{}
	ent := func(r string) *RepoStats {
		x := byRepo[r]
		if x == nil {
			x = &RepoStats{Repo: r}
			byRepo[r] = x
		}
		return x
	}
	seen := map[string]bool{}
	for n, e := range s.store.Entries(NSActions + "/") {
		parts := strings.SplitN(n, "/", 3)
		if len(parts) < 3 {
			continue
		}
		x := ent(parts[1])
		x.Entries++
		if !seen[parts[1]+e.Digest] {
			seen[parts[1]+e.Digest] = true
			x.Size += e.Size
		}
	}
	s.stats.mu.Lock()
	for r, c := range s.stats.m {
		x := ent(r)
		x.Hits, x.Misses, x.BytesServed, x.LastHit = c.hits, c.misses, c.bytes, c.lastHit
	}
	s.stats.mu.Unlock()
	out := make([]RepoStats, 0, len(byRepo))
	for _, x := range byRepo {
		out = append(out, *x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Repo < out[j].Repo })
	return out
}

// WipeRepo removes one repository's Actions entries and its counters.
func (s *Server) WipeRepo(scope string) (int64, int, error) {
	freed, n, err := s.store.RemovePrefix(NSActions + "/" + scope + "/")
	s.stats.forget(scope)
	return freed, n, err
}

// WipeAll removes every Actions entry and every counter.
func (s *Server) WipeAll() (int64, int, error) {
	freed, n, err := s.store.RemovePrefix(NSActions + "/")
	s.stats.mu.Lock()
	s.stats.m = nil
	s.stats.mu.Unlock()
	return freed, n, err
}

// ActionsSize is the bytes the Actions entries hold.
func (s *Server) ActionsSize() int64 {
	var n int64
	for _, r := range s.RepoStats() {
		n += r.Size
	}
	return n
}

// ForgetStats drops one repository's counters.
func (s *Server) ForgetStats(scope string) { s.stats.forget(scope) }

// Stats is the counters, for the holder's own recorders and tests.
func (s *Server) Stats() *Stats { return &s.stats }
