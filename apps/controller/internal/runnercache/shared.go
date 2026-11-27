package runnercache

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SharedMode is the mode of the shared cache root and of every folder made in
// it on Linux and macOS: world-writable with the sticky bit, so any runner
// user can add a tool and no user can replace or delete another user's file
// (caches R5.18).
const SharedMode os.FileMode = os.ModeDir | os.ModeSticky | 0o777

// Shared is a cache folder that several users write (caches R5.14, R5.18 to
// R5.20, the cache part of what `ci_tool.py` did, decision D308). The pinned
// download and unpack logic stays in the delivery scripts; this holds only the
// shared-folder rules. Warn gets the one warning of R5.20.
type Shared struct {
	// Root is the shared folder.
	Root string
	// Private is the per-user folder used when Root cannot be written.
	Private string
	// Warn is called once, the first time Root is not writable.
	Warn func(msg string)

	once sync.Once
}

// Prepare makes the root with the shared mode. On Windows the agent sets the
// access list instead (see SharedACLPlan); Prepare then only makes the folder.
func (s *Shared) Prepare() error {
	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		return err
	}
	return applySharedMode(s.Root)
}

// Folder makes dir and every missing parent between Root and dir with the
// shared mode, whatever the umask says. dir must lie inside Root.
func (s *Shared) Folder(dir string) error {
	rel, err := s.rel(dir)
	if err != nil {
		return err
	}
	cur := s.Root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		if _, err := os.Stat(cur); err == nil {
			continue
		}
		if err := os.Mkdir(cur, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if err := applySharedMode(cur); err != nil {
			return err
		}
	}
	return nil
}

func (s *Shared) rel(p string) (string, error) {
	r, err := filepath.Rel(s.Root, p)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the shared cache %s", p, s.Root)
	}
	return r, nil
}

// Resolve returns the folder a job should use for rel: the shared folder, or
// the private per-user one when the shared folder cannot be written. The
// fallback prints one warning, ever (R5.20).
func (s *Shared) Resolve(rel string) (path string, shared bool) {
	p := filepath.Join(s.Root, rel)
	if s.Folder(filepath.Dir(p)) == nil && writable(filepath.Dir(p)) {
		return p, true
	}
	s.once.Do(func() {
		if s.Warn != nil {
			s.Warn("the shared tool cache " + s.Root + " cannot be written; using the private cache " + s.Private)
		}
	})
	return filepath.Join(s.Private, rel), false
}

// Put writes data to rel under the shared root through a temporary name and a
// rename, so a reader never sees a half file. A process that loses a rename
// race uses the winner's file and returns nil (R5.18). The file is read-only
// to others once in place.
func (s *Shared) Put(rel string, data []byte) (winner bool, err error) {
	final := filepath.Join(s.Root, rel)
	if err := s.Folder(filepath.Dir(final)); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(final), ".put-*")
	if err != nil {
		return false, err
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(name)
		return false, errors.Join(werr, cerr)
	}
	_ = os.Chmod(name, 0o644)
	if _, err := os.Stat(final); err == nil {
		// Someone else is already there: use their file.
		_ = os.Remove(name)
		return false, nil
	}
	if err := os.Rename(name, final); err != nil {
		_ = os.Remove(name)
		if _, serr := os.Stat(final); serr == nil {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// writable says whether the current user can create a file in dir.
func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name) == nil
}

// Writable says whether the current user can create and remove a file in dir.
func Writable(dir string) bool { return writable(dir) }
