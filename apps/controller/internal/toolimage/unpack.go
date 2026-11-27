package toolimage

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
)

// ErrUnsafeEntry is returned for a layer entry that is not a plain file or
// folder, or whose path leaves the tree.
var ErrUnsafeEntry = errors.New("tool image layer holds an unsafe entry")

// MaxFileBytes caps one unpacked file (a file is held in memory once).
const MaxFileBytes = 1 << 30

// Unpack pulls the image by digest and writes the merged tree into dest. The
// work is done in dest+".part"; dest appears only when every digest matched,
// and a failure deletes the part, so a half-unpacked tree is never used.
func Unpack(ctx context.Context, p Puller, files TreeFS, ref Ref, dest string) error {
	m, err := FetchManifest(ctx, p, ref)
	if err != nil {
		return err
	}
	part := dest + ".part"
	if err := files.RemoveAll(part); err != nil {
		return err
	}
	if err := files.MkdirAll(part, 0o755); err != nil {
		return err
	}
	for _, d := range m.Layers {
		if err := ctx.Err(); err != nil {
			_ = files.RemoveAll(part)
			return err
		}
		ls, err := openLayer(ctx, p, ref, d)
		if err != nil {
			_ = files.RemoveAll(part)
			return err
		}
		uerr := applyLayer(files, ls, part)
		cerr := ls.Check()
		if cerr != nil || uerr != nil {
			_ = files.RemoveAll(part)
			if cerr != nil {
				return cerr
			}
			return uerr
		}
	}
	if err := files.RemoveAll(dest); err != nil {
		return err
	}
	return files.Rename(part, dest)
}

// TreeFS is the file system an unpack needs: the small sys.FS plus an atomic
// rename, so the finished tree appears all at once.
type TreeFS interface {
	sys.FS
	Rename(oldpath, newpath string) error
}

// OSTree is the real TreeFS.
type OSTree struct{ sys.OSFS }

// Rename implements TreeFS.
func (OSTree) Rename(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }

func applyLayer(files sys.FS, r io.Reader, root string) error {
	br := bufio.NewReader(r)
	var src io.Reader = br
	if magic, err := br.Peek(2); err == nil && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return fmt.Errorf("layer gzip: %w", err)
		}
		defer func() { _ = gz.Close() }()
		src = gz
	}
	tr := tar.NewReader(src)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("layer tar: %w", err)
		}
		rel, err := cleanRel(h.Name)
		if err != nil {
			return err
		}
		if rel == "" {
			continue
		}
		base := path.Base(rel)
		dir := path.Dir(rel)
		switch {
		case base == ".wh..wh..opq":
			if err := clearDir(files, root, dir); err != nil {
				return err
			}
			continue
		case strings.HasPrefix(base, ".wh."):
			if err := files.RemoveAll(join(root, path.Join(dir, strings.TrimPrefix(base, ".wh.")))); err != nil {
				return err
			}
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := files.MkdirAll(join(root, rel), 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if h.Size > MaxFileBytes {
				return fmt.Errorf("%s: file too large: %w", h.Name, ErrUnsafeEntry)
			}
			b, err := io.ReadAll(io.LimitReader(tr, h.Size))
			if err != nil {
				return fmt.Errorf("layer file %s: %w", h.Name, err)
			}
			if err := files.MkdirAll(join(root, dir), 0o755); err != nil {
				return err
			}
			mode := fs.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				mode = 0o755
			}
			if err := files.WriteFile(join(root, rel), b, mode); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s (type %q): %w", h.Name, string(h.Typeflag), ErrUnsafeEntry)
		}
	}
}

// cleanRel returns the slash path of an entry inside the tree, "" for the root.
func cleanRel(name string) (string, error) {
	if strings.ContainsRune(name, 0) || strings.ContainsRune(name, '\\') || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("%q: %w", name, ErrUnsafeEntry)
	}
	c := path.Clean(name)
	if c == "." {
		return "", nil
	}
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("%q: %w", name, ErrUnsafeEntry)
	}
	return c, nil
}

func join(root, rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

// clearDir implements an opaque whiteout: the lower layers' content of the
// folder goes, the folder stays.
func clearDir(files sys.FS, root, dir string) error {
	p := join(root, dir)
	if err := files.RemoveAll(p); err != nil {
		return err
	}
	return files.MkdirAll(p, 0o755)
}
