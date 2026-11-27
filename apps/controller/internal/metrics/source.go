package metrics

import "context"

// Snapshot is the pool's numbers at one moment. Durations are in seconds.
type Snapshot struct {
	// RunnersByState counts runners per state (for example idle, busy, offline).
	RunnersByState map[string]int
	// QueueWaitSeconds and JobDurationSeconds are the recent finished jobs.
	QueueWaitSeconds   []float64
	JobDurationSeconds []float64
	// FallbackTotal counts jobs sent to the hosted fallback since the controller started.
	FallbackTotal int64
	// CacheHits and CacheMisses count cache lookups since the controller started.
	CacheHits   int64
	CacheMisses int64
	// Machines lists each machine with its uptime.
	Machines []MachineUptime
}

// MachineUptime is one machine.
type MachineUptime struct {
	Name          string
	OS            string
	Online        bool
	UptimeSeconds float64
}

// Source gives the current numbers. The controller wires one that reads the
// other areas through their public interfaces.
type Source interface {
	Snapshot(ctx context.Context) (Snapshot, error)
}

// SourceFunc makes a Source of a function.
type SourceFunc func(ctx context.Context) (Snapshot, error)

// Snapshot implements Source.
func (f SourceFunc) Snapshot(ctx context.Context) (Snapshot, error) { return f(ctx) }

// Buckets are the upper bounds, in seconds, of the two duration histograms.
var Buckets = []float64{1, 5, 15, 30, 60, 120, 300, 600, 1800, 3600}

// Hist is a cumulative histogram.
type Hist struct {
	Bounds []float64
	Counts []uint64 // cumulative, one per bound
	Count  uint64
	Sum    float64
}

// NewHist builds the histogram of values over Buckets.
func NewHist(values []float64) Hist {
	h := Hist{Bounds: Buckets, Counts: make([]uint64, len(Buckets))}
	for _, v := range values {
		h.Count++
		h.Sum += v
		for i, b := range h.Bounds {
			if v <= b {
				h.Counts[i]++
			}
		}
	}
	return h
}

// CacheHitRate is hits over lookups, 0 to 1; zero when nothing was looked up.
func (s Snapshot) CacheHitRate() float64 {
	n := s.CacheHits + s.CacheMisses
	if n == 0 {
		return 0
	}
	return float64(s.CacheHits) / float64(n)
}
