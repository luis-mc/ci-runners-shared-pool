// Package health reads the machine's own health for the agent: uptime, disk,
// swap, OOM kills, connectivity, versions, restart and update state, power,
// Docker disk use, temperature and load (machines R5.70 to R5.77, OPS83).
//
// A value an OS cannot give is reported as unknown with the reason, never as 0.
// Every collector reads through Env, so tests feed fixed answers. The cheap
// collectors run every minute inside a time budget; the slow ones (OS updates,
// Docker disk use, GitHub reachability, power) run off the hot path at their
// own pace and the report repeats their last answer.
package health

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Result is the outcome of a command.
type Result struct {
	Code int
	Out  string
}

// Env is every OS call the collectors make.
type Env interface {
	GOOS() string
	Now() time.Time
	ReadFile(path string) (string, error)
	WriteFile(path string, data []byte) error
	Exists(path string) bool
	// Glob lists the paths that match a pattern, sorted.
	Glob(pattern string) []string
	// Run runs argv; ok is false when the program cannot run at all.
	Run(argv []string) (res Result, ok bool)
	// RunTimeout is Run with its own time limit, for the slow commands.
	RunTimeout(argv []string, d time.Duration) (res Result, ok bool)
	// Disk is total and free bytes of the volume holding path.
	Disk(path string) (total, free int64, err error)
	// TickCount is the time since boot as the Windows tick counter gives it.
	TickCount() (time.Duration, error)
	// Head sends one HEAD request and returns the status code.
	Head(ctx context.Context, url string) (int, error)
	// LookupHost resolves one name.
	LookupHost(ctx context.Context, host string) error
}

// Base is what the agent's Sys gives; NewEnv adds the rest.
type Base interface {
	GOOS() string
	Now() time.Time
	ReadFile(path string) (string, error)
	Exists(path string) bool
	Run(argv []string) (res LoadResult, ok bool)
	Disk(path string) (total, free int64, err error)
}

// LoadResult has the shape of load.Result, so the agent adapts its Sys with a
// one-line function.
type LoadResult = Result

type osEnv struct{ Base }

// NewEnv returns the real Env over the agent's system calls.
func NewEnv(b Base) Env { return osEnv{b} }

func (osEnv) WriteFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (osEnv) Glob(pattern string) []string {
	m, _ := filepath.Glob(pattern)
	return m
}

func (osEnv) Head(ctx context.Context, url string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, http.NoBody)
	if err != nil {
		return 0, err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req) //nolint:gosec // the configured GitHub address
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

func (osEnv) LookupHost(ctx context.Context, host string) error {
	a, err := net.DefaultResolver.LookupHost(ctx, host)
	if err == nil && len(a) == 0 {
		return errors.New("no address")
	}
	return err
}

func (osEnv) RunTimeout(argv []string, d time.Duration) (Result, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).Output() //nolint:gosec // fixed argv, no user input
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return Result{Code: ee.ExitCode(), Out: string(out)}, true
		}
		return Result{}, false
	}
	return Result{Out: string(out)}, true
}

func (osEnv) TickCount() (time.Duration, error) { return tickCount() }
