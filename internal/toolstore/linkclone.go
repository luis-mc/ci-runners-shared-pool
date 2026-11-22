package toolstore

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// LinkCloner clones a tree with hard links: folders are made new, files are linked. The source must be
// read-only for the job's identity (it is sealed, R5.115), because a write through a link changes the source.
// A file that cannot be linked (another volume, a file system with no hard links) is copied instead, and the
// first time that happens for a cache OnFallback is called once, so the owner can read why it is slower.
type LinkCloner struct {
	// Link makes a hard link; nil means os.Link. Tests give a fake linker.
	Link func(from, to string) error
	// OnFallback is called with the cache folder and the link error, once per cache folder for the cloner's
	// life when the cloner came from NewLinkCloner, else once per Clone call. Nil logs nothing.
	OnFallback func(cache string, err error)

	logged *sync.Map
}

var _ Cloner = LinkCloner{}

// NewLinkCloner returns a LinkCloner that reports the copy fallback once per cache folder.
func NewLinkCloner(link func(from, to string) error, onFallback func(cache string, err error)) LinkCloner {
	return LinkCloner{Link: link, OnFallback: onFallback, logged: &sync.Map{}}
}

// Clone implements Cloner.
func (l LinkCloner) Clone(ctx context.Context, src, dst string) error {
	link := l.Link
	if link == nil {
		link = os.Link
	}
	var once sync.Once
	return walkClone(ctx, src, dst, func(from, to string) error {
		err := link(from, to)
		if err != nil && l.OnFallback != nil {
			once.Do(func() {
				if l.logged != nil {
					if _, seen := l.logged.LoadOrStore(src, true); seen {
						return
					}
				}
				l.OnFallback(src, err)
			})
		}
		return err // walkClone copies the file when the link failed
	})
}

// CopyCloner copies every file byte for byte, so the copy shares nothing with the source.
type CopyCloner struct{}

var _ Cloner = CopyCloner{}

// Clone implements Cloner.
func (CopyCloner) Clone(ctx context.Context, src, dst string) error {
	return walkClone(ctx, src, dst, copyFile)
}

func walkClone(ctx context.Context, src, dst string, file func(from, to string) error) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(to, 0o755)
		case d.Type().IsRegular():
			if err := file(p, to); err != nil {
				return copyFile(p, to)
			}
			return nil
		}
		return nil // links and devices are not cache content
	})
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm()|0o200)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
