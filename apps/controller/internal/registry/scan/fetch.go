package scan

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Pin is one platform entry of the CI tool pin list: the same file CI
// uses (DL-94), so a tool is pinned once.
type Pin struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Kind   string `json:"kind"`
	BinDir string `json:"bin_dir"`
	Bin    string `json:"bin"`
}

// LoadPin reads the pin of tool for platform (linux_x64, linux_arm64) from the
// bytes of the pin list.
func LoadPin(toolsJSON []byte, tool, platform string) (version string, p Pin, err error) {
	var all map[string]struct {
		Version   string         `json:"version"`
		Platforms map[string]Pin `json:"platforms"`
	}
	if err := json.Unmarshal(toolsJSON, &all); err != nil {
		return "", Pin{}, fmt.Errorf("the tool pins: %w", err)
	}
	t, ok := all[tool]
	if !ok {
		return "", Pin{}, fmt.Errorf("tool %q is not pinned", tool)
	}
	p, ok = t.Platforms[platform]
	if !ok || p.URL == "" || len(p.SHA256) != 64 {
		return "", Pin{}, fmt.Errorf("tool %q has no usable pin for %s", tool, platform)
	}
	if p.Kind != "archive" || !strings.HasSuffix(p.URL, ".tar.gz") {
		return "", Pin{}, fmt.Errorf("tool %q: only .tar.gz archive pins are supported here", tool)
	}
	return t.Version, p, nil
}

// Fetcher downloads pinned tools into a cache folder that outlives a scan.
type Fetcher struct {
	Dir      string // cache root
	HTTP     *http.Client
	Attempts int           // zero means 5
	Wait     time.Duration // base wait between attempts; zero means 10 s
}

// Fetch returns the path of the tool binary. A cached archive is used only
// when its sha256 equals the pin; a download that does not match is rejected
// and nothing of it stays.
func (f Fetcher) Fetch(ctx context.Context, tool, version string, p Pin) (string, error) {
	root := filepath.Join(f.Dir, tool, version)
	bin := filepath.Join(root, "unpacked", filepath.FromSlash(path.Join(p.BinDir, p.Bin)))
	archive := filepath.Join(root, path.Base(p.URL))
	if sumOf(archive) != p.SHA256 { // also true when the file is missing
		_ = os.Remove(archive)
		if err := f.download(ctx, p, archive); err != nil {
			return "", err
		}
	}
	if st, err := os.Stat(bin); err == nil && st.Mode().IsRegular() {
		return bin, nil
	}
	if err := untar(archive, filepath.Join(root, "unpacked")); err != nil {
		return "", fmt.Errorf("unpack %s: %w", tool, err)
	}
	if _, err := os.Stat(bin); err != nil {
		return "", fmt.Errorf("%s is not in the %s archive", p.Bin, tool)
	}
	return bin, nil
}

func (f Fetcher) download(ctx context.Context, p Pin, dst string) error {
	n, wait := f.Attempts, f.Wait
	if n == 0 {
		n = 5
	}
	if wait == 0 {
		wait = 10 * time.Second
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	var last error
	for i := 1; i <= n; i++ {
		if last = f.once(ctx, p, dst); last == nil {
			return nil
		}
		if i < n {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait * time.Duration(i)):
			}
		}
	}
	return fmt.Errorf("download %s failed after %d attempts: %w", p.URL, n, last)
}

func (f Fetcher) once(ctx context.Context, p Pin, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
	if err != nil {
		return err
	}
	hc := f.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".*.partial")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // the .partial never stays
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != p.SHA256 {
		return fmt.Errorf("sha256 is %s, pinned %s", got, p.SHA256)
	}
	return os.Rename(tmp.Name(), dst)
}

func sumOf(file string) string {
	fh, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer func() { _ = fh.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, fh); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

func untar(archive, dst string) error {
	fh, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer func() { _ = fh.Close() }()
	gz, err := gzip.NewReader(fh)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		clean := path.Clean(h.Name)
		if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("unsafe path %q", h.Name)
		}
		target := filepath.Join(dst, filepath.FromSlash(clean))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, io.LimitReader(tr, 1<<30)); err != nil {
				_ = out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		}
	}
}
