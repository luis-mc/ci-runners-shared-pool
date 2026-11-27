package toolcache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"
)

// Folders the store keeps inside the root. Both start with a dot, so no tool
// name can collide with them.
const (
	stageDir = ".stage"
	trashDir = ".trash"
)

// Defaults.
const (
	DefaultCapBytes = 20 << 30
	DefaultMinAge   = 30 * time.Minute
	DefaultStageTTL = 24 * time.Hour
)

// Errors.
var (
	ErrBadName = errors.New("a tool and a version are plain folder names")
	ErrNoBuild = errors.New("the build left no entry")
)

// Store is the shared tool cache of one machine.
type Store struct {
	Root     string
	CapBytes int64         // 0 means DefaultCapBytes
	MinAge   time.Duration // an entry used this recently is never evicted; 0 means DefaultMinAge
	StageTTL time.Duration // a stage folder older than this is an orphan; 0 means DefaultStageTTL
	FS       FS            // nil means OSFS
	Platform Platform      // nil means Unix
	Now      func() time.Time
	// Token makes a unique name for a stage or trash folder; nil uses random bytes.
	Token func() string
}

func (s *Store) fs() FS {
	if s.FS == nil {
		return OSFS{}
	}
	return s.FS
}

func (s *Store) platform() Platform {
	if s.Platform == nil {
		return Unix{}
	}
	return s.Platform
}

func (s *Store) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

func (s *Store) cap() int64 {
	if s.CapBytes <= 0 {
		return DefaultCapBytes
	}
	return s.CapBytes
}

func (s *Store) minAge() time.Duration {
	if s.MinAge <= 0 {
		return DefaultMinAge
	}
	return s.MinAge
}

func (s *Store) stageTTL() time.Duration {
	if s.StageTTL <= 0 {
		return DefaultStageTTL
	}
	return s.StageTTL
}

func (s *Store) token() string {
	if s.Token != nil {
		return s.Token()
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Prepare makes the root and sets who may write it (R5.18, R5.19). Safe to repeat.
func (s *Store) Prepare(ctx context.Context) error {
	if s.Root == "" {
		return errors.New("the tool cache has no folder")
	}
	if err := s.platform().Secure(ctx, s.fs(), s.Root); err != nil {
		return err
	}
	for _, d := range []string{stageDir, trashDir} {
		if err := s.folder(filepath.Join(s.Root, d)); err != nil {
			return err
		}
	}
	return nil
}

// folder makes one folder inside the root with the shared mode.
func (s *Store) folder(dir string) error {
	if err := s.fs().Mkdir(dir, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return s.platform().Folder(s.fs(), dir)
}

// ValidName says whether a tool or version is one plain, visible folder name.
func ValidName(n string) bool {
	return n != "" && n != "." && n != ".." && !strings.HasPrefix(n, ".") &&
		!strings.ContainsAny(n, `/\:*?"<>|`+"\x00")
}

// Path is the folder of an entry: `<root>/<tool>/<version>`.
func (s *Store) Path(tool, version string) (string, error) {
	if !ValidName(tool) || !ValidName(version) {
		return "", fmt.Errorf("%w: %q %q", ErrBadName, tool, version)
	}
	return filepath.Join(s.Root, tool, version), nil
}

// Lookup returns the entry folder when it is there, and marks it used now so
// eviction keeps it. Reads are not tracked by the volume (noatime), so the job
// that finds a tool is the one that touches it.
func (s *Store) Lookup(tool, version string) (string, bool) {
	p, err := s.Path(tool, version)
	if err != nil {
		return "", false
	}
	fi, err := s.fs().Stat(p)
	if err != nil || !fi.IsDir() {
		return "", false
	}
	n := s.now()
	_ = s.fs().Chtimes(p, n, n)
	return p, true
}

// Install puts an entry in place. build fills the folder it is given; the
// folder is on the same volume as the root, and nobody sees it until the one
// rename at the end. When the entry is already there, or another process wins
// the rename, the existing entry is used and winner is false. A failed build
// leaves nothing behind.
func (s *Store) Install(tool, version string, build func(dir string) error) (path string, winner bool, err error) {
	final, err := s.Path(tool, version)
	if err != nil {
		return "", false, err
	}
	if p, ok := s.Lookup(tool, version); ok {
		return p, false, nil
	}
	f := s.fs()
	if err := f.MkdirAll(filepath.Join(s.Root, stageDir), 0o755); err != nil {
		return "", false, err
	}
	stage := filepath.Join(s.Root, stageDir, s.token())
	if err := f.Mkdir(stage, 0o755); err != nil {
		return "", false, err
	}
	_ = s.platform().Folder(f, stage)
	defer func() { _ = f.RemoveAll(stage) }() // gone after a rename; cleans a failure
	if err := build(stage); err != nil {
		return "", false, err
	}
	if fi, serr := f.Stat(stage); serr != nil || !fi.IsDir() {
		return "", false, ErrNoBuild
	}
	n := s.now()
	_ = f.Chtimes(stage, n, n)
	parent := filepath.Dir(final)
	if err := s.parent(parent); err != nil {
		return "", false, err
	}
	// Renaming a folder onto a non-empty folder fails on every system, so a
	// loser never replaces the winner's entry.
	if err := f.Rename(stage, final); err != nil {
		if fi, serr := f.Stat(final); serr == nil && fi.IsDir() {
			return final, false, nil
		}
		return "", false, err
	}
	return final, true, nil
}

// parent makes the tool folder with the shared mode.
func (s *Store) parent(dir string) error {
	if err := s.fs().Mkdir(dir, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return s.platform().Folder(s.fs(), dir)
}

// Entry is one `<tool>/<version>` folder.
type Entry struct {
	Tool, Version, Path string
	Size                int64
	Used                time.Time
}

// Entries lists every entry with its size. A missing root is an empty cache.
func (s *Store) Entries() ([]Entry, error) {
	tools, err := s.fs().ReadDir(s.Root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []Entry
	for _, t := range tools {
		if !t.IsDir() || !ValidName(t.Name()) {
			continue
		}
		vers, err := s.fs().ReadDir(filepath.Join(s.Root, t.Name()))
		if err != nil {
			return nil, err
		}
		for _, v := range vers {
			if !v.IsDir() || !ValidName(v.Name()) {
				continue
			}
			p := filepath.Join(s.Root, t.Name(), v.Name())
			fi, err := s.fs().Stat(p)
			if err != nil {
				continue // evicted while we looked
			}
			size, err := s.size(p)
			if err != nil {
				return nil, err
			}
			out = append(out, Entry{Tool: t.Name(), Version: v.Name(), Path: p, Size: size, Used: fi.ModTime()})
		}
	}
	return out, nil
}

func (s *Store) size(p string) (int64, error) {
	fi, err := s.fs().Stat(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	if !fi.IsDir() {
		return fi.Size(), nil
	}
	des, err := s.fs().ReadDir(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	var n int64
	for _, de := range des {
		c, err := s.size(filepath.Join(p, de.Name()))
		if err != nil {
			return 0, err
		}
		n += c
	}
	return n, nil
}
