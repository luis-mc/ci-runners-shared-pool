package capacity

import (
	"math"
	"testing"
	"time"
)

// R5.16: c=2, rho=0.8, ca=cs=1, E[S]=60 s gives Wq = 0.8^(sqrt(6)-1) / (2*0.2) * 60 s = 108.55 s.
func TestAllenCunneenKnownValues(t *testing.T) {
	load := LabelSetLoad{Slots: 2, Utilisation: 0.8, MeanService: time.Minute, CVArrivals: 1, CVService: 1, P95Wait: 5 * time.Minute}
	got := AllenCunneen{}.Estimate(load, 0)
	if got.Saturated {
		t.Fatal("rho 0.8 is not saturated")
	}
	want := math.Pow(0.8, math.Sqrt(6)-1) / (2 * 0.2) * 60
	if d := math.Abs(got.MeanWait.Seconds() - want); d > 0.01 {
		t.Fatalf("mean wait %v, want %.2f s", got.MeanWait, want)
	}
	// With the p95 seen now, the ratio is kept: at +0 the estimate p95 is the observed p95.
	if d := math.Abs(got.P95Wait.Seconds() - 300); d > 0.01 {
		t.Fatalf("p95 at +0 = %v, want the observed 5m", got.P95Wait)
	}
	// One more slot: offered load 1.6 over 3 slots.
	more := AllenCunneen{}.Estimate(load, 1)
	if more.Utilisation < 0.533 || more.Utilisation > 0.534 {
		t.Fatalf("utilisation with +1 = %v", more.Utilisation)
	}
	if more.MeanWait >= got.MeanWait || more.P95Wait >= got.P95Wait {
		t.Fatalf("one more slot must cut the wait: %v -> %v", got, more)
	}
}

func TestMoreSlotsNeverRaiseWait(t *testing.T) {
	load := LabelSetLoad{Slots: 2, Utilisation: 0.9, MeanService: 90 * time.Second, CVArrivals: 1.3, CVService: 0.8, P95Wait: 10 * time.Minute}
	prev := AllenCunneen{}.Estimate(load, 0).P95Wait
	for extra := 1; extra <= 4; extra++ {
		cur := AllenCunneen{}.Estimate(load, extra).P95Wait
		if cur > prev {
			t.Fatalf("extra %d: p95 %v rose from %v", extra, cur, prev)
		}
		prev = cur
	}
}

// R5.16: at rho >= 1 no wait is given; the answer is the smallest c with rho < 0.85.
func TestSaturatedGivesMinimumSlots(t *testing.T) {
	// offered load 2.4 slots (rho 1.2 on 2 slots): 3 slots give 0.8 < 0.85.
	load := LabelSetLoad{Slots: 2, Utilisation: 1.2, MeanService: time.Minute, CVArrivals: 1, CVService: 1}
	got := AllenCunneen{}.Estimate(load, 0)
	if !got.Saturated || got.MeanWait != 0 || got.P95Wait != 0 || got.MinSlots != 3 {
		t.Fatalf("got %+v", got)
	}
	// +1 slot gives rho 0.8: not saturated any more, with a number.
	if e := (AllenCunneen{}).Estimate(load, 1); e.Saturated || e.MeanWait <= 0 {
		t.Fatalf("+1: %+v", e)
	}
	// Exactly at 0.85 is saturated as a target: offered 1.7 on 2 slots needs 3.
	if m := minSlots(1.7); m != 3 {
		t.Fatalf("minSlots(1.7) = %d", m)
	}
	if m := minSlots(0); m != 1 {
		t.Fatalf("minSlots(0) = %d", m)
	}
}

func TestEstimateNoSlots(t *testing.T) {
	got := AllenCunneen{}.Estimate(LabelSetLoad{}, 2)
	if !got.Saturated || got.MinSlots != 1 {
		t.Fatalf("got %+v", got)
	}
}
