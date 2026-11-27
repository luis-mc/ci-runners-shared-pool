package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ExtractBinary writes the `crsp` executable of a crsp-<os>-<arch>.tar.gz to
// dst (mode 0755). Only that one regular file is read; no other path in the
// archive is ever written, so a crafted archive cannot escape.
func ExtractBinary(archive, dst string) error {
	f, err := os.Open(archive) //nolint:gosec // downloaded file in the work folder
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
			return errors.New("the archive holds no crsp binary")
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || path.Base(h.Name) != "crsp" {
			continue
		}
		const max = 300 << 20
		if h.Size > max {
			return errors.New("the binary in the archive is too large")
		}
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755) //nolint:gosec // next to the running binary
		if err != nil {
			return err
		}
		_, err = io.Copy(out, io.LimitReader(tr, h.Size))
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(dst)
		}
		return err
	}
}

// selfCheck runs the new binary's `version` command and expects it to print
// the release version (operations R5.13: it starts and says what it is).
func selfCheck(ctx context.Context, bin, version string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version").Output() //nolint:gosec // a binary that passed SHA-256 and provenance
	if err != nil {
		return err
	}
	if !strings.Contains(string(out), version) {
		return fmt.Errorf("it reports %q, not %s", strings.TrimSpace(string(out)), version)
	}
	return nil
}

var schemaLine = regexp.MustCompile(`(?m)^schema (\d+)\s*$`)

// binarySchema reads the database schema version the new binary prints on the
// second line of `version` (operations R5.13). ok is false when it prints none
// (a build older than the line), which never blocks an update.
func binarySchema(ctx context.Context, bin string) (v int64, ok bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version").Output() //nolint:gosec // a binary that passed SHA-256 and provenance
	if err != nil {
		return 0, false, err
	}
	m := schemaLine.FindStringSubmatch(string(out))
	if m == nil {
		return 0, false, nil
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	return n, err == nil, err
}
