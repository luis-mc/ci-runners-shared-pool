package forecast

import (
	"fmt"
	"math"
)

// RangeText says the range in words: "about 6 to 9 min", "about 40 to 55 s", or "not enough history yet".
// Under 90 seconds at the high end it counts seconds in steps of 5, else whole minutes.
func RangeText(f Forecast) string {
	if f.Basis == BasisNone || f.Basis == "" {
		return "not enough history yet"
	}
	if f.Hi < 90 {
		lo := math.Max(5, math.Round(f.Lo/5)*5)
		hi := math.Max(lo, math.Round(f.Hi/5)*5)
		if lo == hi {
			return fmt.Sprintf("about %.0f s", lo)
		}
		return fmt.Sprintf("about %.0f to %.0f s", lo, hi)
	}
	lo := math.Max(1, math.Round(f.Lo/60))
	hi := math.Max(lo, math.Round(f.Hi/60))
	if lo == hi {
		return fmt.Sprintf("about %.0f min", lo)
	}
	return fmt.Sprintf("about %.0f to %.0f min", lo, hi)
}

// BasisText says what the forecast rests on: "median of the last 20", "typical time of this workflow", "typical
// time of this repository". Empty for no history.
func BasisText(f Forecast) string {
	switch f.Basis {
	case BasisJobKey:
		return fmt.Sprintf("median of the last %d", f.Samples)
	case BasisWorkflow:
		return "typical time of this workflow"
	case BasisRepository:
		return "typical time of this repository"
	case BasisNone:
		return ""
	}
	return ""
}
