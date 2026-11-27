package insights

import (
	"sort"
	"time"
)

// guardrailOrder ranks findings that save no time (G4, G5) by guardrail order after the others (R5.9).
func guardrailOrder(g Guardrail) int {
	if g == "" {
		return 99
	}
	return int(g[len(g)-1] - '0')
}

// Rank orders findings for the Do next list (R5.11): minutes saved a week (high first), then first seen
// (old first), then id. Findings that save 0 minutes come after the others and sort by guardrail order, then
// first seen (R5.9). The input is not changed.
func Rank(in []StoredFinding) []StoredFinding {
	out := append([]StoredFinding(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.MinutesSavedWeek != b.MinutesSavedWeek {
			return a.MinutesSavedWeek > b.MinutesSavedWeek
		}
		if a.MinutesSavedWeek == 0 && guardrailOrder(a.Guardrail) != guardrailOrder(b.Guardrail) {
			return guardrailOrder(a.Guardrail) < guardrailOrder(b.Guardrail)
		}
		if !a.FirstSeen.Equal(b.FirstSeen) {
			return a.FirstSeen.Before(b.FirstSeen)
		}
		return a.ID < b.ID
	})
	return out
}

// TopN returns the first n findings of Rank.
func TopN(in []StoredFinding, n int) []StoredFinding {
	r := Rank(in)
	if n < len(r) {
		r = r[:n]
	}
	return r
}

// Minutes rounds a duration to whole minutes, the unit of every "minutes saved a week" figure (R5.9). The
// figure is an estimate and the screens and the digest label it so.
func Minutes(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int((d + 30*time.Second) / time.Minute)
}
