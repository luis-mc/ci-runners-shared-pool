package toolstore

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// maxUnpackBytes caps one archive's unpacked size (a JDK is about 400 MB).
const maxUnpackBytes = 4 << 30

// ErrUnsafeArchive is returned for an archive with a link, device, absolute or ".." path.
var ErrUnsafeArchive = errors.New("toolstore: unsafe archive entry")

// VerifySHA256 checks the file against a SHA-256 in hex. The expected value may be a checksum file's text:
// "<hex>" or "<hex>  <name>"; only the first word counts.
func VerifySHA256(file, expected string) error {
	w := strings.Fields(expected)
	if len(w) == 0 {
		return errors.New("toolstore: empty checksum")
	}
	want := strings.ToLower(w[0])
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("toolstore: SHA-256 of %s is %s, expected %s", filepath.Base(file), got, want)
	}
	return nil
}

// SumFromList finds the SHA-256 of a file in a checksum list ("<hex>  <name>" per line, as Node's
// SHASUMS256.txt). A file that is not in the list is an error: the download is then refused.
func SumFromList(list, name string) (string, error) {
	for _, line := range strings.Split(list, "\n") {
		w := strings.Fields(line)
		if len(w) == 2 && strings.TrimPrefix(w[1], "*") == name {
			return w[0], nil
		}
	}
	return "", fmt.Errorf("toolstore: no SHA-256 for %s in the checksum list", name)
}

// cleanEntry strips strip leading folders from an archive path and refuses unsafe ones. An empty result
// means "skip this entry" (it was inside the stripped folders).
func cleanEntry(name string, strip int) (string, error) {
	name = strings.ReplaceAll(name, `\`, "/")
	if strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return "", fmt.Errorf("%w: %q", ErrUnsafeArchive, name)
	}
	parts := strings.Split(path.Clean(name), "/")
	for _, p := range parts {
		if p == ".." {
			return "", fmt.Errorf("%w: %q", ErrUnsafeArchive, name)
		}
	}
	if len(parts) <= strip {
		return "", nil
	}
	return strings.Join(parts[strip:], "/"), nil
}

// Unpack unpacks a .zip or .tar.gz archive into dest, dropping strip leading folders. Regular files and
// folders only: any other entry type refuses the whole archive.
func Unpack(archive, dest string, strip int) error {
	var written int64
	put := func(rel string, mode os.FileMode, r io.Reader) error {
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm()|0o200)
		if err != nil {
			return err
		}
		n, err := io.Copy(f, io.LimitReader(r, maxUnpackBytes-written+1))
		written += n
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err == nil && written > maxUnpackBytes {
			err = errors.New("toolstore: archive is too large")
		}
		return err
	}
	if strings.HasSuffix(archive, ".zip") {
		zr, err := zip.OpenReader(archive)
		if err != nil {
			return err
		}
		defer func() { _ = zr.Close() }()
		for _, e := range zr.File {
			rel, err := cleanEntry(e.Name, strip)
			if err != nil {
				return err
			}
			if rel == "" {
				continue
			}
			switch m := e.Mode(); {
			case m.IsDir():
				if err := os.MkdirAll(filepath.Join(dest, filepath.FromSlash(rel)), 0o755); err != nil {
					return err
				}
			case m.IsRegular():
				rc, err := e.Open()
				if err != nil {
					return err
				}
				err = put(rel, m, rc)
				_ = rc.Close()
				if err != nil {
					return err
				}
			default:
				return fmt.Errorf("%w: %q is not a regular file", ErrUnsafeArchive, e.Name)
			}
		}
		return nil
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		rel, err := cleanEntry(h.Name, strip)
		if err != nil {
			return err
		}
		if rel == "" {
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(filepath.Join(dest, filepath.FromSlash(rel)), 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := put(rel, os.FileMode(h.Mode), tr); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: %q is not a regular file", ErrUnsafeArchive, h.Name)
		}
	}
}
