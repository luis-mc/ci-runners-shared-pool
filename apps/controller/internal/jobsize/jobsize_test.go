package jobsize

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)

// at is a sample n seconds after t0: a fake clock, no sleeps.
func at(n int, cpu, mem int64) Sample {
	return Sample{At: t0.Add(time.Duration(n) * time.Second), CPUMillis: cpu, MemoryBytes: mem, Known: true}
}

// R5.60: peak CPU is p95 over the windows (not the max), the first window has no CPU, memory is the max.
func TestMeasurePeaks(t *testing.T) {
	var s []Sample
	s = append(s, at(0, 0, 100)) // no earlier reading: left out of the CPU percentile
	for i := 1; i <= 20; i++ {
		s = append(s, at(i, int64(i)*100, int64(i)*10))
	}
	s[20] = at(20, 9000, 5) // one spike window, the single largest
	m := Measure(s, Run{})
	if m.PeakCPUMillis != 1900 { // 20 windows, p95 is the 19th: the spike (rank 20) is not the peak
		t.Fatalf("peak CPU %d", m.PeakCPUMillis)
	}
	if m.PeakMemoryBytes != 190 || m.Samples != 21 || m.DurationMS != 20000 || !m.Good() {
		t.Fatalf("%+v", m)
	}
}

// R5.60: the peak the system kept wins over the sampled memory.
func TestMeasureSystemPeak(t *testing.T) {
	m := Measure([]Sample{at(0, 0, 10), at(1, 50, 20)}, Run{MemoryPeakBytes: 999})
	if m.PeakMemoryBytes != 999 {
		t.Fatalf("%+v", m)
	}
}

// R5.61: an OOM kill or a cut short run is not good; unknown samples count for nothing.
func TestMeasureFlags(t *testing.T) {
	s := []Sample{at(0, 0, 1), at(1, 10, 2), {At: t0, Known: false}}
	if !Measure(s, Run{}).Good() {
		t.Fatal("plain run is good")
	}
	if Measure(s, Run{OOMKilled: true}).Good() || Measure(s, Run{CutShort: true}).Good() {
		t.Fatal("OOM and cut short are not good")
	}
	if m := Measure(s[:1], Run{}); m.Good() || m.Samples != 1 {
		t.Fatalf("one sample is not good: %+v", m)
	}
	m := Measure([]Sample{{At: t0}}, Run{UnknownReason: "cgroup not readable"})
	if m.Samples != 0 || m.UnknownReason != "cgroup not readable" || m.PeakMemoryBytes != 0 {
		t.Fatalf("%+v", m)
	}
	if Measure(nil, Run{}).UnknownReason != "no samples" {
		t.Fatal("empty run says no samples")
	}
}

// R5.63: measured from 5 good runs; bad runs are skipped; only the newest 30 good runs count; p90.
func TestSummaryMeasuredAtFive(t *testing.T) {
	var runs []Peak
	for i := 1; i <= 4; i++ {
		runs = append(runs, Peak{CPUMillis: int64(i) * 1000, MemoryBytes: int64(i), Good: true})
	}
	runs = append(runs, Peak{CPUMillis: 99999, MemoryBytes: 99999, Good: false})
	if s := Summarise(runs); s.Measured || s.Runs != 4 || s.CPUP90Millis != 4000 {
		t.Fatalf("4 good runs: %+v", s)
	}
	runs = append(runs, Peak{CPUMillis: 5000, MemoryBytes: 5, Good: true})
	if s := Summarise(runs); !s.Measured || s.Runs != 5 || s.CPUP90Millis != 5000 || s.MemoryP90Bytes != 5 {
		t.Fatalf("5 good runs: %+v", s)
	}
	if s := Summarise(nil); s.Measured || s.Runs != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestSummaryKeepsNewest30(t *testing.T) {
	var runs []Peak
	for i := 0; i < 40; i++ { // newest first: the 30 newest are 1000, the 10 oldest are 90000
		v := int64(1000)
		if i >= 30 {
			v = 90000
		}
		runs = append(runs, Peak{CPUMillis: v, MemoryBytes: v, Good: true})
	}
	if s := Summarise(runs); s.Runs != KeepRuns || s.CPUP90Millis != 1000 {
		t.Fatalf("%+v", s)
	}
}

func TestPercentile(t *testing.T) {
	if Percentile(nil, 90) != 0 || Percentile([]int64{7}, 90) != 7 || Percentile([]int64{3, 1, 2}, 100) != 3 {
		t.Fatal("percentile")
	}
}
