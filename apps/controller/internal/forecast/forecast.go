package forecast

import (
	"math"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

// Method constants.
const (
	MinSamples = 5
	Window     = 30
	MaxAge     = 90 * 24 * time.Hour

	// DriftUp and DriftDown bound the ratio of the last 10 medians to the 20 before.
	DriftUp   = 1.25
	DriftDown = 0.8
	// MinSpread is the least distance of the range from the median, as a share of the median.
	MinSpread = 0.1
	// MinRemaining is the floor of the remaining time of a running job that is past its forecast, in seconds.
	MinRemaining = 5.0

	driftMinSamples = 15
	driftRecent     = 10
)

// Basis says which history a forecast was made from.
type Basis string

// Bases.
const (
	BasisJobKey     Basis = "job_key"
	BasisWorkflow   Basis = "workflow"
	BasisRepository Basis = "repository"
	BasisNone       Basis = "not_enough_history"
)

// Forecast is a duration forecast in seconds. Lo and Hi are the range shown; P25 and P75 feed run ETAs.
type Forecast struct {
	P50, P25, P75, Lo, Hi float64
	Basis                 Basis
	Samples               int
	Machine               string // set when only samples of this machine were used
	ChangedLately         bool
}

// Eta is a time from now in seconds with its range.
type Eta struct{ P50, Lo, Hi float64 }

func round1(n float64) float64 { return math.Round(n*10) / 10 }

// Percentile is the p-th percentile (0..1) by linear interpolation. Empty input gives 0.
func Percentile(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	at := math.Min(math.Max(p, 0), 1) * float64(len(s)-1)
	lo, hi := math.Floor(at), math.Ceil(at)
	a, b := s[int(lo)], s[int(hi)]
	return a + (b-a)*(at-lo)
}

// NearestRank is the smallest value with at least p (0..1) of the sample at or below it. Empty input gives 0.
func NearestRank(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	i := int(math.Ceil(p*float64(len(s)))) - 1
	return s[min(len(s)-1, max(0, i))]
}

// Median is the 0.5 percentile.
func Median(v []float64) float64 { return Percentile(v, 0.5) }

func noHistory(samples int) Forecast { return Forecast{Basis: BasisNone, Samples: samples} }

// FromSeconds forecasts from sample durations, oldest first (only the newest Window are used). Under MinSamples it
// returns the BasisNone forecast with the sample count. machine is recorded when not empty.
func FromSeconds(oldestFirst []float64, basis Basis, machine string) Forecast {
	seconds := oldestFirst
	if len(seconds) > Window {
		seconds = seconds[len(seconds)-Window:]
	}
	if len(seconds) < MinSamples {
		return noHistory(len(seconds))
	}
	use, changed := seconds, false
	if len(seconds) >= driftMinSamples {
		recent := seconds[len(seconds)-driftRecent:]
		before := seconds[:len(seconds)-driftRecent]
		ratio := Median(recent) / math.Max(1, Median(before))
		if ratio > DriftUp || ratio < DriftDown {
			use, changed = recent, true
		}
	}
	p50 := Median(use)
	p25 := NearestRank(use, 0.25)
	return Forecast{
		P50:           round1(p50),
		P25:           round1(math.Min(p25, p50)),
		P75:           round1(math.Max(NearestRank(use, 0.75), p50)),
		Lo:            round1(math.Min(p25, p50*(1-MinSpread))),
		Hi:            round1(math.Max(NearestRank(use, 0.9), p50*(1+MinSpread))),
		Basis:         basis,
		Samples:       len(use),
		Machine:       machine,
		ChangedLately: changed,
	}
}

// Chain holds the samples of the three levels, newest first as jobsdata.Reader.Samples returns them.
type Chain struct{ JobKey, Workflow, Repository []jobsdata.Sample }

// usable keeps samples not older than MaxAge, takes the same machine when it has MinSamples, and returns the
// newest Window as seconds, oldest first.
func usable(samples []jobsdata.Sample, now time.Time, machine string) (seconds []float64, usedMachine string) {
	recent := make([]jobsdata.Sample, 0, len(samples))
	for _, s := range samples {
		if now.Sub(s.At) <= MaxAge {
			recent = append(recent, s)
		}
	}
	sort.SliceStable(recent, func(i, j int) bool { return recent[i].At.After(recent[j].At) })
	if machine != "" {
		var same []jobsdata.Sample
		for _, s := range recent {
			if s.MachineID == machine {
				same = append(same, s)
			}
		}
		if len(same) >= MinSamples {
			recent, usedMachine = same, machine
		}
	}
	if len(recent) > Window {
		recent = recent[:Window]
	}
	seconds = make([]float64, len(recent))
	for i, s := range recent {
		seconds[len(recent)-1-i] = s.Seconds
	}
	return seconds, usedMachine
}

// ForChain forecasts from the job key, else the workflow, else the repository, else BasisNone (with the job key's
// sample count). Samples older than MaxAge are dropped here. machine is the machine id to prefer; "" for none.
func ForChain(c Chain, now time.Time, machine string) Forecast {
	levels := []struct {
		basis   Basis
		samples []jobsdata.Sample
	}{{BasisJobKey, c.JobKey}, {BasisWorkflow, c.Workflow}, {BasisRepository, c.Repository}}
	for _, l := range levels {
		sec, m := usable(l.samples, now, machine)
		if len(sec) >= MinSamples {
			return FromSeconds(sec, l.basis, m)
		}
	}
	sec, _ := usable(c.JobKey, now, "")
	return noHistory(len(sec))
}

// RemainingAfter is what is left of a running job's forecast after elapsed seconds, floored at MinRemaining.
// A forecast with BasisNone is returned unchanged.
func RemainingAfter(f Forecast, elapsed float64) Forecast {
	if f.Basis == BasisNone {
		return f
	}
	cut := func(v float64) float64 { return math.Max(MinRemaining, v-elapsed) }
	f.P50, f.P25, f.P75, f.Lo, f.Hi = cut(f.P50), cut(f.P25), cut(f.P75), cut(f.Lo), cut(f.Hi)
	return f
}
