// Package jobsize is the shared sizing logic of measured job peaks (docs/features/observability R5.60 to
// R5.64, OPS66): from the samples of one job run it makes the run's peaks and flags, and from the runs of one
// job key it makes the summary that later sizing work reads. It is pure Go with no OS call and no clock, so
// the agent and the controller both use it and a test needs no sleep.
package jobsize

import (
	"sort"
	"time"
)

// Sizing constants (R5.63).
const (
	// MinRuns is the number of good runs from which a job key counts as measured: the same 5-run rule as the
	// time forecast (observability R5.41).
	MinRuns = 5
	// KeepRuns is how many of the newest good runs the summary of a job key reads.
	KeepRuns = 30
	// CPUPercentile is the percentile of the per-window CPU that is called the peak CPU. The maximum of a 1 s
	// window is one noisy reading; p95 is the load the job keeps up.
	CPUPercentile = 95
	// SummaryPercentile is the percentile over runs of a job key.
	SummaryPercentile = 90
	// MinGoodSamples is how many known samples a run needs to be good.
	MinGoodSamples = 2
)

// Sample is one window: CPU cores used over it, times 1000, and the resident memory at its end.
type Sample struct {
	At          time.Time
	CPUMillis   int64
	MemoryBytes int64
	Known       bool
}

// Run is what is known of one job run besides its samples.
type Run struct {
	// MemoryPeakBytes is the peak the system kept for the whole job (cgroup memory.peak, Job Object
	// PeakJobMemoryUsed); 0 when it keeps none.
	MemoryPeakBytes int64
	OOMKilled       bool
	CutShort        bool
	UnknownReason   string
}

// Measurement is the peaks of one job run (R5.60).
type Measurement struct {
	// PeakCPUMillis is the p95 over the windows, in cores times 1000.
	PeakCPUMillis int64
	// PeakMemoryBytes is the largest of the sampled memory and the peak the system kept.
	PeakMemoryBytes int64
	Samples         int
	DurationMS      int64
	OOMKilled       bool
	CutShort        bool
	UnknownReason   string
}

// Good says whether the run may feed a job key's summary: measured, not killed, not cut short (R5.63).
func (m Measurement) Good() bool {
	return m.Samples >= MinGoodSamples && !m.OOMKilled && !m.CutShort
}

// Measure makes the peaks of one run. Only known samples count. The first known sample has no earlier
// reading to take a CPU difference from, so its CPU is left out of the percentile when there are others. With
// no known sample the peaks are 0 and UnknownReason says why (the agent's reason, or "no samples").
func Measure(samples []Sample, r Run) Measurement {
	m := Measurement{OOMKilled: r.OOMKilled, CutShort: r.CutShort}
	var cpu []int64
	var first, last time.Time
	for _, s := range samples {
		if !s.Known {
			continue
		}
		if m.Samples > 0 {
			cpu = append(cpu, s.CPUMillis)
		} else {
			first = s.At
		}
		last = s.At
		m.Samples++
		if s.MemoryBytes > m.PeakMemoryBytes {
			m.PeakMemoryBytes = s.MemoryBytes
		}
	}
	if m.Samples == 0 {
		m.UnknownReason = r.UnknownReason
		if m.UnknownReason == "" {
			m.UnknownReason = "no samples"
		}
		return m
	}
	if len(cpu) > 0 {
		m.PeakCPUMillis = Percentile(cpu, CPUPercentile)
	}
	if r.MemoryPeakBytes > m.PeakMemoryBytes {
		m.PeakMemoryBytes = r.MemoryPeakBytes
	}
	if d := last.Sub(first); d > 0 {
		m.DurationMS = d.Milliseconds()
	}
	return m
}

// Peak is the stored peaks of one past run of a job key.
type Peak struct {
	CPUMillis   int64
	MemoryBytes int64
	Good        bool
}

// Summary is the summary of one job key (R5.63).
type Summary struct {
	// Runs is how many good runs the summary reads: at most KeepRuns.
	Runs int
	// CPUP90Millis and MemoryP90Bytes are the p90 over those runs of the run peaks.
	CPUP90Millis   int64
	MemoryP90Bytes int64
	// Measured is true from MinRuns good runs on. Below it the numbers are shown as "not enough runs yet" and
	// size nothing.
	Measured bool
}

// Summarise reads the runs newest first. Runs that are not good are skipped, and only the first KeepRuns
// good ones count.
func Summarise(newestFirst []Peak) Summary {
	var cpu, mem []int64
	for _, p := range newestFirst {
		if !p.Good {
			continue
		}
		cpu, mem = append(cpu, p.CPUMillis), append(mem, p.MemoryBytes)
		if len(cpu) == KeepRuns {
			break
		}
	}
	s := Summary{Runs: len(cpu), Measured: len(cpu) >= MinRuns}
	if len(cpu) == 0 {
		return s
	}
	s.CPUP90Millis, s.MemoryP90Bytes = Percentile(cpu, SummaryPercentile), Percentile(mem, SummaryPercentile)
	return s
}

// Percentile is the nearest-rank percentile of v; p in 1..100. An empty v gives 0.
func Percentile(v []int64, p int) int64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]int64(nil), v...)
	sort.Slice(s, func(a, b int) bool { return s[a] < s[b] })
	i := (p*len(s)+99)/100 - 1
	if i < 0 {
		i = 0
	}
	if i >= len(s) {
		i = len(s) - 1
	}
	return s[i]
}
