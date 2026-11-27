package machinehealth

import (
	"strconv"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// MinTrendSpan is how long the samples of a volume must cover before a days-until-full figure is given.
const MinTrendSpan = 30 * time.Minute

// Trend is the days-until-full of one volume.
type Trend struct {
	// Days is nil when it cannot be said; Note says why.
	Days *float64
	Note string
}

// DaysUntilFull fits a line through the free-space samples of one volume
// (least squares) and says in how many days it reaches zero. Free space that
// is not falling gives no figure.
func DaysUntilFull(samples []store.DiskSample) Trend {
	if len(samples) < 2 {
		return Trend{Note: "needs at least two samples"}
	}
	first, last := samples[0].At, samples[len(samples)-1].At
	if last.Sub(first) < MinTrendSpan {
		return Trend{Note: "needs 30 minutes of samples"}
	}
	n := float64(len(samples))
	var sx, sy, sxx, sxy float64
	for _, s := range samples {
		x, y := s.At.Sub(first).Seconds(), float64(s.FreeBytes)
		sx, sy, sxx, sxy = sx+x, sy+y, sxx+x*x, sxy+x*y
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return Trend{Note: "the samples have one time"}
	}
	slope := (n*sxy - sx*sy) / den // bytes per second
	if slope >= 0 {
		return Trend{Note: "free space is not falling"}
	}
	free := float64(samples[len(samples)-1].FreeBytes)
	days := free / (-slope * 86400)
	return Trend{Days: &days}
}

// OutOfDate says whether version a is older than b. It reads dotted numbers
// with an optional v prefix and ignores a suffix after - or +; a version that
// is not numbers (dev builds) is never out of date.
func OutOfDate(a, b string) bool {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka || !okb {
		return false
	}
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return out, false
	}
	parts := strings.Split(v, ".")
	if len(parts) > 3 {
		parts = parts[:3]
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
