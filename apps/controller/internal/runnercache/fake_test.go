package runnercache_test

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing/fstest"
	"time"
)

// memFS is a fake file system over fstest.MapFS: paths are slash paths
// without a leading slash, and RemoveAll deletes a whole subtree.
type memFS struct{ m fstest.MapFS }

func newMem() *memFS { return &memFS{m: fstest.MapFS{}} }

// add puts a file of size bytes, last modified age before base.
func (f *memFS) add(path string, size int, age time.Duration) {
	f.m[path] = &fstest.MapFile{Data: make([]byte, size), ModTime: base.Add(-age)}
}

func (f *memFS) ReadDir(d string) ([]fs.DirEntry, error) { return fs.ReadDir(f.m, filepath.ToSlash(d)) }
func (f *memFS) Stat(p string) (fs.FileInfo, error)      { return fs.Stat(f.m, filepath.ToSlash(p)) }
func (f *memFS) RemoveAll(p string) error {
	p = filepath.ToSlash(p)
	for k := range f.m {
		if k == p || strings.HasPrefix(k, p+"/") {
			delete(f.m, k)
		}
	}
	return nil
}

func (f *memFS) has(p string) bool {
	_, err := fs.Stat(f.m, p)
	return err == nil
}
