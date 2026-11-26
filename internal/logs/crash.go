package logs

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/redact"
)

// ReportedFile is the marker in the crash folder that lists the crash files
// already shown to the owner (R5.31).
const ReportedFile = "reported"

// Crasher writes crash files of one process into <data dir>/crash (R5.30).
type Crasher struct {
	Dir     string // <data dir>/crash
	Mode    string // controller, agent, cli or tray
	Version string
	R       *redact.Redactor // nil: shapes only
	Log     *slog.Logger     // optional; the crash is also logged
	Now     func() time.Time // nil: time.Now

	mu    sync.Mutex
	fatal *os.File
}

func (c *Crasher) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

func (c *Crasher) redactor() *redact.Redactor {
	if c.R == nil {
		return redact.New()
	}
	return c.R
}

// Recover runs fn and catches a panic at the top of it: the panic is written
// to a crash file and Recover returns true. It does not re-panic; the caller
// decides whether the process goes on. Use it as the first frame of every
// long-lived goroutine.
func (c *Crasher) Recover(fn func()) (panicked bool) {
	defer func() {
		if p := recover(); p != nil {
			panicked = true
			path, err := c.WriteCrash(fmt.Sprint(p), debug.Stack())
			if c.Log != nil {
				c.Log.Error("panic caught", "crash_file", path, "write_error", err)
			}
		}
	}()
	fn()
	return false
}

// Go starts fn in a goroutine under Recover.
func (c *Crasher) Go(fn func()) {
	go c.Recover(fn)
}

// WriteCrash writes crash-<UTC time>-<mode>.txt with the version, OS,
// architecture, mode, the redacted message and the redacted stack. It returns
// the path. The file is 0600 in a 0700 folder.
func (c *Crasher) WriteCrash(msg string, stack []byte) (string, error) {
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return "", err
	}
	r := c.redactor()
	var b strings.Builder
	fmt.Fprintf(&b, "version: %s\nos: %s\narch: %s\nmode: %s\ntime: %s\nmessage: %s\n\n%s\n",
		c.Version, runtime.GOOS, runtime.GOARCH, c.Mode, c.now().Format(time.RFC3339),
		r.Tagged(msg), r.Tagged(string(stack)))
	stamp := c.now().Format("20060102T150405Z")
	for n := 0; n < 100; n++ {
		name := fmt.Sprintf("crash-%s-%s.txt", stamp, c.Mode)
		if n > 0 {
			name = fmt.Sprintf("crash-%s-%s-%d.txt", stamp, c.Mode, n+1)
		}
		path := filepath.Join(c.Dir, name)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // the crash folder is built from the data folder
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, werr := f.WriteString(b.String())
		return path, errors.Join(werr, f.Close())
	}
	return "", errors.New("crash file name taken")
}

// InstallFatal sends a fatal runtime error (a crash Recover cannot catch) to a
// crash file in the same folder through runtime/debug.SetCrashOutput. The file
// is created now and removed by Close when nothing was written to it.
// The runtime writes the goroutine dump itself, so this file is not redacted;
// a stack prints argument words, never string contents.
func (c *Crasher) InstallFatal() error {
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(c.Dir, fmt.Sprintf("crash-%s-%s.txt", c.now().Format("20060102T150405Z"), c.Mode)),
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // the crash folder is built from the data folder
	if err != nil {
		return err
	}
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return err
	}
	c.mu.Lock()
	c.fatal = f
	c.mu.Unlock()
	return nil
}

// Close ends the fatal output on a clean exit and removes its file when empty.
func (c *Crasher) Close() error {
	c.mu.Lock()
	f := c.fatal
	c.fatal = nil
	c.mu.Unlock()
	if f == nil {
		return nil
	}
	_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
	st, serr := f.Stat()
	cerr := f.Close()
	if serr == nil && st.Size() == 0 {
		_ = os.Remove(f.Name())
	}
	return cerr
}

// Unreported lists the crash files (not empty) that are not in the reported
// marker, oldest first, as full paths.
func Unreported(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	done, err := reportedSet(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, "crash-") || !strings.HasSuffix(n, ".txt") || done[n] {
			continue
		}
		if info, err := e.Info(); err != nil || info.Size() == 0 {
			continue
		}
		out = append(out, filepath.Join(dir, n))
	}
	sort.Strings(out)
	return out, nil
}

func reportedSet(dir string) (map[string]bool, error) {
	set := map[string]bool{}
	f, err := os.Open(filepath.Join(dir, ReportedFile)) //nolint:gosec // fixed name in the crash folder
	if errors.Is(err, os.ErrNotExist) {
		return set, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if n := strings.TrimSpace(sc.Text()); n != "" {
			set[n] = true
		}
	}
	return set, sc.Err()
}

// MarkReported adds the base names of paths to the reported marker.
func MarkReported(dir string, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(dir, ReportedFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // fixed name in the crash folder
	if err != nil {
		return err
	}
	for _, p := range paths {
		if _, err := fmt.Fprintln(f, filepath.Base(p)); err != nil {
			_ = f.Close()
			return err
		}
	}
	return f.Close()
}

// ShowOnce is the start-up hook of the controller (R5.31): it hands the
// unreported crash files to show, which puts one Problem notice in the
// console, and marks them reported only when show returns nil. A notice that
// failed to show comes again at the next start. It returns how many were shown.
func ShowOnce(dir string, show func(files []string) error) (int, error) {
	files, err := Unreported(dir)
	if err != nil || len(files) == 0 {
		return 0, err
	}
	if err := show(files); err != nil {
		return 0, err
	}
	return len(files), MarkReported(dir, files...)
}
