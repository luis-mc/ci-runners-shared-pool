package runnercache

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// RustLimits are the two caps of the Rust build cache folder (caches R5.45):
// Total is the cap of the whole folder, Key the cap of one cache key (one
// top-level folder).
type RustLimits struct{ Total, Key int64 }

// Action is one removal the Rust cap made, with its reason for the job log.
type Action struct {
	Path  string
	Bytes int64
	Why   string
}

// RustResult is what one Rust cap pass did.
type RustResult struct {
	Before, After int64
	Actions       []Action
	// Skipped says why nothing was done, such as "runner busy".
	Skipped string
}

// Lines are the log lines of the pass: one per removal, each naming what went
// and why. The hook writes them so they reach the job's log.
func (r RustResult) Lines() []string {
	out := make([]string, 0, len(r.Actions))
	for _, a := range r.Actions {
		out = append(out, fmt.Sprintf("Rust build cache: removed %s (%d MiB): %s", filepath.Base(a.Path), a.Bytes>>20, a.Why))
	}
	return out
}

// EnforceRust brings the Rust cache folder under its caps (caches R5.45).
//
// keep is the name of the cache key the job uses; it is never removed whole.
// When keep is empty the key changed most recently stands for it, which is the
// conservative reading: the newest key is the one in use. A busy runner is
// left alone.
//
// Order: (1) a key over the key cap loses its incremental/ folders, then its
// content; (2) while the folder is over the total cap, the other keys go,
// oldest first, so the newest other key is the last to go; (3) when the key in
// use alone is still over the total cap, its incremental/ folders go, then
// its whole content (the folder stays).
func EnforceRust(fsys FS, dir string, lim RustLimits, keep string, busy bool) (RustResult, error) {
	es, err := Scan(fsys, dir)
	if err != nil {
		return RustResult{}, err
	}
	res := RustResult{Before: Total(es)}
	res.After = res.Before
	if busy {
		res.Skipped = "runner busy"
		return res, nil
	}
	if len(es) == 0 {
		return res, nil
	}
	if keep == "" {
		keep = filepath.Base(newestEntry(es).Path)
	}
	var inUse *Entry
	var others []Entry
	for i := range es {
		if filepath.Base(es[i].Path) == keep {
			inUse = &es[i]
		} else {
			others = append(others, es[i])
		}
	}
	act := func(path string, n int64, why string) {
		res.After -= n
		res.Actions = append(res.Actions, Action{Path: path, Bytes: n, Why: why})
	}
	// (1) the cap of one key.
	if lim.Key > 0 {
		for i := range others {
			if others[i].Size > lim.Key {
				freed, err := trimEntry(fsys, others[i], lim.Key, true, "over the cap of one key", act)
				if err != nil {
					return res, err
				}
				others[i].Size -= freed
			}
		}
		if inUse != nil && inUse.Size > lim.Key {
			freed, err := trimEntry(fsys, *inUse, lim.Key, false, "the key in use is over the cap of one key", act)
			if err != nil {
				return res, err
			}
			inUse.Size -= freed
		}
	}
	// (2) the total cap: other keys, oldest first.
	if lim.Total > 0 {
		for _, e := range Order(others) {
			if res.After <= lim.Total {
				break
			}
			if e.Size == 0 {
				continue
			}
			if err := fsys.RemoveAll(e.Path); err != nil {
				return res, err
			}
			act(e.Path, e.Size, "the folder was over its cap and this key is not in use")
		}
		// (3) the key in use alone.
		if inUse != nil && res.After > lim.Total {
			if _, err := trimEntry(fsys, *inUse, inUse.Size-(res.After-lim.Total), false, "the key in use alone is over the cap", act); err != nil {
				return res, err
			}
		}
	}
	return res, nil
}

func newestEntry(es []Entry) Entry {
	o := Order(es)
	return o[len(o)-1]
}

// trimEntry makes an entry smaller than target bytes: its incremental/ folders
// go first (largest first), then, if it is still too big, its whole content.
// A key that is not in use is removed whole at that point (whole is true); the
// key in use keeps its folder. It returns the bytes freed.
func trimEntry(fsys FS, e Entry, target int64, whole bool, why string, act func(string, int64, string)) (int64, error) {
	var freed int64
	cur := e.Size
	incs, err := incrementalDirs(fsys, e.Path, 3)
	if err != nil {
		return 0, err
	}
	for _, d := range incs {
		if cur <= target {
			return freed, nil
		}
		if err := fsys.RemoveAll(d.Path); err != nil {
			return freed, err
		}
		cur -= d.Size
		freed += d.Size
		act(d.Path, d.Size, why+"; incremental folders go first")
	}
	if cur <= target {
		return freed, nil
	}
	if whole {
		if err := fsys.RemoveAll(e.Path); err != nil {
			return freed, err
		}
		act(e.Path, cur, why+"; the whole key goes")
		return freed + cur, nil
	}
	des, err := fsys.ReadDir(e.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return freed, nil
		}
		return freed, err
	}
	for _, de := range des {
		p := filepath.Join(e.Path, de.Name())
		sub := Entry{Path: p}
		if err := walk(fsys, p, &sub, nil); err != nil {
			return freed, err
		}
		if err := fsys.RemoveAll(p); err != nil {
			return freed, err
		}
		cur -= sub.Size
		freed += sub.Size
		act(p, sub.Size, why+"; the whole content goes")
	}
	return freed, nil
}

// incrementalDirs finds folders named incremental under root, at most depth
// levels down (target/<profile>/incremental), largest first.
func incrementalDirs(fsys FS, root string, depth int) ([]Entry, error) {
	var out []Entry
	var visit func(dir string, d int) error
	visit = func(dir string, d int) error {
		des, err := fsys.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		for _, de := range des {
			if !de.IsDir() {
				continue
			}
			p := filepath.Join(dir, de.Name())
			if de.Name() == "incremental" {
				e := Entry{Path: p}
				if err := walk(fsys, p, &e, nil); err != nil {
					return err
				}
				out = append(out, e)
				continue
			}
			if d < depth {
				if err := visit(p, d+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(root, 1); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}
