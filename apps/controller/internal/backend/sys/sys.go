// Package sys holds the seams between a persistent backend and the machine:
// a command runner, a file system and a downloader. Backends call only these,
// so unit tests inject fakes and assert the exact calls; a test never creates
// an account or a service (runners R6.9).
package sys

import (
	"context"
	"errors"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Command is one program call. Args are passed one by one, never joined into a
// shell line (runners R5.18). Env carries values the program reads; SecretEnv
// carries values that must never appear in a plan, log or output (runners
// R5.15, R6.2). Stdin is a script text for PowerShell, or the JIT config of a container runner (it never goes in Args or Env).
type Command struct {
	Name      string
	Args      []string
	Env       map[string]string
	SecretEnv map[string]string
	Stdin     string
	// Dir is the working directory.
	Dir string
}

// String is the redacted description of the call, safe for a plan or log.
func (c Command) String() string {
	return c.Name + " " + strings.Join(c.Args, " ")
}

// Result is what a command gave back.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Commands runs programs.
type Commands interface {
	Run(ctx context.Context, c Command) (Result, error)
}

// StreamCommands is a Commands that can hand over output while the program
// runs (IMG1, R5.68). OnLine gets each line of stdout and stderr as it
// appears, without the line end. A runner that does not implement it is run
// whole and its output is read after the end.
type StreamCommands interface {
	Commands
	RunStream(ctx context.Context, c Command, onLine func(line string)) (Result, error)
}

// FS is the small file system the backends need.
type FS interface {
	MkdirAll(path string, perm fs.FileMode) error
	WriteFile(path string, data []byte, perm fs.FileMode) error
	ReadFile(path string) ([]byte, error)
	Exists(path string) bool
	RemoveAll(path string) error
}

// Fetcher downloads a URL to a file. The caller verifies the hash.
type Fetcher interface {
	Fetch(ctx context.Context, url, dest string) error
}

// Deps is what every persistent backend is built with.
type Deps struct {
	Cmd Commands
	FS  FS
	// Addresses are the external addresses (runner download base); the zero
	// value takes the github.com defaults.
	Addresses addresses.Set
	Fetch     Fetcher
	Verify    func(path, sha256 string) error
	Redact    func(string) string
	// Secret teaches Redact a one-time value (token, JIT config) before it is used (runners R5.11).
	Secret  func(string)
	LogLine func(step, msg string)
	// Sleep replaces the real wait while a listener ends (tests); nil is real.
	Sleep func(ctx context.Context, d time.Duration)
}

// PipeWaitDelay is how long Run still waits for the output pipes once the
// program has ended or been killed (OPS96). A variable so tests can shorten it.
var PipeWaitDelay = 10 * time.Second

// ExecCommands is the real command runner.
type ExecCommands struct{}

// Run runs the program with its arguments, never through a shell.
func (e ExecCommands) Run(ctx context.Context, c Command) (Result, error) {
	return e.RunStream(ctx, c, nil)
}

// lineWriter collects the text and calls onLine for each finished line. A
// carriage return also ends a line: a progress bar rewrites its line.
type lineWriter struct {
	mu     sync.Mutex
	sb     *strings.Builder
	part   strings.Builder
	onLine func(string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sb.Write(p)
	if w.onLine == nil {
		return len(p), nil
	}
	for _, c := range string(p) {
		if c == '\n' || c == '\r' {
			if w.part.Len() > 0 {
				w.onLine(w.part.String())
				w.part.Reset()
			}
			continue
		}
		w.part.WriteRune(c)
	}
	return len(p), nil
}

func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.onLine != nil && w.part.Len() > 0 {
		w.onLine(w.part.String())
		w.part.Reset()
	}
}

// RunStream is Run with a line callback (StreamCommands).
func (ExecCommands) RunStream(ctx context.Context, c Command, onLine func(string)) (Result, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...) //nolint:gosec // the backends build every call from fixed programs.
	cmd.Dir = c.Dir
	cmd.Env = os.Environ()
	for k, v := range c.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	for k, v := range c.SecretEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	if c.Stdin != "" {
		cmd.Stdin = strings.NewReader(c.Stdin)
	}
	var out, errb strings.Builder
	// One callback guard for both pipes: lines from the two never overlap.
	var cb sync.Mutex
	guarded := func(l string) {
		if onLine == nil {
			return
		}
		cb.Lock()
		defer cb.Unlock()
		onLine(l)
	}
	ow := &lineWriter{sb: &out, onLine: guarded}
	ew := &lineWriter{sb: &errb, onLine: guarded}
	cmd.Stdout, cmd.Stderr = ow, ew
	// A child the program started can inherit the output pipes and outlive
	// it; Wait would then block until that child ends, also after ctx is done
	// and the program was killed. WaitDelay bounds that wait (OPS96).
	cmd.WaitDelay = PipeWaitDelay
	err := cmd.Run()
	ow.flush()
	ew.flush()
	res := Result{Stdout: out.String(), Stderr: errb.String()}
	if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil {
		// The program itself ended; only a leftover child still held its
		// output. Its exit code is the result, with the output read so far.
		res.ExitCode = cmd.ProcessState.ExitCode()
		return res, nil
	}
	if ee, ok := err.(*exec.ExitError); ok {
		res.ExitCode = ee.ExitCode()
		return res, nil
	}
	return res, err
}

// OSFS is the real file system.
type OSFS struct{}

// MkdirAll implements FS.
func (OSFS) MkdirAll(p string, perm fs.FileMode) error { return os.MkdirAll(p, perm) }

// WriteFile implements FS.
func (OSFS) WriteFile(p string, d []byte, perm fs.FileMode) error { return os.WriteFile(p, d, perm) }

// ReadFile implements FS.
func (OSFS) ReadFile(p string) ([]byte, error) { return os.ReadFile(p) } //nolint:gosec // runner folders only.

// Exists implements FS.
func (OSFS) Exists(p string) bool { _, err := os.Stat(p); return err == nil }

// RemoveAll implements FS.
func (OSFS) RemoveAll(p string) error { return os.RemoveAll(p) }

// DirReader lists a directory; a file system that can is read for the logs of a
// runner folder (runners R5.152).
type DirReader interface {
	// ReadDir returns the names of the files and folders directly in dir.
	ReadDir(dir string) ([]string, error)
}

// ReadDir implements DirReader.
func (OSFS) ReadDir(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names, nil
}
