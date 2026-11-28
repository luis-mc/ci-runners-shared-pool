// Package depstore is the controller's own copy of what a machine without
// internet needs to install (dependency-setup DEP15, D-DEP-8): the pinned
// installers, the runner packages and the agent packages. The controller, which
// has internet, fetches each file, checks its SHA-256 against the pin of this
// release, keeps it in its data folder and serves it to its machines. When the
// controller is offline too, the owner imports the file in the console; it is
// kept only when its SHA-256 matches. A file that does not match is never
// kept and never served. The machine checks the SHA-256 again from its own
// pinned list, so this store is a cache, not a source of trust.
package depstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// PathPrefix is where the :8461 agent link serves the files (the agent's pinned TLS applies).
const PathPrefix = "/dep-files/"

// Kinds of a dependency.
const (
	KindInstaller = "installer" // a pinned installer of the dependency list (Docker Desktop)
	KindRunner    = "runner"    // the GitHub Actions runner package
	KindAgent     = "agent"     // an agent package of this release
)

// States of an entry.
const (
	StateReady     = "ready"      // stored and checked
	StateMissing   = "missing"    // known and pinned, not stored yet
	StateFailed    = "failed"     // the last fetch failed or did not match
	StateNotPinned = "not_pinned" // this release pins no SHA-256 for it: it cannot be checked, so it is never stored
)

