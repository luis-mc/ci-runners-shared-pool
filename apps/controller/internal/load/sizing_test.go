package load

import (
	"testing"
	"time"
)

func meminfoUsed(usedKB int) string {
	return "MemTotal: 32000000 kB\nMemAvailable: " + itoa(32000000-usedKB) + " kB\n"
}

// R5.47: the median of 5 samples 1 s apart; a spike does not decide.
func TestSizingReadIsMedianOfFiveSamplesOneSecondApart(t *testing.T) {
	p := linux(map[string]string{})
	var gaps []time.Duration
	uses := []int{8000000, 8000000, 30000000, 8000000, 8000000}
	i := 0
	p.files["/proc/meminfo"] = meminfoUsed(uses[0])
	sleep := func(d time.Duration) {
		gaps = append(gaps, d)
		i++
		p.files["/proc/meminfo"] = meminfoUsed(uses[i])
	}
	s, ok := SizingRead(p, sleep, nil)
	if !ok || len(gaps) != 4 || gaps[0] != time.Second {
		t.Fatalf("ok=%v gaps=%v", ok, gaps)
	}
	if s.UsedOutsideBytes != 8000000*1024 || s.TotalBytes != 32000000*1024 {
		t.Fatalf("median decided: %+v", s)
	}
}

func TestFreshPublishedFigureWinsWhenLarger(t *testing.T) {
	p := linux(map[string]string{"/proc/meminfo": meminfoUsed(8000000)})
	pub := &Report{MemoryKnown: true, MemoryPct: 50, SampledAt: p.now.Add(-10 * time.Second)}
	s, _ := SizingRead(p, func(time.Duration) {}, pub)
	if !s.Published || s.UsedOutsideBytes != int64(float64(32000000*1024)*0.5) {
		t.Fatalf("%+v", s)
	}
	old := &Report{MemoryKnown: true, MemoryPct: 50, SampledAt: p.now.Add(-5 * time.Minute)}
	if s, _ = SizingRead(p, func(time.Duration) {}, old); s.Published {
		t.Fatal("a stale figure must not win")
	}
	low := &Report{MemoryKnown: true, MemoryPct: 10, SampledAt: p.now}
	if s, _ = SizingRead(p, func(time.Duration) {}, low); s.Published {
		t.Fatal("a smaller figure must not win")
	}
}

func TestSizingReadUnknownWhenMemoryUnreadable(t *testing.T) {
	if _, ok := SizingRead(linux(nil), nil, nil); ok {
		t.Fatal("must be unknown")
	}
}
