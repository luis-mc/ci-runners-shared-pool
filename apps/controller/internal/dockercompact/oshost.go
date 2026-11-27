package dockercompact

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// OSHost is the real Host. Tests never use it.
type OSHost struct{}

var _ Host = OSHost{}

// GOOS implements Host.
func (OSHost) GOOS() string { return runtime.GOOS }

// Elevated implements Host.
func (OSHost) Elevated() bool { return elevated() }

// Run implements Host: the program starts without a shell and is stopped at
// the limit.
func (OSHost) Run(ctx context.Context, timeout time.Duration, name string, args ...string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // fixed program names of the helper; no shell
	out, err := cmd.CombinedOutput()
	res := Result{Out: string(out)}
	var ee *exec.ExitError
	switch {
	case err == nil:
		return res, nil
	case errors.As(err, &ee):
		res.ExitCode = ee.ExitCode()
		if res.ExitCode == 0 {
			res.ExitCode = -1
		}
		if ctx.Err() != nil {
			return res, errors.New("stopped at the time limit")
		}
		return res, nil
	default:
		return res, err
	}
}

// FileSize implements Host.
func (OSHost) FileSize(path string) (int64, bool) {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return 0, false
	}
	return st.Size(), true
}

// Getenv implements Host.
func (OSHost) Getenv(key string) string { return os.Getenv(key) }

// ReadFile implements Host.
func (OSHost) ReadFile(path string) (string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // Docker Desktop's settings file
	return string(b), err
}

// WriteTemp implements Host.
func (OSHost) WriteTemp(pattern, content string) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	_, werr := f.WriteString(content)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// Remove implements Host.
func (OSHost) Remove(path string) { _ = os.Remove(path) }

// Sleep implements Host.
func (OSHost) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