// Errors.
var (
	ErrUnknown    = errors.New("no such dependency")
	ErrNotPinned  = errors.New("this release has no SHA-256 for this dependency, so crsp cannot check a file for it")
	ErrMismatch   = errors.New("the file does not have the SHA-256 this release lists for it, so it was not kept")
	ErrNotAllowed = errors.New("this address is not an allowed download address")
	ErrNoSource   = errors.New("this dependency has no download address")
)

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._+-]{0,127}$`)

// ValidID says an id is a safe file name.
func ValidID(id string) bool { return idRe.MatchString(id) }

// Spec is one dependency the controller may keep.
type Spec struct {
	// ID is a path-safe name, unique in the list (docker-desktop-windows, runner-linux-x64).
	ID   string
	Name string
	Kind string
	OS   string
	Arch string
	// Version is shown to the user.
	Version string
	// Source is the host shown to the user.
	Source string
	// URL is where the controller fetches it; empty when only an import can supply it.
	URL string
	// SHA256 is the pinned checksum, lower-case hex; empty means not pinned.
	SHA256 string
	// Bytes is the expected size, 0 when unknown (display only).
	Bytes int64
}

// Entry is a Spec and what the store holds of it.
type Entry struct {
	Spec
	State string
	// Bytes of the stored file (0 when none).
	Stored int64
	// FetchedAt is when the file was stored.
	FetchedAt time.Time
	// Origin is "fetched" or "imported"; empty when nothing is stored.
	Origin string
	// Error is the plain-words reason of a failed fetch.
	Error string
}

// Fetcher downloads url to dst. It must not follow a redirect to a page that is not https.
type Fetcher func(ctx context.Context, url, dst string) error

// Store keeps the files.
type Store struct {
	// Dir is the folder of the files (the data folder's dependencies/).
	Dir string
	// Catalog lists what this release may keep; called on every List.
	Catalog func(ctx context.Context) []Spec
	// Fetch downloads a file; nil means the controller has no internet path (imports only).
	Fetch Fetcher
	// AllowURL says an address may be fetched; nil allows https only.
	AllowURL func(string) bool
	mu       sync.Mutex
	errs     map[string]string
	origin   map[string]string
	// verified holds the ids whose stored file matched in this process.
	verified map[string]bool
}

func (s *Store) path(id string) string { return filepath.Join(s.Dir, id) }

func (s *Store) specs(ctx context.Context) []Spec {
	if s.Catalog == nil {
		return nil
	}
	out := s.Catalog(ctx)
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Store) spec(ctx context.Context, id string) (Spec, bool) {
	for _, sp := range s.specs(ctx) {
		if sp.ID == id {
			return sp, true
		}
	}
	return Spec{}, false
}

// fileHash returns the SHA-256 and size of a file.
func fileHash(p string) (string, int64, error) {
	f, err := os.Open(p) //nolint:gosec // our data folder
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// entry builds the entry of a spec. It re-checks a stored file once per process.
func (s *Store) entry(sp Spec) Entry {
	e := Entry{Spec: sp, State: StateMissing}
	if sp.SHA256 == "" {
		e.State = StateNotPinned
		return e
	}
	p := s.path(sp.ID)
	st, err := os.Stat(p)
	if err == nil && st.Mode().IsRegular() {
		if !s.verified[sp.ID] {
			got, _, herr := fileHash(p)
			if herr != nil || !strings.EqualFold(got, sp.SHA256) {
				_ = os.Remove(p)
				s.errs[sp.ID] = "The stored file no longer matched this release and was removed."
				return s.failedOrMissing(e)
			}
			s.verified[sp.ID] = true
		}
		e.State, e.Stored, e.FetchedAt = StateReady, st.Size(), st.ModTime().UTC()
		e.Origin = s.origin[sp.ID]
		if e.Origin == "" {
			e.Origin = "fetched"
		}
		return e
	}
	return s.failedOrMissing(e)
}

func (s *Store) failedOrMissing(e Entry) Entry {
	if msg := s.errs[e.ID]; msg != "" {
		e.State, e.Error = StateFailed, msg
	}
	return e
}

func (s *Store) init() {
	if s.errs == nil {
		s.errs = map[string]string{}
		s.origin = map[string]string{}
		s.verified = map[string]bool{}
	}
}

// List returns every dependency of the catalog and its state.
func (s *Store) List(ctx context.Context) []Entry {
	specs := s.specs(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	out := make([]Entry, 0, len(specs))
	for _, sp := range specs {
		out = append(out, s.entry(sp))
	}
	return out
}

// Get returns one entry.
func (s *Store) Get(ctx context.Context, id string) (Entry, error) {
	sp, ok := s.spec(ctx, id)
	if !ok {
		return Entry{}, ErrUnknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	return s.entry(sp), nil
}

func (s *Store) allow(u string) bool {
	if s.AllowURL != nil {
		return s.AllowURL(u)
	}
	return strings.HasPrefix(strings.ToLower(u), "https://")
}

// store moves a checked part file into place.
func (s *Store) keep(id, part, origin string) error {
	if err := os.Rename(part, s.path(id)); err != nil {
		_ = os.Remove(part)
		return err
	}
	s.verified[id] = true
	s.origin[id] = origin
	delete(s.errs, id)
	return nil
}

// FetchOne downloads one dependency, checks its SHA-256 and keeps it. A file
// that does not match is deleted. The reason of a failure is kept for List.
func (s *Store) FetchOne(ctx context.Context, id string) error {
	sp, ok := s.spec(ctx, id)
	if !ok {
		return ErrUnknown
	}
	if sp.SHA256 == "" {
		return ErrNotPinned
	}
	if sp.URL == "" || s.Fetch == nil {
		return ErrNoSource
	}
	if !s.allow(sp.URL) {
		return ErrNotAllowed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	if err := os.MkdirAll(s.Dir, 0o750); err != nil {
		return err
	}
	part := s.path(id) + ".part"
	if err := s.Fetch(ctx, sp.URL, part); err != nil {
		_ = os.Remove(part)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		msg := fmt.Sprintf("The download from %s failed: %s", sp.Source, err.Error())
		s.errs[id] = msg
		return errors.New(msg)
	}
	got, _, err := fileHash(part)
	if err != nil || !strings.EqualFold(got, sp.SHA256) {
		_ = os.Remove(part)
		s.errs[id] = ErrMismatch.Error()
		return ErrMismatch
	}
	return s.keep(id, part, "fetched")
}

// FetchAll fetches every pinned entry that is not ready, one after the other,
// and returns the ids it stored and the first error.
func (s *Store) FetchAll(ctx context.Context) ([]string, error) {
	var done []string
	var first error
	for _, e := range s.List(ctx) {
		if e.State == StateReady || e.State == StateNotPinned || e.URL == "" {
			continue
		}
		if err := s.FetchOne(ctx, e.ID); err != nil {
			if first == nil {
				first = fmt.Errorf("%s: %w", e.Name, err)
			}
			continue
		}
		done = append(done, e.ID)
	}
	return done, first
}

// Import keeps a file the owner carried over (the controller is offline). The
// bytes must match the pinned SHA-256; nothing else is kept.
func (s *Store) Import(ctx context.Context, id string, r io.Reader, limit int64) error {
	sp, ok := s.spec(ctx, id)
	if !ok {
		return ErrUnknown
	}
	if sp.SHA256 == "" {
		return ErrNotPinned
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	if err := os.MkdirAll(s.Dir, 0o750); err != nil {
		return err
	}
	part := s.path(id) + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640) //nolint:gosec // our data folder
	if err != nil {
		return err
	}
	h := sha256.New()
	src := r
	if limit > 0 {
		src = io.LimitReader(r, limit+1)
	}
	n, cerr := io.Copy(io.MultiWriter(f, h), src)
	if err := f.Close(); cerr == nil {
		cerr = err
	}
	if cerr == nil && limit > 0 && n > limit {
		cerr = errors.New("the file is larger than this controller accepts")
	}
	if cerr != nil {
		_ = os.Remove(part)
		return cerr
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), sp.SHA256) {
		_ = os.Remove(part)
		return ErrMismatch
	}
	return s.keep(id, part, "imported")
}

// Open returns a stored, checked file for serving. A file that does not match
// its pin is removed and reported as missing.
func (s *Store) Open(ctx context.Context, id string) (*os.File, int64, error) {
	if !ValidID(id) {
		return nil, 0, ErrUnknown
	}
	e, err := s.Get(ctx, id)
	if err != nil {
		return nil, 0, err
	}
	if e.State != StateReady {
		return nil, 0, os.ErrNotExist
	}
	f, err := os.Open(s.path(id)) //nolint:gosec // id passed ValidID and the catalog
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	return f, st.Size(), nil
}

// Remove deletes a stored file (the owner's choice).
func (s *Store) Remove(ctx context.Context, id string) error {
	if _, ok := s.spec(ctx, id); !ok {
		return ErrUnknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	delete(s.verified, id)
	delete(s.origin, id)
	delete(s.errs, id)
	if err := os.Remove(s.path(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
