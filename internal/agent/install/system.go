// Package install runs the dependency-setup job on one machine (spec
// features/dependency-setup, CRSP248): download with a SHA-256 check, install
// by the pinned method of the OS, restart pause, verify and prepare, and undo
// of only what a run added. Every OS call goes through System so tests on
// Linux run the Windows and WSL paths with a fake.
package install

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sync"

	"crypto/sha256"
	"encoding/hex"
)

// System is every call the installer makes to the machine.
type System interface {
	// Run runs argv directly (never a shell string) and sends each output line
	// to onLine with stream "stdout" or "stderr". code is -1 when it did not start.
	Run(ctx context.Context, argv []string, onLine func(stream, line string)) (code int, err error)
	// Download fetches url to destPath; onProgress gets real bytes (total is 0 when unknown).
	Download(ctx context.Context, url, destPath string, onProgress func(bytes, total int64)) error
	SHA256(path string) (string, error)
	WriteFile(path string, data []byte, mode os.FileMode) error
	Remove(path string) error
	Exists(path string) bool
	GOOS() string
	// Arch is the Debian-style architecture: amd64 or arm64.
	Arch() string
	IsAdmin() bool
	ActiveJobs() int
	Reboot() error
	// TempDir is where downloads go.
	TempDir() string
}

// OSOptions are the parts of OSSystem the agent supplies.
type OSOptions struct {
	GOOS       string // "" = the running OS
	Arch       string
	Admin      func() bool
	ActiveJobs func() int
	Client     *http.Client
}

// OSSystem is the real System (os/exec and net/http).
type OSSystem struct {
	opt OSOptions
}

var _ System = (*OSSystem)(nil)

// NewOSSystem builds the real System.
func NewOSSystem(o OSOptions) *OSSystem {
	if o.Client == nil {
		o.Client = &http.Client{CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			if req.URL.Scheme != "https" {
				return errors.New("a redirect to a page that is not https was refused")
			}
			return nil
		}}
	}
	return &OSSystem{opt: o}
}

// GOOS implements System.
func (s *OSSystem) GOOS() string {
	if s.opt.GOOS != "" {
		return s.opt.GOOS
	}
	return goos
}

// Arch implements System.
func (s *OSSystem) Arch() string {
	if s.opt.Arch != "" {
		return s.opt.Arch
	}
	return debArch
}

// IsAdmin implements System.
func (s *OSSystem) IsAdmin() bool { return s.opt.Admin != nil && s.opt.Admin() }

// ActiveJobs implements System.
func (s *OSSystem) ActiveJobs() int {
	if s.opt.ActiveJobs == nil {
		return 0
	}
	return s.opt.ActiveJobs()
}

// TempDir implements System.
func (*OSSystem) TempDir() string { return os.TempDir() }

// Exists implements System.
func (*OSSystem) Exists(p string) bool { _, err := os.Stat(p); return err == nil }

// Remove implements System.
func (*OSSystem) Remove(p string) error {
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// WriteFile implements System.
func (*OSSystem) WriteFile(p string, data []byte, mode os.FileMode) error {
	return os.WriteFile(p, data, mode)
}

// SHA256 implements System.
func (*OSSystem) SHA256(p string) (string, error) {
	f, err := os.Open(p) //nolint:gosec // a path the installer chose
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Reboot implements System: restart the machine in about a minute.
func (s *OSSystem) Reboot() error {
	argv := []string{"shutdown", "-r", "+1"}
	if s.GOOS() == "windows" {
		argv = []string{"shutdown", "/r", "/t", "30"}
	}
	code, err := s.Run(context.Background(), argv, func(string, string) {})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("shutdown exited with code %d", code)
	}
	return nil
}

// Download implements System. Only https is fetched.
func (s *OSSystem) Download(ctx context.Context, rawURL, dest string, onProgress func(bytes, total int64)) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("only an https address is downloaded")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := s.opt.Client.Do(req) //nolint:gosec // the caller checked the host against the pinned list
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the server answered %d", resp.StatusCode)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // a path the installer chose
	if err != nil {
		return err
	}
	total := resp.ContentLength
	if total < 0 {
		total = 0
	}
	cw := &countWriter{w: f, total: total, on: onProgress}
	_, cerr := io.Copy(cw, resp.Body)
	if e := f.Close(); cerr == nil {
		cerr = e
	}
	if cerr != nil {
		_ = os.Remove(dest)
		return cerr
	}
	if onProgress != nil {
		onProgress(cw.n, max(total, cw.n))
	}
	return nil
}

type countWriter struct {
	w     io.Writer
	n     int64
	total int64
	on    func(bytes, total int64)
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	if c.on != nil {
		c.on(c.n, c.total)
	}
	return n, err
}

// Run implements System. No shell is used; the environment is the agent's.
func (s *OSSystem) Run(ctx context.Context, argv []string, onLine func(stream, line string)) (int, error) {
	if len(argv) == 0 || argv[0] == "" {
		return -1, errors.New("no command to run")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // argv is built by the installer from the pinned list
	var wg sync.WaitGroup
	pipe := func(stream string, r io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		sc.Split(splitLines)
		for sc.Scan() {
			onLine(stream, sc.Text())
		}
	}
	so, err := cmd.StdoutPipe()
	if err != nil {
		return -1, err
	}
	se, err := cmd.StderrPipe()
	if err != nil {
		return -1, err
	}
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	wg.Add(2)
	go pipe("stdout", so)
	go pipe("stderr", se)
	wg.Wait()
	err = cmd.Wait()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &ee):
		return ee.ExitCode(), nil
	}
	return -1, err
}

// splitLines splits on \n and \r (progress bars redraw with \r).
func splitLines(data []byte, atEOF bool) (int, []byte, error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		adv := i + 1
		if data[i] == '\r' && adv < len(data) && data[adv] == '\n' {
			adv++
		}
		return adv, data[:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}
