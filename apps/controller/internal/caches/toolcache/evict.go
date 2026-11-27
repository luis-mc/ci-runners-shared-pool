package toolcache

import (
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
)

// EvictResult is what one pass did.
type EvictResult struct {
	Before, After int64
	Cap           int64
	Removed       []Entry
	// Protected counts entries over the cap's need that were kept because a job
	// used them within MinAge.
	Protected int
	// Orphans counts stage and trash folders removed.
	Orphans int
}

// Evict brings the cache under its cap, least recently used first, and removes
// orphan stage and trash folders (R5.62). An entry used within MinAge stays,
// so the cache may stay over its cap while jobs run. An entry is renamed into
// the trash folder first, then deleted: a reader never sees half an entry, and
// a job that opened a file keeps it on systems that allow that.
func (s *Store) Evict() (EvictResult, error) {
	res := EvictResult{Cap: s.cap()}
	res.Orphans = s.sweepOrphans()
	es, err := s.Entries()
	if err != nil {
		return res, err
	}
	for _, e := range es {
		res.Before += e.Size
	}
	res.After = res.Before
	if res.Before <= res.Cap {
		return res, nil
	}
	sort.Slice(es, func(i, j int) bool {
		if !es[i].Used.Equal(es[j].Used) {
			return es[i].Used.Before(es[j].Used)
		}
		return es[i].Path < es[j].Path
	})
	cutoff := s.now().Add(-s.minAge())
	for _, e := range es {
		if res.After <= res.Cap {
			break
		}
		if e.Used.After(cutoff) {
			res.Protected++
			continue
		}
		if err := s.remove(e.Path); err != nil {
			return res, err
		}
		res.After -= e.Size
		res.Removed = append(res.Removed, e)
		s.dropEmpty(filepath.Join(s.Root, e.Tool))
	}
	return res, nil
}

// remove renames the entry into the trash, then deletes it there.
func (s *Store) remove(p string) error {
	f := s.fs()
	if err := f.MkdirAll(filepath.Join(s.Root, trashDir), 0o755); err != nil {
		return err
	}
	dst := filepath.Join(s.Root, trashDir, s.token())
	if err := f.Rename(p, dst); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // someone else removed it
		}
		return err
	}
	return f.RemoveAll(dst)
}

// dropEmpty removes a tool folder that holds no version. Remove fails on a
// folder that is not empty, which is what we want; RemoveAll is not used.
func (s *Store) dropEmpty(dir string) {
	des, err := s.fs().ReadDir(dir)
	if err == nil && len(des) == 0 {
		_ = s.fs().RemoveAll(dir)
	}
}

// sweepOrphans removes everything in the trash and every stage folder older
// than StageTTL (a crashed job). It returns how many it removed.
func (s *Store) sweepOrphans() int {
	n := 0
	cutoff := s.now().Add(-s.stageTTL())
	for _, d := range []string{trashDir, stageDir} {
		des, err := s.fs().ReadDir(filepath.Join(s.Root, d))
		if err != nil {
			continue
		}
		for _, de := range des {
			p := filepath.Join(s.Root, d, de.Name())
			if d == stageDir {
				fi, err := s.fs().Stat(p)
				if err != nil || fi.ModTime().After(cutoff) {
					continue
				}
			}
			if s.fs().RemoveAll(p) == nil {
				n++
			}
		}
	}
	return n
}

// Wipe removes every entry (caches R5.30, one machine's tool cache). A running
// job that holds a tool keeps its open files only where the system allows.
func (s *Store) Wipe() (int, error) {
	es, err := s.Entries()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range es {
		if err := s.remove(e.Path); err != nil {
			return n, err
		}
		s.dropEmpty(filepath.Join(s.Root, e.Tool))
		n++
	}
	return n, nil
}
