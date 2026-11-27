package collect

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// Caps of R5.2 and R5.3.
const (
	MaxLogBytes     int64 = 64 << 20  // a log over this is not stored
	MaxReportBytes  int64 = 50 << 20  // one report file
	MaxRunReportSum int64 = 200 << 20 // all report files of one run
)

// DirName is the folder inside the data dir that holds the CI data (R5.2).
const DirName = "ci-data"

// FSVault is the Vault on the owner's disk: `<data dir>/ci-data/<repository id>/logs/...` and `.../reports/...`,
// every file redacted, gzipped and readable by its owner only. Nothing leaves the machine (D4).
type FSVault struct {
	// Dir is the data dir.
	Dir string
	// Redactor masks secrets. A nil Redactor stores nothing.
	Redactor Redactor
	// MaxLog and MaxReport override the caps of R5.2, R5.3 (zero keeps them); tests use small ones.
	MaxLog, MaxReport int64
}

var _ Vault = (*FSVault)(nil)

// PutLog stores one job log: redacted, env: values dropped, gzipped. A log over the cap is ErrTooLarge.
func (v *FSVault) PutLog(ctx context.Context, k Key, r io.Reader) (Stored, error) {
	rel := path.Join(DirName, strconv.FormatInt(k.RepositoryID, 10), "logs",
		fmt.Sprintf("%d-%d-%d.log.gz", k.RunID, k.RunAttempt, k.JobID))
	return v.put(ctx, rel, r, scrubOptions{maxRaw: orDefault(v.MaxLog, MaxLogBytes), envBlocks: true})
}

// PutReport stores one report file of an artifact, redacted and gzipped.
func (v *FSVault) PutReport(ctx context.Context, k Key, r io.Reader) (Stored, error) {
	sum := sha256.Sum256([]byte(k.Member))
	rel := path.Join(DirName, strconv.FormatInt(k.RepositoryID, 10), "reports",
		fmt.Sprintf("%d-%d-%d-%s-%s.gz", k.RunID, k.RunAttempt, k.ArtifactID, hex.EncodeToString(sum[:4]), safeName(k.Member)))
	return v.put(ctx, rel, r, scrubOptions{maxRaw: orDefault(v.MaxReport, MaxReportBytes)})
}

func orDefault(v, d int64) int64 {
	if v > 0 {
		return v
	}
	return d
}

// safeName keeps the last element of a member name, in letters, digits and . _ -, at most 60 characters.
func safeName(member string) string {
	base := path.Base(strings.ReplaceAll(member, "\\", "/"))
	var b strings.Builder
	for _, c := range base {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			b.WriteRune(c)
		default:
			b.WriteByte('_')
		}
	}
	s := strings.TrimLeft(b.String(), ".")
	if len(s) > 60 {
		s = s[len(s)-60:]
	}
	if s == "" {
		s = "file"
	}
	return s
}

// put writes rel atomically: a temp file in the same folder, renamed when whole. A failure leaves nothing.
func (v *FSVault) put(ctx context.Context, rel string, r io.Reader, opt scrubOptions) (st Stored, err error) {
	if v.Redactor == nil {
		return Stored{}, ErrRedactor
	}
	full := filepath.Join(v.Dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return Stored{}, fmt.Errorf("make the CI data folder: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), ".put-*")
	if err != nil {
		return Stored{}, fmt.Errorf("make a CI data file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if err = os.Chmod(tmp.Name(), 0o600); err != nil {
		return Stored{}, err
	}
	gz := gzip.NewWriter(tmp)
	res, err := scrub(gz, &ctxReader{ctx: ctx, r: r}, v.Redactor, opt)
	if err != nil {
		return Stored{}, err
	}
	if err = gz.Close(); err != nil {
		return Stored{}, fmt.Errorf("write a CI data file: %w", err)
	}
	info, err := tmp.Stat()
	if err != nil {
		return Stored{}, err
	}
	if err = tmp.Close(); err != nil {
		return Stored{}, err
	}
	if err = os.Rename(tmp.Name(), full); err != nil {
		return Stored{}, fmt.Errorf("store a CI data file: %w", err)
	}
	return Stored{Path: rel, Bytes: info.Size(), RawBytes: res.raw, Lines: res.lines, SHA256: res.sum}, nil
}

// ctxReader stops a copy when ctx ends.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// resolve checks that rel is a path this vault made and returns the full path.
func (v *FSVault) resolve(rel string) (string, error) {
	clean := path.Clean(rel)
	if !strings.HasPrefix(clean, DirName+"/") || strings.Contains(clean, "..") {
		return "", fmt.Errorf("%q is not a CI data path", rel)
	}
	return filepath.Join(v.Dir, filepath.FromSlash(clean)), nil
}

type gzFile struct {
	*gzip.Reader
	f *os.File
}

func (g gzFile) Close() error {
	_ = g.Reader.Close()
	return g.f.Close()
}

// Open opens a stored file decompressed.
func (v *FSVault) Open(_ context.Context, rel string) (io.ReadCloser, error) {
	full, err := v.resolve(rel)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("read a CI data file: %w", err)
	}
	return gzFile{Reader: zr, f: f}, nil
}

// Remove deletes a stored file. A file that is already gone is not an error.
func (v *FSVault) Remove(_ context.Context, rel string) error {
	full, err := v.resolve(rel)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
