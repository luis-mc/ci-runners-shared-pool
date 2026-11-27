package logs

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Buffer is the agent's disk buffer for log lines while it cannot reach the
// controller (R5.23, R6.8: 64 MiB, floor 8 MiB, ceiling 1 GiB). Lines are kept
// in order in segment files; when the buffer is over its cap the oldest segment
// goes first, and the count of lines lost is kept so that one line can say how
// many were dropped. Lines in it are already redacted (R6.5).
type Buffer struct {
	dir     string
	cap     int64
	segSize int64

	mu      sync.Mutex
	segs    []*segment // oldest first
	next    int
	dropped int64
}

type segment struct {
	name   string
	size   int64
	lines  int64
	sealed bool
}

const (
	segPrefix   = "seg-"
	segSuffix   = ".jsonl"
	droppedFile = "dropped"
)

// OpenBuffer opens (creating) the buffer in dir with a cap of capMiB, which
// must be inside AgentBufferMiB.
func OpenBuffer(dir string, capMiB int) (*Buffer, error) {
	if err := AgentBufferMiB.Check("agent log buffer", int64(capMiB), "MiB"); err != nil {
		return nil, err
	}
	return openBuffer(dir, int64(capMiB)<<20)
}

func openBuffer(dir string, capBytes int64) (*Buffer, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	b := &Buffer{dir: dir, cap: capBytes, segSize: max(capBytes/8, 1)}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, segPrefix) || !strings.HasSuffix(n, segSuffix) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return nil, err
		}
		b.segs = append(b.segs, &segment{name: n, size: int64(len(data)), lines: int64(bytes.Count(data, []byte("\n")))})
		if id, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(n, segPrefix), segSuffix)); err == nil && id >= b.next {
			b.next = id + 1
		}
	}
	sort.Slice(b.segs, func(i, j int) bool { return b.segs[i].name < b.segs[j].name })
	if data, err := os.ReadFile(filepath.Join(dir, droppedFile)); err == nil {
		b.dropped, _ = strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	}
	return b, nil
}

type diskLine struct {
	At        int64      `json:"t"`
	Level     slog.Level `json:"l"`
	Machine   string     `json:"m,omitempty"`
	Runner    string     `json:"r,omitempty"`
	Job       string     `json:"j,omitempty"`
	Component string     `json:"c,omitempty"`
	Message   string     `json:"msg"`
	Attrs     string     `json:"a,omitempty"`
}

// Append adds one line at the end. When the buffer is over its cap the oldest
// segments are deleted and counted as dropped.
func (b *Buffer) Append(l Line) error {
	data, err := json.Marshal(diskLine{l.At.UnixMilli(), l.Level, l.Machine, l.Runner, l.Job, l.Component, l.Message, l.Attrs})
	if err != nil {
		return err
	}
	data = append(data, '\n')
	b.mu.Lock()
	defer b.mu.Unlock()
	if n := len(b.segs); n == 0 || b.segs[n-1].sealed || b.segs[n-1].size+int64(len(data)) > b.segSize && b.segs[n-1].lines > 0 {
		b.segs = append(b.segs, &segment{name: fmt.Sprintf("%s%08d%s", segPrefix, b.next, segSuffix)})
		b.next++
	}
	s := b.segs[len(b.segs)-1]
	f, err := os.OpenFile(filepath.Join(b.dir, s.name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}
	s.size += int64(len(data))
	s.lines++
	return b.trim()
}

// trim drops the oldest segments while the buffer is over its cap; the segment
// being written is never dropped by the line that fills it.
func (b *Buffer) trim() error {
	var total int64
	for _, s := range b.segs {
		total += s.size
	}
	changed := false
	for total > b.cap && len(b.segs) > 1 && !b.segs[0].sealed { // a segment being sent is not dropped
		old := b.segs[0]
		if err := os.Remove(filepath.Join(b.dir, old.name)); err != nil && !os.IsNotExist(err) {
			return err
		}
		total -= old.size
		b.dropped += old.lines
		b.segs = b.segs[1:]
		changed = true
	}
	if changed {
		return os.WriteFile(filepath.Join(b.dir, droppedFile), []byte(strconv.FormatInt(b.dropped, 10)), 0o600)
	}
	return nil
}

// Dropped is the number of lines lost since the last Take was acknowledged.
func (b *Buffer) Dropped() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped
}

// Take returns the buffered lines in order. When lines were dropped, the first
// line says how many ("dropped N log lines: the agent buffer was full"). Ack
// removes exactly what was taken; call it after the controller got the lines.
// Lines appended after Take stay for the next one.
func (b *Buffer) Take(now time.Time) (lines []Line, dropped int64, ack func() error, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	taken := append([]*segment(nil), b.segs...)
	dropped = b.dropped
	for _, s := range taken {
		s.sealed = true // lines appended from now on go to a new segment, so Ack never removes them
	}
	for _, s := range taken {
		f, err := os.Open(filepath.Join(b.dir, s.name))
		if err != nil {
			return nil, 0, nil, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
		for sc.Scan() {
			var d diskLine
			if json.Unmarshal(sc.Bytes(), &d) != nil {
				continue // a torn last line after a crash
			}
			lines = append(lines, Line{At: time.UnixMilli(d.At).UTC(), Level: d.Level, Machine: d.Machine, Runner: d.Runner, Job: d.Job, Component: d.Component, Message: d.Message, Attrs: d.Attrs})
		}
		_ = f.Close()
	}
	if dropped > 0 {
		at := now
		if len(lines) > 0 {
			at = lines[0].At
		}
		note := Line{At: at, Level: slog.LevelWarn, Component: "logs", Message: fmt.Sprintf("dropped %d log lines: the agent buffer was full", dropped)}
		lines = append([]Line{note}, lines...)
	}
	ack = func() error {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, s := range taken {
			if err := os.Remove(filepath.Join(b.dir, s.name)); err != nil && !os.IsNotExist(err) {
				return err
			}
			for i, c := range b.segs {
				if c == s {
					b.segs = append(b.segs[:i], b.segs[i+1:]...)
					break
				}
			}
		}
		b.dropped -= dropped
		_ = os.Remove(filepath.Join(b.dir, droppedFile))
		if b.dropped > 0 {
			return os.WriteFile(filepath.Join(b.dir, droppedFile), []byte(strconv.FormatInt(b.dropped, 10)), 0o600)
		}
		return nil
	}
	return lines, dropped, ack, nil
}
