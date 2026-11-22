package forecast

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsdata"
)

var now = time.Date(2026, 11, 18, 14, 30, 0, 0, time.UTC)

func near(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func fill(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func sample(daysAgo float64, secs float64, machine string) jobsdata.Sample {
	return jobsdata.Sample{At: now.Add(-time.Duration(daysAgo * float64(24*time.Hour))), Seconds: secs, MachineID: machine}
}

func TestPercentile(t *testing.T) {
	cases := []struct {
		in   []float64
		p    float64
		want float64
	}{
		{nil, 0.5, 0}, {[]float64{10}, 0.9, 10}, {[]float64{1, 2, 3, 4}, 0.5, 2.5},
		{[]float64{4, 1, 3, 2}, 0, 1}, {[]float64{4, 1, 3, 2}, 1, 4},
	}
	for _, c := range cases {
		near(t, Percentile(c.in, c.p), c.want)
	}
	near(t, NearestRank([]float64{1, 2, 3, 4}, 0.25), 1)
	near(t, NearestRank([]float64{1, 2, 3, 4}, 0.9), 4)
	near(t, NearestRank(nil, 0.9), 0)
	near(t, Median([]float64{3, 1, 2}), 2)
}

func TestFromSeconds(t *testing.T) {
	if f := FromSeconds([]float64{10, 11, 12, 13}, BasisJobKey, ""); f.Basis != BasisNone || f.Samples != 4 {
		t.Fatalf("not enough history: %+v", f)
	}
	f := FromSeconds([]float64{100, 100, 100, 100, 100, 100, 100, 100, 100, 300}, BasisJobKey, "")
	near(t, f.P50, 100)
	if f.Lo > 90 || f.Hi < 110 || f.Basis != BasisJobKey || f.Samples != 10 {
		t.Fatalf("median and range: %+v", f)
	}
	w := FromSeconds([]float64{100, 100, 100, 100, 100, 101}, BasisJobKey, "")
	near(t, w.Lo, 90)
	near(t, w.Hi, 110)
	near(t, w.P25, 100)
	near(t, w.P75, 100)
}

func TestDrift(t *testing.T) {
	f := FromSeconds(append(fill(20, 100), fill(10, 160)...), BasisJobKey, "")
	if !f.ChangedLately || f.P50 != 160 || f.Samples != 10 {
		t.Fatalf("drift up: %+v", f)
	}
	f = FromSeconds(append(fill(20, 100), fill(10, 50)...), BasisJobKey, "")
	if !f.ChangedLately || f.P50 != 50 {
		t.Fatalf("drift down: %+v", f)
	}
	f = FromSeconds(append(fill(20, 100), fill(10, 110)...), BasisJobKey, "")
	if f.ChangedLately || f.Samples != 30 {
		t.Fatalf("steady: %+v", f)
	}
	if f := FromSeconds(append(fill(5, 100), fill(9, 300)...), BasisJobKey, ""); f.ChangedLately {
		t.Fatalf("under 15 samples never drifts: %+v", f)
	}
	if f := FromSeconds(append(fill(5, 100), fill(10, 300)...), BasisJobKey, ""); !f.ChangedLately {
		t.Fatalf("15 samples can drift: %+v", f)
	}
}

func TestUsableWindowAndAge(t *testing.T) {
	var s []jobsdata.Sample
	s = append(s, sample(120, 5, "")) // too old, newest-first order is restored by the sort anyway
	for i := 0; i < 40; i++ {
		s = append(s, sample(float64(1+i), float64(139-i), ""))
	}
	sec, m := usable(s, now, "")
	if len(sec) != Window || m != "" {
		t.Fatalf("len %d machine %q", len(sec), m)
	}
	for _, v := range sec {
		if v == 5 {
			t.Fatal("old sample kept")
		}
	}
	near(t, sec[len(sec)-1], 139) // newest is last (oldest first)
}

func TestSameMachinePreference(t *testing.T) {
	var s []jobsdata.Sample
	for i := 0; i < 6; i++ {
		s = append(s, sample(float64(1+i), 50, "linux-box"), sample(float64(10+i), 90, "other"))
	}
	f := ForChain(Chain{JobKey: s}, now, "linux-box")
	if f.Machine != "linux-box" || f.Samples != 6 || f.P50 != 50 {
		t.Fatalf("same machine: %+v", f)
	}
	f = ForChain(Chain{JobKey: s[:6]}, now, "nobody")
	if f.Machine != "" || f.Samples != 6 {
		t.Fatalf("not enough on the machine: %+v", f)
	}
}

func TestChainFallback(t *testing.T) {
	few := []jobsdata.Sample{sample(1, 10, ""), sample(2, 10, ""), sample(3, 10, "")}
	var many []jobsdata.Sample
	for i, v := range []float64{50, 52, 51, 53, 50, 54} {
		many = append(many, sample(float64(i+1), v, ""))
	}
	cases := []struct {
		name string
		c    Chain
		want Basis
		text string
	}{
		{"job key", Chain{many, few, few}, BasisJobKey, "median of the last 6"},
		{"workflow", Chain{few, many, few}, BasisWorkflow, "typical time of this workflow"},
		{"repository", Chain{few, few, many}, BasisRepository, "typical time of this repository"},
		{"none", Chain{few, few, few}, BasisNone, ""},
	}
	for _, c := range cases {
		f := ForChain(c.c, now, "")
		if f.Basis != c.want || BasisText(f) != c.text {
			t.Errorf("%s: %+v %q", c.name, f, BasisText(f))
		}
	}
	if f := ForChain(Chain{JobKey: few}, now, ""); f.Samples != 3 || RangeText(f) != "not enough history yet" {
		t.Fatalf("none keeps the count: %+v", f)
	}
	// four recent samples plus old ones do not count
	four := append(append([]jobsdata.Sample{}, few...), sample(1, 9, ""), sample(200, 9, ""))
	if f := ForChain(Chain{JobKey: four}, now, ""); f.Basis != BasisNone || f.Samples != 4 {
		t.Fatalf("four samples: %+v", f)
	}
}

func TestRangeAndBasisText(t *testing.T) {
	cases := []struct {
		f    Forecast
		want string
	}{
		{Forecast{Basis: BasisJobKey, Lo: 360, Hi: 540}, "about 6 to 9 min"},
		{Forecast{Basis: BasisJobKey, Lo: 41, Hi: 54}, "about 40 to 55 s"},
		{Forecast{Basis: BasisJobKey, Lo: 300, Hi: 310}, "about 5 min"},
		{Forecast{Basis: BasisJobKey, Lo: 50, Hi: 200}, "about 1 to 3 min"},
		{Forecast{Basis: BasisNone}, "not enough history yet"},
	}
	for _, c := range cases {
		if got := RangeText(c.f); got != c.want {
			t.Errorf("%+v: %q, want %q", c.f, got, c.want)
		}
	}
}

func TestRemainingAfter(t *testing.T) {
	f := RemainingAfter(Forecast{Basis: BasisJobKey, P50: 100, P25: 80, P75: 120, Lo: 70, Hi: 130}, 90)
	if f.P50 != 10 || f.P25 != MinRemaining || f.Hi != 40 {
		t.Fatalf("%+v", f)
	}
	if f := RemainingAfter(Forecast{Basis: BasisNone}, 10); f.P50 != 0 {
		t.Fatalf("%+v", f)
	}
}

func node(id string, n float64, needs ...string) Node {
	return Node{ID: id, Needs: needs, Remaining: Forecast{Basis: BasisJobKey, P25: n * 0.8, P50: n, P75: n * 1.5}}
}

func TestCriticalPath(t *testing.T) {
	dag := []Node{node("lint", 60), node("types", 90), node("unit", 200, "lint"), node("e2e", 300, "unit", "types"), node("docs", 20)}
	path, eta, ok := CriticalPath(dag)
	if !reflect.DeepEqual(path, []string{"lint", "unit", "e2e"}) || !ok {
		t.Fatalf("%v %v", path, ok)
	}
	near(t, eta.P50, 560)
	near(t, eta.Lo, 448)
	near(t, eta.Hi, 840)

	rev := []Node{dag[4], dag[3], dag[2], dag[1], dag[0]}
	if p, _, _ := CriticalPath(rev); !reflect.DeepEqual(p, []string{"lint", "unit", "e2e"}) {
		t.Fatalf("out of order: %v", p)
	}
	dag[0].Remaining = Forecast{Basis: BasisJobKey}
	if _, e, _ := CriticalPath(dag); e.P50 != 500 {
		t.Fatalf("finished node: %v", e)
	}
}

func TestCriticalPathOddGraphs(t *testing.T) {
	odd := []Node{node("a", 10, "b"), node("b", 10, "a", "ghost")}
	path, eta, _ := CriticalPath(odd)
	if eta.P50 <= 0 || len(path) == 0 {
		t.Fatalf("cycle: %v %v", path, eta)
	}
	path, eta, ok := CriticalPath(nil)
	if path != nil || eta != (Eta{}) || !ok {
		t.Fatalf("empty: %v %v %v", path, eta, ok)
	}
	unknown := []Node{node("a", 10), {ID: "b", Needs: []string{"a"}, Remaining: Forecast{Basis: BasisNone}}}
	path, eta, ok = CriticalPath(unknown)
	if ok || eta.P50 != 10 || !reflect.DeepEqual(path, []string{"a", "b"}) {
		t.Fatalf("unknown on path: %v %v %v", path, eta, ok)
	}
	off := []Node{node("a", 100), {ID: "x", Remaining: Forecast{Basis: BasisNone}}}
	if _, _, ok := CriticalPath(off); !ok {
		t.Fatal("unknown node off the path must not matter")
	}
}

func job(n float64) SlotJob {
	return SlotJob{Remaining: Forecast{Basis: BasisJobKey, Lo: n * 0.8, P50: n, Hi: n * 1.5}}
}

func TestSimulateQueue(t *testing.T) {
	// 2 slots, 3 running (ends 100, 200, 300): slots free at 200 and 300. Queued 50, 100, 10, 10.
	etas, known := SimulateQueue(2, []SlotJob{job(100), job(200), job(300)}, []SlotJob{job(50), job(100), job(10), job(10)})
	want := []float64{200, 250, 300, 310}
	for i, w := range want {
		if !known[i] {
			t.Fatalf("job %d unknown", i)
		}
		near(t, etas[i].P50, w)
		if etas[i].Lo > etas[i].P50 || etas[i].Hi < etas[i].P50 {
			t.Fatalf("range %d: %+v", i, etas[i])
		}
	}
	near(t, etas[0].Lo, 160)
	near(t, etas[0].Hi, 300)
}

func TestSimulateQueueEdges(t *testing.T) {
	etas, known := SimulateQueue(1, nil, []SlotJob{job(60), job(30)})
	if etas[0].P50 != 0 || etas[1].P50 != 60 || !known[0] || !known[1] {
		t.Fatalf("free slot: %v %v", etas, known)
	}
	etas, known = SimulateQueue(0, nil, []SlotJob{job(60)})
	if len(etas) != 1 || known[0] || etas[0] != (Eta{}) {
		t.Fatalf("zero slots: %v %v", etas, known)
	}
	etas, known = SimulateQueue(2, []SlotJob{job(120)}, []SlotJob{job(60), job(30)})
	if etas[0].P50 != 0 || etas[1].P50 != 60 || !known[1] {
		t.Fatalf("one running, one free: %v", etas)
	}
	none := SlotJob{Remaining: Forecast{Basis: BasisNone}}
	_, known = SimulateQueue(1, nil, []SlotJob{none, job(5)})
	if !known[0] || known[1] {
		t.Fatalf("unknown ahead: %v", known)
	}
	_, known = SimulateQueue(1, []SlotJob{none}, []SlotJob{job(5)})
	if known[0] {
		t.Fatalf("unknown running: %v", known)
	}
	if e, k := SimulateQueue(2, nil, nil); len(e) != 0 || len(k) != 0 {
		t.Fatal("no queue")
	}
}

func TestSimulateQueueSlots(t *testing.T) {
	run := func(n float64, slot int) SlotJob { j := job(n); j.Slot = slot; return j }
	// 2 slots: the running job holds slot 2 for 100 s, so slot 1 is free first.
	etas, known, slot := SimulateQueueSlots(2, []SlotJob{run(100, 2)}, []SlotJob{job(50), job(30), job(10)})
	wantSlot, wantStart := []int{1, 1, 1}, []float64{0, 50, 80}
	for i := range wantSlot {
		if !known[i] || slot[i] != wantSlot[i] {
			t.Fatalf("job %d: slot %v known %v", i, slot, known)
		}
		near(t, etas[i].P50, wantStart[i])
	}
	// The same running job on slot 1 leaves slot 2 to the queue.
	_, _, slot = SimulateQueueSlots(2, []SlotJob{run(100, 1)}, []SlotJob{job(50), job(60)})
	if slot[0] != 2 || slot[1] != 2 {
		t.Fatalf("held slot 1: %v", slot)
	}
	// A running job with no known slot takes the lowest free number; ties go to the lower slot.
	_, _, slot = SimulateQueueSlots(3, []SlotJob{job(100)}, []SlotJob{job(5), job(5), job(5)})
	if slot[0] != 2 || slot[1] != 3 || slot[2] != 2 {
		t.Fatalf("loose running: %v", slot)
	}
	// Starts agree with SimulateQueue; no slots or no history leaves slot 0.
	e1, k1 := SimulateQueue(2, []SlotJob{job(100)}, []SlotJob{job(50), job(20)})
	e2, k2, _ := SimulateQueueSlots(2, []SlotJob{job(100)}, []SlotJob{job(50), job(20)})
	if e1[1] != e2[1] || k1[1] != k2[1] {
		t.Fatalf("differs: %v %v", e1, e2)
	}
	_, k, slot := SimulateQueueSlots(0, nil, []SlotJob{job(5)})
	if k[0] || slot[0] != 0 {
		t.Fatalf("zero slots: %v %v", k, slot)
	}
	none := SlotJob{Remaining: Forecast{Basis: BasisNone}}
	_, k, slot = SimulateQueueSlots(1, nil, []SlotJob{none, job(5)})
	if !k[0] || slot[0] != 1 || k[1] || slot[1] != 0 {
		t.Fatalf("no history: %v %v", k, slot)
	}
}
