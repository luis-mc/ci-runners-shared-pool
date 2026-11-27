package lancache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Namespaces of the store.
const (
	NSTools    = "tools"    // <tool>/<version>/<file> (R5.14)
	NSPackages = "packages" // npm, PyPI, crates, apt proxy entries
	NSActions  = "actions"  // Actions cache entries (R5.1)
)

// Errors of the store.
var (
	ErrMiss           = errors.New("no such cache entry")
	ErrDigestMismatch = errors.New("the bytes do not match the digest")
	ErrTooLarge       = errors.New("the entry is larger than the cache cap")
	ErrBadName        = errors.New("bad cache name")
)

// Entry is one index row.
type Entry struct {
	Digest string    `json:"digest"`
	Size   int64     `json:"size"`
	Used   time.Time `json:"used"`
}

// Store keeps blobs by digest and an index name -> digest. It is safe for
// concurrent use.
type Store struct {
	dir string
	cap int64
	now func() time.Time

	mu    sync.Mutex
	index map[string]Entry
}

// Open makes or loads a store in dir with a cap in bytes (cap <= 0 is refused).
func Open(dir string, capBytes int64, now func() time.Time) (*Store, error) {
	if capBytes <= 0 {
		return nil, errors.New("the cache cap must be above zero")
	}
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o700); err != nil {
		return nil, fmt.Errorf("make cache folder: %w", err)
	}
	s := &Store{dir: dir, cap: capBytes, now: now, index: map[string]Entry{}}
	b, err := os.ReadFile(filepath.Join(dir, "index.json"))
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &s.index); err != nil {
			return nil, fmt.Errorf("read cache index: %w", err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("read cache index: %w", err)
	}
	return s, nil
}

// Name builds the index name and refuses anything that could leave a scope.
func Name(ns, scope, name string) (string, error) {
	switch ns {
	case NSTools, NSPackages, NSActions:
	default:
		return "", fmt.Errorf("%w: namespace %q", ErrBadName, ns)
	}
	if scope == "" || strings.ContainsAny(scope, "/\\\x00") || scope == "." || scope == ".." {
		return "", fmt.Errorf("%w: scope", ErrBadName)
	}
	if name == "" || strings.ContainsRune(name, 0) || strings.Contains(name, "\\") {
		return "", fmt.Errorf("%w: name", ErrBadName)
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("%w: name", ErrBadName)
		}
	}
	return ns + "/" + scope + "/" + name, nil
}

func (s *Store) blob(digest string) string { return filepath.Join(s.dir, "blobs", digest) }

func validDigest(d string) bool {
	if len(d) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(d)
	return err == nil && d == strings.ToLower(d)
}

// Put stores the bytes of r under name. A non-empty want must equal the
// SHA-256 of the bytes, else nothing is stored (ErrDigestMismatch). Older
// entries are dropped, least recently used first, until the store fits the cap.
func (s *Store) Put(name, want string, r io.Reader) (Entry, error) {
	if want != "" && !validDigest(want) {
		return Entry{}, ErrDigestMismatch
	}
	tmp, err := os.CreateTemp(filepath.Join(s.dir, "blobs"), "tmp-*")
	if err != nil {
		return Entry{}, fmt.Errorf("make temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	h := sha256.New()
	// One byte past the cap tells an oversize body without storing it all.
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, s.cap+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Entry{}, fmt.Errorf("write cache entry: %w", err)
	}
	if n > s.cap {
		return Entry{}, ErrTooLarge
	}
	got := hex.EncodeToString(h.Sum(nil))
	if want != "" && want != got {
		return Entry{}, ErrDigestMismatch
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.blob(got)); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return Entry{}, fmt.Errorf("check blob: %w", err)
		}
		if err := os.Rename(tmp.Name(), s.blob(got)); err != nil {
			return Entry{}, fmt.Errorf("place blob: %w", err)
		}
	}
	e := Entry{Digest: got, Size: n, Used: s.now()}
	s.index[name] = e
	s.evictLocked(name)
	return e, s.saveLocked()
}

// Get opens the entry. The stored bytes are hashed first: a blob that no
// longer matches its digest is deleted and reported as a miss (R6.3).
func (s *Store) Get(name string) (*os.File, Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.index[name]
	if !ok {
		return nil, Entry{}, ErrMiss
	}
	f, err := os.Open(s.blob(e.Digest))
	if err == nil {
		h := sha256.New()
		if _, err = io.Copy(h, f); err == nil && hex.EncodeToString(h.Sum(nil)) == e.Digest {
			if _, err = f.Seek(0, io.SeekStart); err == nil {
				e.Used = s.now()
				s.index[name] = e
				return f, e, s.saveLocked()
			}
		}
		_ = f.Close()
	}
	// Missing, unreadable or changed: drop every entry of that blob.
	for n, x := range s.index {
		if x.Digest == e.Digest {
			delete(s.index, n)
		}
	}
	_ = os.Remove(s.blob(e.Digest))
	_ = s.saveLocked()
	return nil, Entry{}, ErrMiss
}

// Lookup is Get without opening: the entry row, or ErrMiss.
func (s *Store) Lookup(name string) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.index[name]
	if !ok {
		return Entry{}, ErrMiss
	}
	return e, nil
}

// PrefixNewest returns the newest entry whose name starts with prefix.
func (s *Store) PrefixNewest(prefix string) (string, Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var bn string
	var be Entry
	for n, e := range s.index {
		if strings.HasPrefix(n, prefix) && (bn == "" || e.Used.After(be.Used)) {
			bn, be = n, e
		}
	}
	return bn, be, bn != ""
}

// Size is the bytes the blobs hold (each blob once).
func (s *Store) Size() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sizeLocked()
}

func (s *Store) sizeLocked() int64 {
	seen := map[string]bool{}
	var total int64
	for _, e := range s.index {
		if !seen[e.Digest] {
			seen[e.Digest] = true
			total += e.Size
		}
	}
	return total
}

// evictLocked drops least recently used entries, never keep, until under cap.
func (s *Store) evictLocked(keep string) {
	for s.sizeLocked() > s.cap {
		names := make([]string, 0, len(s.index))
		for n := range s.index {
			if n != keep {
				names = append(names, n)
			}
		}
		if len(names) == 0 {
			return
		}
		sort.Slice(names, func(i, j int) bool {
			a, b := s.index[names[i]], s.index[names[j]]
			if a.Used.Equal(b.Used) {
				return names[i] < names[j]
			}
			return a.Used.Before(b.Used)
		})
		victim := s.index[names[0]]
		delete(s.index, names[0])
		still := false
		for _, e := range s.index {
			if e.Digest == victim.Digest {
				still = true
				break
			}
		}
		if !still {
			_ = os.Remove(s.blob(victim.Digest))
		}
	}
}

func (s *Store) saveLocked() error {
	b, err := json.Marshal(s.index)
	if err != nil {
		return fmt.Errorf("write cache index: %w", err)
	}
	tmp := filepath.Join(s.dir, "index.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write cache index: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, "index.json")); err != nil {
		return fmt.Errorf("write cache index: %w", err)
	}
	return nil
}
