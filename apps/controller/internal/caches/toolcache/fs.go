package toolcache

import (
	"io/fs"
	"os"
	"time"
)

// FS is the file access the store needs; tests may give a fake.
type FS interface {
	Mkdir(path string, perm fs.FileMode) error
	MkdirAll(path string, perm fs.FileMode) error
	Chmod(path string, mode fs.FileMode) error
	Chtimes(path string, atime, mtime time.Time) error
	Rename(oldpath, newpath string) error
	RemoveAll(path string) error
	ReadDir(path string) ([]fs.DirEntry, error)
	Stat(path string) (fs.FileInfo, error)
}

// OSFS is the real file system.
type OSFS struct{}

func (OSFS) Mkdir(p string, m fs.FileMode) error     { return os.Mkdir(p, m) }
func (OSFS) MkdirAll(p string, m fs.FileMode) error  { return os.MkdirAll(p, m) }
func (OSFS) Chmod(p string, m fs.FileMode) error     { return os.Chmod(p, m) }
func (OSFS) Chtimes(p string, a, m time.Time) error  { return os.Chtimes(p, a, m) }
func (OSFS) Rename(o, n string) error                { return os.Rename(o, n) }
func (OSFS) RemoveAll(p string) error                { return os.RemoveAll(p) }
func (OSFS) ReadDir(p string) ([]fs.DirEntry, error) { return os.ReadDir(p) }
func (OSFS) Stat(p string) (fs.FileInfo, error)      { return os.Stat(p) }
