package capacity

import (
	"testing"
	"time"
)

// R5.20: the summary written at the job's end counts known samples only.
func TestSummaryAtJobEnd(t *testing.T) {
	at := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	var samples []ResourceSample
	for i := 1; i <= 20; i++ {
		samples = append(samples, ResourceSample{At: at.Add(time.Duration(i) * 5 * time.Second), CPUMillis: int64(i * 100), MemoryBytes: int64(i) << 20, Known: true})
	}
	samples = append(samples, ResourceSample{At: at, CPUMillis: 99999, MemoryBytes: 1 << 40, Known: false})
	s := Summarise(7, "o/r|ci.yml|build", "m1", samples, "", at.Add(time.Hour))
	if s.Samples != 20 || s.JobID != 7 || s.JobIdent != "o/r|ci.yml|build" || s.MachineID != "m1" {
		t.Fatalf("got %+v", s)
	}
	if s.CPUP50Millis != 1000 || s.CPUP95Millis != 1900 || s.CPUPeakMillis != 2000 {
		t.Fatalf("cpu %+v", s)
	}
	if s.MemP50 != 10<<20 || s.MemP95 != 19<<20 || s.MemPeak != 20<<20 {
		t.Fatalf("mem %+v", s)
	}
	if s.UnknownReason != "" || !s.CompletedAt.Equal(at.Add(time.Hour)) {
		t.Fatalf("got %+v", s)
	}
}

func TestSummaryUnknownKeepsReason(t *testing.T) {
	at := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	s := Summarise(1, "k", "m", []ResourceSample{{At: at, Known: false}}, "cgroup not readable", at)
	if s.Samples != 0 || s.CPUP95Millis != 0 || s.UnknownReason != "cgroup not readable" {
		t.Fatalf("got %+v", s)
	}
	if e := Summarise(1, "k", "m", nil, "", at); e.UnknownReason != "no samples" {
		t.Fatalf("got %+v", e)
	}
}
