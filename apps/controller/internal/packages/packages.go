// Package packages holds the release packages the controller hands to new
// machines (machines R5.65, CRSP122). It proxies the GitHub release the
// controller was built from: SHA256SUMS first, then each package on demand,
// checked against SHA256SUMS before it is kept or served.
package packages

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/joinfile"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update"
)

// ErrUnavailable says there is no release to serve from (a development build,
// no network, or the release has no SHA256SUMS).
var ErrUnavailable = errors.New("this controller has no release packages to hand out")

// ErrNotListed says the name is not an installer package of the release.
var ErrNotListed = errors.New("no such package")

// Store serves one release's packages.
type Store struct {
	// Source finds the release; pin it to the controller's version.
	Source update.Source
	// Dir is the cache folder (the data folder's packages/).
	Dir string
	// Fetch and AllowURL are seams for tests; the defaults are the updater's.
	Fetch    func(ctx context.Context, url, dst string) error
	AllowURL func(string) bool

	mu       sync.Mutex
	release  update.Release
	sums     []byte
	verified map[string]bool
}

func (s *Store) fetch() func(context.Context, string, string) error {
	if s.Fetch != nil {
		return s.Fetch
	}
	return update.Fetch
}

func (s *Store) allow(u string) bool {
	if s.AllowURL != nil {
		return s.AllowURL(u)
	}
	return update.AllowedURL(u)
}

// load reads the release and its SHA256SUMS once. Callers hold mu.
func (s *Store) load(ctx context.Context) error {
	if s.sums != nil {
		return nil
	}
	if s.Source == nil {
		return ErrUnavailable
	}
	rel, err := s.Source.Latest(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	u, ok := rel.Assets["SHA256SUMS"]
	if !ok || !s.allow(u) {
		return fmt.Errorf("%w: the release has no SHA256SUMS", ErrUnavailable)
	}
	dir := filepath.Join(s.Dir, rel.Version)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	dst := filepath.Join(dir, "SHA256SUMS")
	if err := s.fetch()(ctx, u, dst); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	b, err := os.ReadFile(dst) //nolint:gosec // our cache folder
	if err != nil {
		return err
	}
	s.release, s.sums, s.verified = rel, b, map[string]bool{}
	return nil
}

// Sums returns the SHA256SUMS text and the release version.
func (s *Store) Sums(ctx context.Context) ([]byte, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(ctx); err != nil {
		return nil, "", err
	}
	return append([]byte(nil), s.sums...), s.release.Version, nil
}

// Open returns the package name, downloaded and checked against SHA256SUMS on
// first use. A name that SHA256SUMS does not list as an installer is refused.
func (s *Store) Open(ctx context.Context, name string) (*os.File, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(ctx); err != nil {
		return nil, 0, err
	}
	want := ""
	for _, p := range joinfile.ParseSums(s.sums) {
		if p.Name == name {
			want = p.SHA256
		}
	}
	if want == "" {
		return nil, 0, ErrNotListed
	}
	path := filepath.Join(s.Dir, s.release.Version, name)
	if !s.verified[name] {
		got, err := update.FileSHA256(path)
		if err != nil || got != want {
			u, ok := s.release.Assets[name]
			if !ok || !s.allow(u) {
				return nil, 0, ErrNotListed
			}
			if err := s.fetch()(ctx, u, path); err != nil {
				return nil, 0, fmt.Errorf("%w: %v", ErrUnavailable, err)
			}
			if got, err = update.FileSHA256(path); err != nil || got != want {
				_ = os.Remove(path)
				return nil, 0, fmt.Errorf("%w: %s does not match SHA256SUMS", ErrUnavailable, name)
			}
		}
		s.verified[name] = true
	}
	f, err := os.Open(path) //nolint:gosec // our cache folder, name checked against SHA256SUMS
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
