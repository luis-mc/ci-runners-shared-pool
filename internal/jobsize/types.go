// Package jobsize gives each CI job a first size guess by scan and count
// (scheduling R5.89 to R5.97, registry R5.77). It reads the workflow files and
// the test tool configs of a repository at a commit through the stackscan
// reader: it lists the commit's files once, reads only the files it needs,
// never clones and never runs code. The guess feeds slots.Settings.WeightOf;
// an override wins over a measured size, a measured size over the guess, and
// the guess over the name classes that stay as the fallback.
package jobsize

import "math"

// Class is the cost class of a job or a framework.
type Class string

// The three classes.
const (
	Light  Class = "light"
	Normal Class = "normal"
	Heavy  Class = "heavy"
)

// ValidClass reports whether c is one of the three classes.
func ValidClass(c Class) bool { return c == Light || c == Normal || c == Heavy }

// Source says where the size of a job came from, strongest first.
type Source string

// The sources. SourceDefault is the size of a job nothing is known about.
const (
	SourceOverride Source = "override"
	SourceMeasured Source = "measured"
	SourceScan     Source = "scan"
	SourceDefault  Source = "default"
)

// Confidence is how far a guess can be trusted. A job step that hides what
// runs, such as `run: ./ci.sh`, gives a low confidence guess.
type Confidence string

// The two levels.
const (
	ConfHigh Confidence = "high"
	ConfLow  Confidence = "low"
)

// Weight is a size in scheduler units (one CPU, one GiB; see UnitCPU, UnitGiB).
type Weight struct{ CPU, Memory int }

// DefaultWeight is the size of a job with no history and no scan result: one
// CPU unit and one memory unit, as before the scan (scheduling R5.69).
var DefaultWeight = Weight{CPU: 1, Memory: 1}

// ClassWeight is the weight a class stands for when an override names a class
// and no number. The scheduler cuts it to the machine's budget.
func ClassWeight(c Class) Weight {
	switch c {
	case Light:
		return Weight{1, 1}
	case Heavy:
		return Weight{4, 4}
	default:
		return Weight{2, 2}
	}
}

// ClassOfPeak is the class of a peak: up to 1 CPU and 1 GiB is light, up to 2
// and 2 is normal, anything more is heavy.
func ClassOfPeak(cpu, gib float64) Class {
	switch {
	case cpu <= 1 && gib <= 1:
		return Light
	case cpu <= 2 && gib <= 2:
		return Normal
	default:
		return Heavy
	}
}

// WeightOfPeak rounds a peak up to whole units, at least one of each.
func WeightOfPeak(cpu, gib float64) Weight {
	return Weight{CPU: unit(cpu), Memory: unit(gib)}
}

func unit(v float64) int {
	n := int(math.Ceil(v - 1e-9))
	if n < 1 {
		return 1
	}
	return n
}
