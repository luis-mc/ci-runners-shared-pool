package toolstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Cloner copies a folder tree cheaply: ReFS block clone or NTFS hardlinks on Windows, APFS clonefile on
// macOS (step 5). The destination must not exist. The real ones are clone_*.go; tests use FakeCloner or the
// hardlink one on Linux.
type Cloner interface {
	Clone(ctx context.Context, src, dst string) error
}

var keyShape = regexp.MustCompile(`^[0-9a-f]{64}$`)

// CacheKey is the cache key of a job: the tool versions of the plan plus the hash of the lockfiles (step 5).
// Order and repeats of the plan do not matter; a different OS or a different tool version is a different key.
func CacheKey(goos string, p Plan, lockHash string) string {
	lines := map[string]bool{}
	for _, t := range p {
		lines[t.Tool+"="+t.Version] = true
	}
	var ls []string
	for l := range lines {
		ls = append(ls, l)
	}
	sortStrings(ls)
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "crsp-toolcache-v1\nos=%s\n%s\nlock=%s\n", goos, strings.Join(ls, "\n"), lockHash)
	return hex.EncodeToString(h.Sum(nil))
}

// Trust says whether a finished run may write the cache (step 7).
type Trust struct {
	// Repository is the repository the job ran for; HeadRepository the one its code comes from (a fork
	// differs); Ref and DefaultBranch tell whether it ran on the default branch; Green is a passed job.
	Repository, HeadRepository string
	Ref, DefaultBranch         string
	Green                      bool
}

// MaySave is true only for a green run on the default branch of the same repository. A fork, a pull request
// head and a red run never write.
func (t Trust) MaySave() bool {
	if !t.Green || t.Repository == "" || t.DefaultBranch == "" {
		return false
	}
	if !strings.EqualFold(t.Repository, t.HeadRepository) {
		return false
	}
	return t.Ref == t.DefaultBranch || t.Ref == "refs/heads/"+t.DefaultBranch
}

// Cache is the cache store of one machine: Root/<key>/, written once and then read-only.
type Cache struct {
	Root  string
	Clone Cloner
	// Snapshot copies a finished job's folder into a new entry; nil means CopyCloner. It must copy, never
	// link: the source is the job's writable home, and a link would let a leftover process of the job change
	// the saved entry.
	Snapshot Cloner
	// Seal makes a saved entry read-only; nil does not seal (tests with a fake). The OS default is sealDir.
	Seal func(dir string) error
}

// ErrBadKey is returned for a key that is not 64 lowercase hex characters.
var ErrBadKey = errors.New("toolstore: not a cache key")

func (c Cache) dir(key string) (string, error) {
	if !keyShape.MatchString(key) {
		return "", ErrBadKey
	}
	return filepath.Join(c.Root, key), nil
}

// Has says whether the key is saved.
func (c Cache) Has(key string) bool {
	d, err := c.dir(key)
	if err != nil {
		return false
	}
	_, err = os.Stat(d)
	return err == nil
}

// Restore clones the entry into dst, the job's caches folder subfolder (dst must not exist). A miss is not
// an error: it returns false. The source is never opened for writing.
func (c Cache) Restore(ctx context.Context, key, dst string) (bool, error) {
	d, err := c.dir(key)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(d); err != nil {
		return false, nil
	}
	if err := c.Clone.Clone(ctx, d, dst); err != nil {
		return false, err
	}
	return true, nil
}

// Save stores src under key when the run is trusted (Trust.MaySave) and the key is new. It never overwrites:
// an existing key is left as it is and Save reports false. The entry is cloned into a temporary folder and
// renamed into place, so a reader never sees half an entry.
func (c Cache) Save(ctx context.Context, key, src string, t Trust) (bool, error) {
	d, err := c.dir(key)
	if err != nil {
		return false, err
	}
	if !t.MaySave() {
		return false, nil
	}
	if _, err := os.Stat(d); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(c.Root, 0o755); err != nil {
		return false, err
	}
	tmp := d + ".tmp"
	_ = os.RemoveAll(tmp)
	snap := c.Snapshot
	if snap == nil {
		snap = CopyCloner{}
	}
	if err := snap.Clone(ctx, src, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return false, err
	}
	if c.Seal != nil {
		if err := c.Seal(tmp); err != nil {
			_ = os.RemoveAll(tmp)
			return false, err
		}
	}
	if err := os.Rename(tmp, d); err != nil {
		if _, serr := os.Stat(d); serr == nil { // another job saved the same key first: keep theirs
			_ = os.RemoveAll(tmp)
			return false, nil
		}
		return false, err
	}
	return true, nil
}
