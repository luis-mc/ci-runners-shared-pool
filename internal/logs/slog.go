// Package logs is the local logging of every crsp process (observability R5.29
// to R5.31, R6.5, R6.7): one slog setup, size rotation in the data folder,
// crash files, and the marker of the crash files already shown. Nothing here
// touches the network: logs stay on the machine (R6.7).
package logs

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/luis-mc/ci-runners-shared-pool/internal/logging"
	"github.com/luis-mc/ci-runners-shared-pool/internal/redact"
)

// Rotation defaults: a file is closed at 10 MiB and 5 files are kept.
const (
	DefaultMaxSize  int64 = 10 << 20
	DefaultMaxFiles       = 5
)

// Options says how a process logs.
type Options struct {
	Dir       string // the logs folder in the data folder
	Mode      string // controller, agent, cli or tray; also the file name
	Version   string
	Component string // defaults to Mode
	// Foreground also writes readable lines to Stderr.
	Foreground bool
	Stderr     io.Writer // defaults to os.Stderr
	Level      slog.Leveler
	MaxSize    int64
	MaxFiles   int
	Redactor   *redact.Redactor
	// Extra handlers get every record after the redactor, like the file: the agent adds
	// NewAgentHandler here so its lines go to the controller (R5.20).
	Extra []slog.Handler
}

// IDs are the ids a log line carries when it has them (R5.29).
type IDs struct{ Machine, Runner, Job, Request string }

// With returns l with the non-empty ids as attributes.
func With(l *slog.Logger, ids IDs) *slog.Logger {
	var args []any
	for _, kv := range [][2]string{{"machine", ids.Machine}, {"runner", ids.Runner}, {"job", ids.Job}, {"request", ids.Request}} {
		if kv[1] != "" {
			args = append(args, kv[0], kv[1])
		}
	}
	if len(args) == 0 {
		return l
	}
	return l.With(args...)
}

// Setup opens <Dir>/<Mode>.log and returns the logger and a closer for the
// file. The chain is: redactor, then the rotating JSON file and, in the
// foreground, readable lines on stderr. When the file cannot open, the logger
// still works on stderr and the error is returned with it, so a log problem
// never stops a process.
func Setup(o Options) (*slog.Logger, io.Closer, error) {
	if o.Component == "" {
		o.Component = o.Mode
	}
	if o.Level == nil {
		o.Level = slog.LevelInfo
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	var handlers []slog.Handler
	var closer io.Closer = nopCloser{}
	var openErr error
	rw, err := OpenRotating(filepath.Join(o.Dir, o.Mode+".log"), o.MaxSize, o.MaxFiles)
	if err != nil {
		openErr = err
		handlers = append(handlers, slog.NewTextHandler(o.Stderr, &slog.HandlerOptions{Level: o.Level}))
	} else {
		closer = rw
		handlers = append(handlers, slog.NewJSONHandler(rw, &slog.HandlerOptions{Level: o.Level}))
		if o.Foreground {
			handlers = append(handlers, slog.NewTextHandler(o.Stderr, &slog.HandlerOptions{Level: o.Level}))
		}
	}
	handlers = append(handlers, o.Extra...)
	l := slog.New(logging.NewTaggedHandler(fanout(handlers), o.Redactor)).
		With("component", o.Component, "version", o.Version, "mode", o.Mode)
	return l, closer, openErr
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// Rotating is a file that rolls over by size: <name>, <name>.1 (newest
// rolled), up to MaxFiles-1 rolled files; the oldest is deleted.
type Rotating struct {
	mu       sync.Mutex
	path     string
	f        *os.File
	size     int64
	maxSize  int64
	maxFiles int
}

// OpenRotating opens (or creates) path for appending, mode 0600 in a 0700 folder.
func OpenRotating(path string, maxSize int64, maxFiles int) (*Rotating, error) {
	if maxSize <= 0 {
		maxSize = DefaultMaxSize
	}
	if maxFiles < 1 {
		maxFiles = DefaultMaxFiles
	}
	r := &Rotating{path: path, maxSize: maxSize, maxFiles: maxFiles}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Rotating) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // the log path is built from the data folder
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	r.f, r.size = f, st.Size()
	return nil
}

// Write implements io.Writer; it rolls the file first when p would pass the size.
func (r *Rotating) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return 0, os.ErrClosed
	}
	if r.size > 0 && r.size+int64(len(p)) > r.maxSize {
		if err := r.roll(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *Rotating) roll() error {
	if err := r.f.Close(); err != nil {
		return err
	}
	r.f = nil
	_ = os.Remove(fmt.Sprintf("%s.%d", r.path, r.maxFiles-1))
	for i := r.maxFiles - 2; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", r.path, i), fmt.Sprintf("%s.%d", r.path, i+1))
	}
	if r.maxFiles > 1 {
		_ = os.Rename(r.path, r.path+".1")
	} else {
		_ = os.Remove(r.path)
	}
	return r.open()
}

// Close closes the file.
func (r *Rotating) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}
