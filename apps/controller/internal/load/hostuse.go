package load

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The host's own use (machines R5.68, OPS61): memory used by everything except
// crsp's pool (the WSL VM, the pool's containers or VMs, crsp's own processes).
// It is sampled over time on the agent and kept as a rolling p95.
const (
	// HostUseWindow is how far back the p95 reaches.
	HostUseWindow = 7 * 24 * time.Hour
	// HostUseMinSpan is how long the samples must cover before the p95 counts.
	HostUseMinSpan = 24 * time.Hour
	// HostUseStep keeps one sample per step, so a week is at most 2016 points.
	HostUseStep = 5 * time.Minute
	// hostUseP is the percentile that counts.
	hostUseP = 0.95
)

// HostUseStats is what the tracker knows now.
type HostUseStats struct {
	// Ready is true when the samples cover HostUseMinSpan.
	Ready bool
	// P95Bytes is the p95 of the host's own use; 0 unless Ready.
	P95Bytes int64
	// Span is how long the samples cover, at most HostUseWindow.
	Span time.Duration
	// Samples is how many points the window holds.
	Samples int
}

type usePoint struct {
	At    time.Time `json:"at"`
	Bytes int64     `json:"bytes"`
}

// HostUse keeps the host-use samples. Add takes the time as an argument, so a
// test drives it with a fake clock and never sleeps.
type HostUse struct {
	mu  sync.Mutex
	pts []usePoint
}

// Add records the host's own use at now. Samples closer than HostUseStep to the
// last kept one replace its value only when larger, so a step keeps its peak;
// samples older than the window are dropped.
func (h *HostUse) Add(now time.Time, bytes int64) {
	if bytes < 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if n := len(h.pts); n > 0 && now.Sub(h.pts[n-1].At) < HostUseStep && !now.Before(h.pts[n-1].At) {
		if bytes > h.pts[n-1].Bytes {
			h.pts[n-1].Bytes = bytes
		}
	} else {
		h.pts = append(h.pts, usePoint{At: now, Bytes: bytes})
	}
	h.trim(now)
}

func (h *HostUse) trim(now time.Time) {
	i := 0
	for i < len(h.pts) && now.Sub(h.pts[i].At) > HostUseWindow {
		i++
	}
	if i > 0 {
		h.pts = append(h.pts[:0], h.pts[i:]...)
	}
}

// Stats is the rolling p95 over the window at now.
func (h *HostUse) Stats(now time.Time) HostUseStats {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.trim(now)
	if len(h.pts) == 0 {
		return HostUseStats{}
	}
	st := HostUseStats{Samples: len(h.pts), Span: now.Sub(h.pts[0].At)}
	if st.Span < 0 {
		st.Span = 0
	}
	if st.Span > HostUseWindow {
		st.Span = HostUseWindow
	}
	if st.Span < HostUseMinSpan {
		return st
	}
	vals := make([]float64, len(h.pts))
	for i, p := range h.pts {
		vals[i] = float64(p.Bytes)
	}
	sort.Float64s(vals)
	// Nearest rank: the smallest value at or above 95% of the samples.
	rank := int(math.Ceil(hostUseP*float64(len(vals)))) - 1
	if rank < 0 {
		rank = 0
	}
	st.Ready, st.P95Bytes = true, int64(vals[rank])
	return st
}

// Save writes the samples atomically so a restart keeps the week.
func (h *HostUse) Save(path string) error {
	if path == "" {
		return nil
	}
	h.mu.Lock()
	b, err := json.Marshal(h.pts)
	h.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Restore reads the samples saved by Save. A missing or broken file leaves the
// tracker empty; samples from the future (a clock set back) are dropped.
func (h *HostUse) Restore(path string, now time.Time) error {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path) //nolint:gosec // the agent's own data file
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var pts []usePoint
	if err := json.Unmarshal(b, &pts); err != nil {
		return err
	}
	kept := pts[:0]
	for _, p := range pts {
		if !p.At.After(now) && p.Bytes >= 0 {
			kept = append(kept, p)
		}
	}
	h.mu.Lock()
	h.pts = kept
	h.trim(now)
	h.mu.Unlock()
	return nil
}

// ownUse is the memory of crsp's own processes on Linux and macOS: `ps` lists
// resident KiB and the command, and every command named crsp* counts.
func ownUse(p Probe) int64 {
	switch p.GOOS() {
	case "windows":
		res, ok := p.Run([]string{"powershell", "-NoProfile", "-Command",
			"($p = Get-Process -Name crsp*,crsp-agent* -ErrorAction SilentlyContinue | Measure-Object WorkingSet64 -Sum).Sum"})
		if !ok || res.Code != 0 {
			return 0
		}
		return parseInt(res.Out)
	default:
		res, ok := p.Run([]string{"ps", "-axo", "rss=,comm="})
		if !ok || res.Code != 0 {
			return 0
		}
		return ParsePSOwn(res.Out)
	}
}

func parseInt(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// ParsePSOwn sums the resident memory of the commands named crsp* in the output
// of `ps -axo rss=,comm=` (KiB and a path or name per line).
func ParsePSOwn(out string) int64 {
	var total int64
	for _, ln := range strings.Split(out, "\n") {
		f := strings.Fields(ln)
		if len(f) < 2 {
			continue
		}
		kib, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil || kib < 0 {
			continue
		}
		if strings.HasPrefix(path.Base(strings.Join(f[1:], " ")), "crsp") {
			total += kib << 10
		}
	}
	return total
}

// hostOwnBytes is the host's own use for one sample: the used memory without
// what the pool holds. Windows takes off the vmmem working set (the host figure
// counts the VM at its working set); Linux and macOS take off the pool's
// containers. crsp's own processes come off everywhere.
func (s *Sampler) hostOwnBytes() (int64, bool) {
	used, _, ok := hostUsed(s.p)
	if !ok {
		return 0, false
	}
	e := s.extras
	pool := e.poolContainers
	if s.p.GOOS() == "windows" {
		pool = e.wsBytes
	}
	own := used - pool - e.ownBytes
	if own < 0 {
		own = 0
	}
	return own, true
}

// HostUse is the tracker the sampler feeds.
func (s *Sampler) HostUse() *HostUse { return &s.use }
