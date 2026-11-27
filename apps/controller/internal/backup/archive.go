// Package backup writes, lists, restores and drills backups of the controller
// (operations R5.21 to R5.27, R6.4 to R6.9).
//
// A backup is one archive crsp-backup-<yyyy-mm-dd-hhmm>.tar.gz in
// <data dir>/backups holding controller.db (a VACUUM INTO copy), config.toml
// and manifest.json with the SHA-256 of each file. It never holds a secret:
// secrets live in the OS secret store, and a config file that holds one is
// left out.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/config"
)

// Names of the files inside an archive.
const (
	DBName       = "controller.db"
	ConfigName   = "config.toml"
	ManifestName = "manifest.json"
	// FilePrefix and FileSuffix frame an archive's file name.
	FilePrefix = "crsp-backup-"
	FileSuffix = ".tar.gz"
	// maxFileBytes bounds one extracted file (a corrupt archive cannot fill the disk).
	maxFileBytes = 64 << 30
)

// ManifestFile is one file of an archive with its checksum.
type ManifestFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manifest describes an archive (operations R5.21).
type Manifest struct {
	ProductVersion string           `json:"product_version"`
	SchemaVersion  int64            `json:"schema_version"`
	CreatedAt      time.Time        `json:"created_at"`
	Files          []ManifestFile   `json:"files"`
	RowCounts      map[string]int64 `json:"row_counts"`
	// ConfigSkipped says why the config file is not in the archive, if so.
	ConfigSkipped string `json:"config_skipped,omitempty"`
}

// File returns the entry for name.
func (m Manifest) File(name string) (ManifestFile, bool) {
	for _, f := range m.Files {
		if f.Name == name {
			return f, true
		}
	}
	return ManifestFile{}, false
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// writeArchive packs the files in dir named by m into dest, manifest last.
func writeArchive(dest, dir string, m Manifest) (err error) {
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	add := func(name string, r io.Reader, size int64) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: size, ModTime: m.CreatedAt}); err != nil {
			return err
		}
		_, err := io.Copy(tw, r)
		return err
	}
	for _, f := range m.Files {
		src, err := os.Open(filepath.Join(dir, f.Name))
		if err != nil {
			return err
		}
		err = add(f.Name, src, f.Size)
		_ = src.Close()
		if err != nil {
			return err
		}
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := add(ManifestName, bytesReader(raw), int64(len(raw))); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// ErrBadArchive wraps every reason an archive is refused.
var ErrBadArchive = errors.New("backup archive is not valid")

func badf(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrBadArchive, fmt.Sprintf(format, a...))
}

// extract unpacks the archive into dest and checks each file's SHA-256 against
// the manifest (operations R5.25). Only known names are accepted.
func extract(archive, dest string) (Manifest, error) {
	f, err := os.Open(archive)
	if err != nil {
		return Manifest{}, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return Manifest{}, badf("not a gzip file")
	}
	defer func() { _ = gz.Close() }()
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return Manifest{}, err
	}
	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Manifest{}, badf("unreadable: %v", err)
		}
		switch h.Name {
		case DBName, ConfigName, ManifestName:
		default:
			return Manifest{}, badf("unexpected file %q", h.Name)
		}
		if h.Typeflag != tar.TypeReg || seen[h.Name] {
			return Manifest{}, badf("bad entry %q", h.Name)
		}
		seen[h.Name] = true
		out, err := os.OpenFile(filepath.Join(dest, h.Name), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return Manifest{}, err
		}
		_, err = io.Copy(out, io.LimitReader(tr, maxFileBytes))
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return Manifest{}, badf("unreadable: %v", err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dest, ManifestName))
	if err != nil {
		return Manifest{}, badf("no %s", ManifestName)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Manifest{}, badf("%s is not valid: %v", ManifestName, err)
	}
	if _, ok := m.File(DBName); !ok {
		return Manifest{}, badf("%s lists no database", ManifestName)
	}
	for _, mf := range m.Files {
		if !seen[mf.Name] {
			return Manifest{}, badf("%s is listed but missing", mf.Name)
		}
		sum, size, err := hashFile(filepath.Join(dest, mf.Name))
		if err != nil {
			return Manifest{}, err
		}
		if sum != mf.SHA256 || size != mf.Size {
			return Manifest{}, badf("%s does not match its SHA-256 in the manifest", mf.Name)
		}
	}
	return m, nil
}

// configIsClean reports whether the config file may go into a backup: it must
// parse and hold no secret key (operations R6.8).
func configIsClean(data []byte) error {
	if keys := config.SecretKeys(data); len(keys) > 0 {
		return fmt.Errorf("it holds secret keys (%v)", keys)
	}
	return nil
}
