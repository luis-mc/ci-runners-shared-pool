package tests

import (
	"regexp"
	"strings"
	"time"
)

var (
	// A duration or a random seed inside a name: "(0.12s)", "[35ms]", "seed=123", "seed 9f2a".
	durationRE = regexp.MustCompile(`\s*[\(\[]\s*\d+(\.\d+)?\s*(ms|s|m|us|µs)\s*[\)\]]`)
	seedRE     = regexp.MustCompile(`(?i)\s*[\(\[]?\bseed[=: ]\s*[0-9a-f]+[\)\]]?`)
	spaceRE    = regexp.MustCompile(`\s+`)
)

// DefaultNormaliser implements Normaliser (R5.33): a test id is the format, suite and name with no
// durations and no random seeds.
type DefaultNormaliser struct{}

// TestID returns "format|suite|name", normalised.
func (DefaultNormaliser) TestID(f Format, c Case) string {
	return string(f) + "|" + clean(c.Suite) + "|" + clean(c.Name)
}

func clean(s string) string {
	s = durationRE.ReplaceAllString(s, "")
	s = seedRE.ReplaceAllString(s, "")
	return strings.TrimSpace(spaceRE.ReplaceAllString(s, " "))
}

// Kind reads the test kind from the job name and the file name, else unit (R5.34).
func (DefaultNormaliser) Kind(jobName, file string) TestKind {
	s := strings.ToLower(jobName + " " + file)
	switch {
	case containsAny(s, "e2e", "end-to-end", "end to end", "playwright", "cypress", ".spec."):
		return E2E
	case containsAny(s, "integration", "integ ", "_integration", "-integration"):
		return Integration
	}
	return Unit
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}

// Limit is the G1 limit of a kind: unit 10 s, integration 60 s, e2e 2 min (R5.34).
func Limit(k TestKind) (d time.Duration) {
	switch k {
	case Integration:
		return time.Duration(60e9)
	case E2E:
		return time.Duration(120e9)
	}
	return time.Duration(10e9)
}
