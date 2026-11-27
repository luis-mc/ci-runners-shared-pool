// Package dockercompact is the `docker-compact` host helper (caches R5.51 to R5.54,
// machines R5.66). On Windows, Docker Desktop keeps its data in a WSL2 disk
// file (a .vhdx) that grows and never shrinks. The helper trims it, stops
// Docker Desktop, shuts WSL down, compacts the file and starts Docker Desktop
// again. On Linux and macOS it prunes only what Docker no longer uses.
//
// Every OS call goes through Host, so tests feed a fake and never run the real
// compact. The helper always tries to bring Docker back, also after a failure.
package dockercompact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Command is the name the controller sends as the first argument of the host
// command (cli R5.29).
const Command = "docker-compact"

// Timeouts of the steps. A step that passes its limit is cut and counts as failed.
const (
	TrimTimeout    = 10 * time.Minute
	StopTimeout    = 3 * time.Minute
	CompactTimeout = 90 * time.Minute
	StartTimeout   = 2 * time.Minute
	// ReadyTimeout is how long `docker info` may take to answer after the start.
	ReadyTimeout = 5 * time.Minute
	PollEvery    = 5 * time.Second
	probeTimeout = 30 * time.Second
	pruneTimeout = 30 * time.Minute
)

// ErrNotElevated is returned before anything is stopped: the compact needs
// administrator rights.
var ErrNotElevated = errors.New("docker-compact needs the agent to run as administrator; nothing was stopped")

// Result is what one Host call gave.
type Result struct {
	Out      string
	ExitCode int
}

// Host is every OS call the helper makes.
type Host interface {
	GOOS() string
	// Elevated says whether the process has administrator rights.
	Elevated() bool
	// Run runs a program directly (no shell) with a time limit. A non-zero exit
	// is reported in Result.ExitCode with a nil error; err is for a program that
	// could not start or ran past ctx.
	Run(ctx context.Context, timeout time.Duration, name string, args ...string) (Result, error)
	// FileSize is the size on disk of a file; ok is false when it does not exist.
	FileSize(path string) (size int64, ok bool)
	// Getenv reads an environment variable.
	Getenv(key string) string
	// ReadFile reads a small text file.
	ReadFile(path string) (string, error)
	// WriteTemp writes a text file in the temporary folder and returns its path.
	WriteTemp(pattern, content string) (string, error)
	Remove(path string)
	Sleep(ctx context.Context, d time.Duration) error
}

// DiskFile is one WSL2 disk file with its size before and after.
type DiskFile struct {
	Path   string `json:"path"`
	Before int64  `json:"before_bytes"`
	After  int64  `json:"after_bytes"`
}

// Report is what the helper did. It is printed as text and as one JSON line
// (`RESULT {...}`), which the controller reads for its notices.
type Report struct {
	Platform string     `json:"platform"`
	Files    []DiskFile `json:"files,omitempty"`
	// UsedBefore and UsedAfter are the bytes Docker uses inside the disk (0 when
	// not known).
	UsedBefore int64 `json:"used_before_bytes"`
	UsedAfter  int64 `json:"used_after_bytes"`
	// Steps names the steps in the order they ran.
	Steps []string `json:"steps"`
	// Error is empty when everything worked.
	Error string `json:"error,omitempty"`
	// KVMBefore and KVMAfter say whether /dev/kvm was in the Docker VM before
	// the compact and again after the restart (caches R5.58).
	KVMBefore bool `json:"kvm_before"`
	KVMAfter  bool `json:"kvm_after"`
	// DockerRunning says whether `docker info` answered at the end.
	DockerRunning bool `json:"docker_running"`
	lines         []string
}

// FileBefore and FileAfter are the sums over the disk files.
func (r Report) FileBefore() (n int64) {
	for _, f := range r.Files {
		n += f.Before
	}
	return n
}

// FileAfter is the sum of the sizes after the compact.
func (r Report) FileAfter() (n int64) {
	for _, f := range r.Files {
		n += f.After
	}
	return n
}

// Text is the output of the helper: the lines it wrote and the RESULT line.
func (r Report) Text() string {
	b, err := json.Marshal(r)
	if err != nil {
		b = []byte("{}")
	}
	return strings.Join(r.lines, "\n") + "\nRESULT " + string(b) + "\n"
}

// ParseReport reads the RESULT line of an output. ok is false when there is none.
func ParseReport(output string) (Report, bool) {
	for _, ln := range strings.Split(output, "\n") {
		ln = strings.TrimRight(ln, "\r")
		if rest, found := strings.CutPrefix(ln, "RESULT "); found {
			var r Report
			if json.Unmarshal([]byte(rest), &r) == nil {
				return r, true
			}
		}
	}
	return Report{}, false
}

type run struct {
	h Host
	r *Report
}

func (c *run) say(format string, a ...any) { c.r.lines = append(c.r.lines, fmt.Sprintf(format, a...)) }
func (c *run) step(name string)            { c.r.Steps = append(c.r.Steps, name); c.say("step: %s", name) }

// exec runs one program and turns a non-zero exit into an error.
func (c *run) exec(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	res, err := c.h.Run(ctx, timeout, name, args...)
	out := normalize(res.Out)
	if err != nil {
		return out, fmt.Errorf("%s: %w", name, err)
	}
	if res.ExitCode != 0 {
		return out, fmt.Errorf("%s exited with code %d: %s", name, res.ExitCode, lastLine(out))
	}
	return out, nil
}

// Do runs the helper. It always returns a report; the error is non-nil when the
// helper failed. Docker is started again before it returns, whatever failed.
func Do(ctx context.Context, h Host) (Report, error) {
	c := &run{h: h, r: &Report{Platform: h.GOOS()}}
	var err error
	if h.GOOS() == "windows" {
		err = c.windows(ctx)
	} else {
		err = c.prune(ctx)
	}
	if err != nil {
		c.r.Error = err.Error()
		c.say("failed: %v", err)
	}
	return *c.r, err
}

// Inspect reads the disk files and what Docker uses, with no change. It is the
// report the agent sends so the controller can decide (machines R5.66).
type Inspection struct {
	Files     []DiskFile
	UsedBytes int64
	// UsedKnown is false when Docker did not answer.
	UsedKnown bool
	Elevated  bool
	// Reclaimable is the file size minus what Docker uses; 0 when not known.
}

// Reclaimable is the size of the disk files minus what Docker uses inside.
func (i Inspection) Reclaimable() int64 {
	if !i.UsedKnown {
		return 0
	}
	var total int64
	for _, f := range i.Files {
		total += f.Before
	}
	if total <= i.UsedBytes {
		return 0
	}
	return total - i.UsedBytes
}

// Inspect finds the disk files (Windows only) and reads the used size. It
// never stops or starts anything.
func Inspect(ctx context.Context, h Host) Inspection {
	in := Inspection{Elevated: h.Elevated()}
	if h.GOOS() != "windows" {
		return in
	}
	c := &run{h: h, r: &Report{}}
	for _, p := range c.findDisks(ctx) {
		if size, ok := h.FileSize(p); ok {
			in.Files = append(in.Files, DiskFile{Path: p, Before: size, After: size})
		}
	}
	if len(in.Files) > 0 {
		in.UsedBytes, in.UsedKnown = c.usedInside(ctx)
	}
	return in
}

func normalize(s string) string {
	return strings.ToValidUTF8(strings.ReplaceAll(strings.ReplaceAll(s, "\x00", ""), "\r\n", "\n"), "?")
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
