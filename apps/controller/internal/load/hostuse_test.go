package load

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

// feed adds one sample every HostUseStep for the span, value from f; the fake
// clock is the time argument, so nothing sleeps.
func feed(h *HostUse, from time.Time, span time.Duration, f func(i int) int64) time.Time {
	now := from
	for i := 0; now.Sub(from) <= span; i++ {
		h.Add(now, f(i))
		now = now.Add(HostUseStep)
	}
	return now.Add(-HostUseStep)
}

// machines R5.68: the p95 counts only after 24 h of samples; before that the
// span says how far the measuring has come.
func TestHostUseNeedsADayOfSamples(t *testing.T) {
	var h HostUse
	now := feed(&h, t0, 23*time.Hour, func(int) int64 { return 8 * g })
	st := h.Stats(now)
	if st.Ready || st.P95Bytes != 0 || st.Span != 23*time.Hour {
		t.Fatalf("23 h: %+v", st)
	}
	h.Add(now.Add(HostUseStep*12), 8*g)
	st = h.Stats(now.Add(HostUseStep * 12))
	if !st.Ready || st.P95Bytes != 8*g {
		t.Fatalf("24 h: %+v", st)
	}
}

func TestHostUseP95IsTheNearestRank(t *testing.T) {
	var h HostUse
	// 100 steps of 1..100 GiB cover 495 minutes, so add a day of 1 GiB first.
	start := t0
	end := feed(&h, start, 24*time.Hour, func(int) int64 { return g })
	for i := 1; i <= 100; i++ {
		end = end.Add(HostUseStep)
		h.Add(end, int64(i)*g)
	}
	st := h.Stats(end)
	if !st.Ready {
		t.Fatalf("%+v", st)
	}
	// 289 samples of 1 GiB and 100 of 1..100 GiB: rank ceil(0.95*389)=370, the 370-289=81st of the ramp, minus the 1 GiB it ties with.
	if want := int64(81) * g; st.P95Bytes != want {
		t.Fatalf("p95 %d GiB, want 81", st.P95Bytes/g)
	}
}

func TestHostUseForgetsAfterAWeek(t *testing.T) {
	var h HostUse
	feed(&h, t0, 24*time.Hour, func(int) int64 { return 20 * g })
	later := t0.Add(9 * 24 * time.Hour)
	end := feed(&h, later, 24*time.Hour, func(int) int64 { return 4 * g })
	st := h.Stats(end)
	if !st.Ready || st.P95Bytes != 4*g {
		t.Fatalf("the old week must not count: %+v", st)
	}
	if st.Span != 24*time.Hour {
		t.Fatalf("span %v", st.Span)
	}
}

func TestHostUseKeepsThePeakOfAStep(t *testing.T) {
	var h HostUse
	h.Add(t0, 3*g)
	h.Add(t0.Add(time.Minute), 9*g)
	h.Add(t0.Add(2*time.Minute), 5*g)
	if st := h.Stats(t0.Add(3 * time.Minute)); st.Samples != 1 {
		t.Fatalf("samples %d", st.Samples)
	}
	end := feed(&h, t0.Add(HostUseStep), 24*time.Hour, func(int) int64 { return g })
	if st := h.Stats(end); st.P95Bytes != g {
		t.Fatalf("one peak in 290 samples is under p95: %d", st.P95Bytes)
	}
}

