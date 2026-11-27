package load

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Reading is one load figure.
type Reading struct {
	MemoryPct   float64   `json:"memory_pct"`
	MemoryKnown bool      `json:"memory_known"`
	CPUPct      float64   `json:"cpu_pct"`
	CPUKnown    bool      `json:"cpu_known"`
	At          time.Time `json:"at"`
	// Source is own, file or stale (R5.43).
	Source string `json:"source,omitempty"`
}

// Measurable is true when at least one figure is known (R5.22).
func (r Reading) Measurable() bool { return r.MemoryKnown || r.CPUKnown }

// Budget is a load budget in percent.
type Budget struct{ MemoryPct, CPUPct int }

// Over reports whether the reading is over budget: memory strictly above the
// memory budget or CPU strictly above the CPU budget, each exceeded budget
// named with its figures (R5.45). A figure that is not known never counts.
func Over(r Reading, b Budget) (bool, []string) {
	var parts []string
	if r.MemoryKnown && r.MemoryPct > float64(b.MemoryPct) {
		parts = append(parts, fmt.Sprintf("memory %.0f%% > %d%%", r.MemoryPct, b.MemoryPct))
	}
	if r.CPUKnown && r.CPUPct > float64(b.CPUPct) {
		parts = append(parts, fmt.Sprintf("CPU %.0f%% > %d%%", r.CPUPct, b.CPUPct))
	}
	return len(parts) > 0, parts
}

// Probe reads the raw figures of the operating system. A probe never sleeps.
type Probe interface {
	Memory() (pct float64, ok bool)
	CPURaw() (Raw, bool)
	// LoadAvgPerCore is the 1-minute load average per core in percent.
	LoadAvgPerCore() (pct float64, ok bool)
}

// Timing constants (R5.41, R5.42, R5.43).
const (
	CacheTTL      = 10 * time.Second
	HistoryWindow = 30 * time.Second
	HistoryMax    = 3
	RawMaxAge     = 10 * time.Minute
	HostMaxAge    = 60 * time.Second
)

type cacheFile struct {
	Last    Reading   `json:"last"`
	Raw     Raw       `json:"raw"`
	RawAt   time.Time `json:"raw_at"`
	History []Reading `json:"history"`
}

// Sampler takes samples with a small cache file. The cache is per user, never
// in a shared temp folder; one that cannot be read or written is ignored (R5.42).
type Sampler struct {
	Probe     Probe
	CachePath string
	Now       func() time.Time
}

// DefaultCachePath is the per-user cache file.
func DefaultCachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "crsp", "load.json")
}

func (s *Sampler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Sample returns the load now. A sample younger than 10 s is reused, so a
// burst of hooks costs one measurement. CPU is the mean of the known CPU
// values of up to 3 samples from the last 30 s (unknown counts as 0); memory
// is the latest (R5.42). With no previous raw sample younger than 10
// minutes, CPU is the load average per core; with neither it is unknown (R5.41).
func (s *Sampler) Sample() Reading {
	now := s.now()
	c := s.readCache()
	if c != nil && now.Sub(c.Last.At) >= 0 && now.Sub(c.Last.At) < CacheTTL {
		r := c.Last
		r.Source = "own"
		return r
	}
	cur := Reading{At: now, Source: "own"}
	cur.MemoryPct, cur.MemoryKnown = s.Probe.Memory()
	raw, rawOK := s.Probe.CPURaw()
	if rawOK && c != nil && now.Sub(c.RawAt) < RawMaxAge {
		cur.CPUPct, cur.CPUKnown = CPUBetween(c.Raw, raw)
	}
	if !cur.CPUKnown {
		cur.CPUPct, cur.CPUKnown = s.Probe.LoadAvgPerCore()
	}
	next := cacheFile{Last: cur, Raw: raw, RawAt: now}
	if !rawOK {
		next.RawAt = time.Time{}
	}
	var hist []Reading
	if c != nil {
		hist = c.History
	}
	hist = append(hist, cur)
	var keep []Reading
	for _, h := range hist {
		if d := now.Sub(h.At); d >= 0 && d <= HistoryWindow {
			keep = append(keep, h)
		}
	}
	if len(keep) > HistoryMax {
		keep = keep[len(keep)-HistoryMax:]
	}
	next.History = keep
	out := cur
	out.CPUPct, out.CPUKnown = smoothCPU(keep)
	next.Last = out
	s.writeCache(next)
	return out
}

func smoothCPU(h []Reading) (float64, bool) {
	var sum float64
	known := false
	for _, r := range h {
		if r.CPUKnown {
			sum += r.CPUPct
			known = true
		}
	}
	if !known || len(h) == 0 {
		return 0, false
	}
	return round1(sum / float64(len(h))), true
}

func (s *Sampler) readCache() *cacheFile {
	if s.CachePath == "" {
		return nil
	}
	b, err := os.ReadFile(s.CachePath)
	if err != nil {
		return nil
	}
	var c cacheFile
	if json.Unmarshal(b, &c) != nil {
		return nil
	}
	return &c
}

// writeCache writes through a temp file and a rename, and leaves no temp file
// behind; any error is ignored.
func (s *Sampler) writeCache(c cacheFile) {
	if s.CachePath == "" {
		return
	}
	b, err := json.Marshal(c)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.CachePath), 0o700); err != nil {
		return
	}
	f, err := os.CreateTemp(filepath.Dir(s.CachePath), "load-*.tmp")
	if err != nil {
		return
	}
	tmp := f.Name()
	_, werr := f.Write(b)
	cerr := f.Close()
	if werr != nil || cerr != nil || os.Rename(tmp, s.CachePath) != nil {
		_ = os.Remove(tmp)
	}
}

// WriteHost publishes this machine's load for readers in a VM or container (R5.43).
func WriteHost(path string, r Reading) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Combine picks the published figure when it is at most 60 s old and the
// reader's own figure otherwise, marking the source file, own or stale (R5.43).
// A published figure that is too old is marked stale only when there is no own figure.
func Combine(path string, own Reading, now time.Time) Reading {
	b, err := os.ReadFile(path)
	if err != nil {
		own.Source = "own"
		return own
	}
	var h Reading
	if json.Unmarshal(b, &h) != nil || !h.Measurable() {
		own.Source = "own"
		return own
	}
	if age := now.Sub(h.At); age >= 0 && age <= HostMaxAge {
		h.Source = "file"
		return h
	}
	if own.Measurable() {
		own.Source = "own"
		return own
	}
	h.Source = "stale"
	return h
}
