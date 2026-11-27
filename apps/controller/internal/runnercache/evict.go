package runnercache

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// FS is the file access eviction needs; tests give a fake.
type FS interface {
	// ReadDir lists the entries of dir.
	ReadDir(dir string) ([]fs.DirEntry, error)
	// Stat describes path.
	Stat(path string) (fs.FileInfo, error)
	// RemoveAll deletes path and what it holds.
	RemoveAll(path string) error
}

// OSFS is the real file system.
type OSFS struct{}

// ReadDir implements FS.
func (OSFS) ReadDir(d string) ([]fs.DirEntry, error) { return os.ReadDir(d) }

// Stat implements FS.
func (OSFS) Stat(p string) (fs.FileInfo, error) { return os.Stat(p) }

// RemoveAll implements FS.
func (OSFS) RemoveAll(p string) error { return os.RemoveAll(p) }

// Entry is one top-level item of a cache folder: a tool folder, a crate
// folder or a build folder. Eviction removes whole entries.
type Entry struct {
	Path string
	Size int64
	// Used is the newest time any file in the entry was modified. Reads are
	// not tracked (many volumes mount with noatime), so a tool a job reuses
	// keeps its entry fresh through the job's own touch (see Touch).
	Used time.Time
}

// Scan sizes every top-level entry of dir. A missing dir is an empty cache.
func Scan(fsys FS, dir string) ([]Entry, error) { return scanWith(fsys, dir, nil) }

// scanWith is Scan that also calls onFile for every file it sees, so one walk
// serves both the sizes and the size alert (caches R5.49).
func scanWith(fsys FS, dir string, onFile func(path string, size int64)) ([]Entry, error) {
	des, err := fsys.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Entry, 0, len(des))
	for _, de := range des {
		p := filepath.Join(dir, de.Name())
		e := Entry{Path: p}
		if err := walk(fsys, p, &e, onFile); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func walk(fsys FS, p string, e *Entry, onFile func(string, int64)) error {
	fi, err := fsys.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if t := fi.ModTime(); t.After(e.Used) {
		e.Used = t
	}
	if !fi.IsDir() {
		e.Size += fi.Size()
		if onFile != nil {
			onFile(p, fi.Size())
		}
		return nil
	}
	des, err := fsys.ReadDir(p)
	if err != nil {
		return err
	}
	for _, de := range des {
		if err := walk(fsys, filepath.Join(p, de.Name()), e, onFile); err != nil {
			return err
		}
	}
	return nil
}

// Total is the bytes an entry list holds.
func Total(es []Entry) int64 {
	var n int64
	for _, e := range es {
		n += e.Size
	}
	return n
}

// Order sorts entries oldest first (least recently used first). Ties break by
// path so the order is the same on every run.
func Order(es []Entry) []Entry {
	out := append([]Entry(nil), es...)
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Used.Equal(out[j].Used) {
			return out[i].Used.Before(out[j].Used)
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// Result is what one eviction pass did.
type Result struct {
	Before, After int64
	Quota         int64
	Removed       []Entry
	// Skipped says why nothing was done, such as "runner busy".
	Skipped string
}

// Enforce brings the cache folder under its quota by removing the least
// recently used entries first, and stops as soon as the folder fits. It runs
// only between jobs: when busy is true it removes nothing (caches R5.37).
func Enforce(fsys FS, dir string, quota int64, busy bool) (Result, error) {
	es, err := Scan(fsys, dir)
	if err != nil {
		return Result{}, err
	}
	res := Result{Before: Total(es), Quota: quota}
	res.After = res.Before
	if busy {
		res.Skipped = "runner busy"
		return res, nil
	}
	if quota <= 0 || res.Before <= quota {
		return res, nil
	}
	for _, e := range Order(es) {
		if res.After <= quota {
			break
		}
		if err := fsys.RemoveAll(e.Path); err != nil {
			return res, err
		}
		res.After -= e.Size
		res.Removed = append(res.Removed, e)
	}
	return res, nil
}

// Touch marks an entry as used now, so a job that reuses a cached tool keeps
// it out of the next eviction.
func Touch(path string, now time.Time) error { return os.Chtimes(path, now, now) }

// SweepResult is the outcome of both caps of one runner.
type SweepResult struct{ Tools, Rust Result }

// Sweep applies the tool quota and the Rust cap of one runner folder. Call it
// when the runner is between jobs (the job-completed hook, or a timer that
// first checks for an idle runner).
func Sweep(fsys FS, runnerFolder string, q Quota, busy bool) (SweepResult, error) {
	q = q.WithDefaults()
	d := DirsFor(runnerFolder)
	t, err := Enforce(fsys, d.Tools, q.ToolBytes(), busy)
	if err != nil {
		return SweepResult{Tools: t}, err
	}
	r, err := Enforce(fsys, d.Rust, q.RustBytes(), busy)
	return SweepResult{Tools: t, Rust: r}, err
}
