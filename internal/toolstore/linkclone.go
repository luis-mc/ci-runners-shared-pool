package toolstore

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// LinkCloner clones a tree with hard links: folders are made new, files are linked. The source must be
// read-only for the job's identity (it is sealed, R5.115), because a write through a link changes the source.
// A file that cannot be linked (another volume) is copied.
type LinkCloner struct{}

var _ Cloner = LinkCloner{}

// Clone implements Cloner.
func (LinkCloner) Clone(ctx context.Context, src, dst string) error {
	return walkClone(ctx, src, dst, os.Link)
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
