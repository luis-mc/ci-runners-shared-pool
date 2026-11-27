package runnerswap

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxUnpacked bounds what an archive may write, so a bad bundle cannot fill the disk.
const maxUnpacked = 4 << 30

// ErrBadArchive is a bundle that is neither tar.gz nor zip, or that holds a
// path outside its folder.
var ErrBadArchive = errors.New("the runner bundle is not a valid archive")

// Unpack writes a runner archive (tar.gz or zip, found by its first bytes)
// into dest. A path that leaves dest, or a link that points outside it, is
// refused.
func Unpack(data []byte, dest string) error {
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return err
	}
	switch {
	case bytes.HasPrefix(data, []byte{0x1f, 0x8b}):
		return unpackTar(data, dest)
	case bytes.HasPrefix(data, []byte("PK")):
		return unpackZip(data, dest)
	}
	return ErrBadArchive
}

// inside returns the path of name under dest, or an error when it leaves dest.
func inside(dest, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." {
		return dest, nil
	}
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q leaves the folder", ErrBadArchive, name)
	}
	return filepath.Join(dest, clean), nil
}

func unpackTar(data []byte, dest string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBadArchive, err)
	}
	tr := tar.NewReader(gz)
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return gz.Close()
		}
		if err != nil {
			return fmt.Errorf("%w: %w", ErrBadArchive, err)
		}
		target, err := inside(dest, h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
		case tar.TypeReg:
			total += h.Size
			if total > maxUnpacked {
				return fmt.Errorf("%w: too large", ErrBadArchive)
			}
			if err := writeFile(target, tr, os.FileMode(h.Mode&0o777)); err != nil { //nolint:gosec // masked to permission bits
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return err
			}
			if filepath.IsAbs(h.Linkname) {
				return fmt.Errorf("%w: link %q points outside the folder", ErrBadArchive, h.Name)
			}
			if _, err := inside(dest, filepath.Join(filepath.Dir(h.Name), h.Linkname)); err != nil {
				return fmt.Errorf("%w: link %q points outside the folder", ErrBadArchive, h.Name)
			}
			if err := os.Symlink(h.Linkname, target); err != nil {
				return err
			}
		}
	}
}

func unpackZip(data []byte, dest string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBadArchive, err)
	}
	var total uint64
	for _, f := range zr.File {
		target, err := inside(dest, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
			continue
		}
		total += f.UncompressedSize64
		if total > maxUnpacked {
			return fmt.Errorf("%w: too large", ErrBadArchive)
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("%w: %w", ErrBadArchive, err)
		}
		werr := writeFile(target, rc, f.Mode().Perm())
		cerr := rc.Close()
		if werr != nil {
			return werr
		}
		if cerr != nil {
			return cerr
		}
	}
	return nil
}

func writeFile(path string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0o600) //nolint:gosec // path built by inside()
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, io.LimitReader(r, maxUnpacked)); err != nil { //nolint:gosec // bounded above
		_ = f.Close()
		return err
	}
	return f.Close()
}
