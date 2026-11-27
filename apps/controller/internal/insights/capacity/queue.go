package capacity

import (
	"math"
	"time"
)

// Budget limits of the owner's load budget (R5.17, R5.18).
const (
	// MemoryBudget is the share of a machine's memory the slots may use.
	MemoryBudget = 0.85
	// CPUBudget is the share of a machine's CPU the slots may use.
	CPUBudget = 0.60
	// TargetUtilisation is the utilisation a saturated pool is sized back under (R5.16).
	TargetUtilisation = 0.85
)

// fallbackTail is the p95/mean ratio of an exponential wait, used when no ratio can be seen now.
const fallbackTail = 3.0

// AllenCunneen is the Estimator of R5.16. It has no state.
type AllenCunneen struct{}

var _ Estimator = AllenCunneen{}

// Estimate returns the wait of load with extraSlots more slots. The offered load a = ρ·c₀ stays the same;
// the new utilisation is a/c with c = c₀ + extraSlots.
func (AllenCunneen) Estimate(load LabelSetLoad, extraSlots int) Estimate {
	if extraSlots < 0 {
		extraSlots = 0
	}
	c := load.Slots + extraSlots
	out := Estimate{ExtraSlots: extraSlots}
	offered := load.Utilisation * float64(load.Slots)
	if load.Slots <= 0 || c <= 0 {
		out.Saturated = true
		out.MinSlots = minSlots(offered)
		return out
	}
	rho := offered / float64(c)
	out.Utilisation = rho
	if rho >= 1 {
		out.Saturated = true
		out.MinSlots = minSlots(offered)
		return out
	}
	mean := meanWait(load, c, rho)
	out.MeanWait = mean
	// The p95 is the mean scaled by the ratio seen now: the observed p95 over the model's mean at the
	// current slot count. With no usable ratio the exponential tail stands in.
	ratio := fallbackTail
	if load.Slots > 0 && load.Utilisation < 1 {
		if now := meanWait(load, load.Slots, load.Utilisation); now > 0 && load.P95Wait > 0 {
			ratio = float64(load.P95Wait) / float64(now)
		}
	}
	out.P95Wait = time.Duration(float64(mean) * ratio)
	return out
}

// meanWait is Wq ≈ (ca² + cs²)/2 · ρ^(√(2(c+1)) − 1) / (c(1 − ρ)) · E[S].
func meanWait(load LabelSetLoad, c int, rho float64) time.Duration {
	if rho <= 0 {
		return 0
	}
	cc := float64(c)
	wq := (load.CVArrivals*load.CVArrivals + load.CVService*load.CVService) / 2 *
		math.Pow(rho, math.Sqrt(2*(cc+1))-1) / (cc * (1 - rho)) * float64(load.MeanService)
	if math.IsNaN(wq) || math.IsInf(wq, 0) || wq < 0 {
		return 0
	}
	return time.Duration(wq)
}

// minSlots is the smallest c with offered/c < TargetUtilisation, at least 1.
func minSlots(offered float64) int {
	c := int(math.Floor(offered/TargetUtilisation)) + 1
	if c < 1 {
		c = 1
	}
	for offered/float64(c) >= TargetUtilisation {
		c++
	}
	return c
}