func TestHostUseSaveRestore(t *testing.T) {
	var h HostUse
	end := feed(&h, t0, 25*time.Hour, func(i int) int64 { return int64(i%10+1) * g })
	path := filepath.Join(t.TempDir(), "d", "hostuse.json")
	if err := h.Save(path); err != nil {
		t.Fatal(err)
	}
	var r HostUse
	if err := r.Restore(path, end); err != nil {
		t.Fatal(err)
	}
	if a, b := h.Stats(end), r.Stats(end); a != b {
		t.Fatalf("restored %+v, saved %+v", b, a)
	}
	// A clock set back drops samples from the future.
	var back HostUse
	if err := back.Restore(path, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if st := back.Stats(t0.Add(time.Hour)); st.Samples == 0 || st.Span > time.Hour {
		t.Fatalf("future kept: %+v", st)
	}
	// Missing: fine and empty. Broken: an error and empty.
	var none HostUse
	if err := none.Restore(filepath.Join(t.TempDir(), "x.json"), end); err != nil || none.Stats(end).Samples != 0 {
		t.Fatal("missing file")
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := none.Restore(bad, end); err == nil {
		t.Fatal("broken file must error")
	}
}

func TestParsePSOwnCountsOnlyCrsp(t *testing.T) {
	out := " 20480 /usr/local/bin/crsp\n 1000 crsp-agent\n 99999 /usr/bin/firefox\n  bad line\n 512 /opt/crspx/helper\n"
	if got := ParsePSOwn(out); got != (20480+1000)<<10 {
		t.Fatalf("own %d", got)
	}
}

// One sample per OS: what leaves the host's own use is the pool, never the
// rest of the PC. Fake clock, fake probe, no sleep.
func TestHostOwnBytesPerOS(t *testing.T) {
	t.Run("linux takes off the pool containers and crsp", func(t *testing.T) {
		p := linux(map[string]string{"/proc/meminfo": meminfo})
		s := NewSampler(p)
		s.extras = extras{poolContainers: 6 * g, ownBytes: g / 2}
		used, _, _ := hostUsed(p)
		got, ok := s.hostOwnBytes()
		if !ok || got != used-6*g-g/2 {
			t.Fatalf("own %d of used %d", got, used)
		}
	})
	t.Run("darwin takes off the pool containers and crsp", func(t *testing.T) {
		p := &fakeProbe{os: "darwin", now: time.Unix(1_000_000, 0), cpus: 8, cmds: []fakeCmd{
			{[]string{"sysctl", "-n", "hw.memsize"}, Result{Out: "17179869184\n"}, true},
			{[]string{"vm_stat"}, Result{Out: vmStat}, true},
		}}
		s := NewSampler(p)
		s.extras = extras{poolContainers: 2 * g, ownBytes: g}
		used, _, _ := hostUsed(p)
		if got, ok := s.hostOwnBytes(); !ok || got != used-3*g {
			t.Fatalf("own %d of used %d", got, used)
		}
	})
	t.Run("windows takes off the vmmem working set and crsp, not the containers", func(t *testing.T) {
		p := winProbe()
		p.winMem, p.winTotal = []float64{75}, 32*g
		s := NewSampler(p)
		s.extras = extras{wsBytes: 10 * g, privBytes: 14 * g, containers: 12 * g, poolContainers: 99 * g, ownBytes: g}
		if got, ok := s.hostOwnBytes(); !ok || got != 24*g-10*g-g {
			t.Fatalf("own %d GiB", got/g)
		}
	})
	t.Run("never below zero", func(t *testing.T) {
		p := linux(map[string]string{"/proc/meminfo": meminfo})
		s := NewSampler(p)
		s.extras = extras{poolContainers: 500 * g}
		if got, ok := s.hostOwnBytes(); !ok || got != 0 {
			t.Fatalf("own %d", got)
		}
	})
	t.Run("unreadable memory is not a sample", func(t *testing.T) {
		if _, ok := NewSampler(linux(nil)).hostOwnBytes(); ok {
			t.Fatal("ok without memory")
		}
	})
}

// The sampler feeds the tracker on every sample and reports the p95 once it
// is ready.
func TestSamplerReportsHostUse(t *testing.T) {
	p := linux(map[string]string{"/proc/meminfo": meminfo})
	s := NewSampler(p)
	s.extras = extras{poolContainers: 4 * g}
	rep := s.Sample()
	if rep.HostUseReady || rep.HostUseP95Bytes != 0 {
		t.Fatalf("first sample: %+v", rep)
	}
	for i := 0; i < 24*12+1; i++ {
		p.tick(HostUseStep)
		rep = s.Sample()
	}
	used, _, _ := hostUsed(p)
	if !rep.HostUseReady || rep.HostUseP95Bytes != used-4*g || rep.HostUseSpan < HostUseMinSpan {
		t.Fatalf("after a day: %+v (want p95 %d)", rep, used-4*g)
	}
}

func TestRefreshReadsPoolContainersAndOwnUse(t *testing.T) {
	p := linux(map[string]string{"/proc/meminfo": meminfo})
	p.cmds = []fakeCmd{
		{[]string{"docker", "ps"}, Result{Out: fmt.Sprintf("a\t%s\nb\t\n", PoolLabel)}, true},
		{[]string{"docker", "stats"}, Result{Out: "a\t2GiB / 8GiB\nb\t1GiB / 8GiB\n"}, true},
		{[]string{"ps", "-axo"}, Result{Out: " 1024 /usr/bin/crsp\n 4096 /usr/bin/other\n"}, true},
	}
	s := NewSampler(p)
	s.Refresh()
	if s.extras.poolContainers != 2*g || s.extras.ownBytes != 1<<20 {
		t.Fatalf("extras %+v", s.extras)
	}
	if p.calledWSL() {
		t.Fatal("Linux never calls wsl")
	}
}
