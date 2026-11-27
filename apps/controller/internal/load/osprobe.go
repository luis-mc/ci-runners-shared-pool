package load

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// OSProbe is the real Probe.
type OSProbe struct{}

var _ Probe = OSProbe{}

// GOOS implements Probe.
func (OSProbe) GOOS() string { return runtime.GOOS }

// ReadFile implements Probe.
func (OSProbe) ReadFile(path string) (string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // fixed OS paths
	return string(b), err
}

// Exists implements Probe.
func (OSProbe) Exists(path string) bool { _, err := os.Stat(path); return err == nil }

// Run implements Probe. It runs argv with a 10 s limit and no shell.
func (OSProbe) Run(argv []string) (Result, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // fixed argv, no user input
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return Result{Code: ee.ExitCode(), Out: string(out)}, true
		}
		return Result{}, false
	}
	return Result{Out: string(out)}, true
}

// NumCPU implements Probe.
func (OSProbe) NumCPU() int { return runtime.NumCPU() }

// Now implements Probe.
func (OSProbe) Now() time.Time { return time.Now() }
