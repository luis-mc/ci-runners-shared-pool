package load

import (
	"testing"
	"time"
)

// R5.15: parsers per OS. Ports test_load.py Parsers.
func TestParsersLinux(t *testing.T) {
	pct, total, err := ParseMeminfo(meminfo)
	if err != nil || pct != 85.0 || total != 32000000*1024 {
		t.Fatalf("meminfo = %v %v %v", pct, total, err)
	}
	a, _ := ParseProcStat(statA)
	b, _ := ParseProcStat(statB)
	if got := CPUBetween(a, b); got != 50.0 {
		t.Fatalf("cpu = %v, want 50", got)
	}
	if _, _, err := ParseMeminfo("nothing"); err == nil {
		t.Fatal("meminfo without MemTotal must fail")
	}
	if _, err := ParseProcStat("intr 1 2"); err == nil {
		t.Fatal("stat without a cpu line must fail")
	}
}

func TestParsersMacOS(t *testing.T) {
	// 16384-byte pages, 1,000,000 pages of memory, 320,000 free+inactive+speculative -> 68% in use.
	if got, err := ParseVMStat(vmStat, 16384*1000000); err != nil || got != 68.0 {
		t.Fatalf("vm_stat = %v %v", got, err)
	}
	if got, err := ParseTopCPU(top); err != nil || got != 65.0 {
		t.Fatalf("top = %v %v", got, err)
	}
	if _, err := ParseTopCPU("no cpu line"); err == nil {
		t.Fatal("top without a CPU line must fail")
	}
	if _, err := ParseVMStat(vmStat, 0); err == nil {
		t.Fatal("zero memsize must fail")
	}
}

func TestWindowsCPUFromTimes(t *testing.T) {
	if got := CPUBetween(Counters{100, 1000}, Counters{400, 1500}); got != 60.0 {
		t.Fatalf("cpu = %v, want 60", got)
	}
	if got := CPUBetween(Counters{5, 5}, Counters{5, 5}); got != 0 {
		t.Fatalf("cpu = %v, want 0", got)
	}
}

// R5.15: CPU is a delta of raw counters between samples, never a sleep. The
// first sample falls back to the load average per core; ports
// test_no_sleep_and_delta_from_saved_raw and test_delta_and_fallbacks.
func TestDeltaFromSavedRawAndFallbacks(t *testing.T) {
	p := linux(map[string]string{"/proc/meminfo": meminfo, "/proc/stat": statA, "/proc/loadavg": "0.28 0.1 0.1 1/100 5\n"})
	s := NewSampler(p)
	first := s.Sample()
	if !first.CPUKnown || first.CPUPct != 7.0 { // 0.28 / 4 cores
		t.Fatalf("first sample cpu = %v known=%v, want load-average 7.0", first.CPUPct, first.CPUKnown)
	}
	p.files["/proc/stat"] = statB
	p.tick(5 * time.Second)
	second := s.Sample()
	// samples: 7.0 (loadavg), 50.0 (delta): median is their mean.
	if second.CPUPct != 28.5 {
		t.Fatalf("second cpu = %v, want 28.5", second.CPUPct)
	}
	// A previous reading older than RawMaxAge is not used, and with no load average CPU is unknown.
	p2 := linux(map[string]string{"/proc/meminfo": meminfo, "/proc/stat": statA})
	s2 := NewSampler(p2)
	if r := s2.Sample(); r.CPUKnown {
		t.Fatal("CPU must be unknown with no previous reading and no load average")
	}
	p2.files["/proc/stat"] = statB
	p2.tick(RawMaxAge + time.Second)
	if r := s2.Sample(); r.CPUKnown {
		t.Fatalf("an old previous reading must not give a delta, got %v", r.CPUPct)
	}
}

func TestSampleNeverRunsCommandsOnLinux(t *testing.T) {
	p := linux(map[string]string{"/proc/meminfo": meminfo, "/proc/stat": statA})
	NewSampler(p).Sample()
	if len(p.calls) != 0 {
		t.Fatalf("a Linux sample ran commands: %v", p.calls)
	}
}

func TestMacOSSample(t *testing.T) {
	p := &fakeProbe{os: "darwin", cpus: 8, now: time.Unix(1_000_000, 0), cmds: []fakeCmd{
		{[]string{"sysctl", "-n", "hw.memsize"}, Result{Out: "16384000000\n"}, true},
		{[]string{"vm_stat"}, Result{Out: vmStat}, true},
		{[]string{"top"}, Result{Out: top}, true},
	}}
	s := NewSampler(p)
	r := s.Sample()
	if !r.MemoryKnown || r.CPUKnown {
		t.Fatalf("before Refresh: mem known=%v cpu known=%v", r.MemoryKnown, r.CPUKnown)
	}
	s.Refresh()
	r = s.Sample()
	if !r.CPUKnown || r.CPUPct != 65.0 {
		t.Fatalf("after Refresh cpu = %v known=%v", r.CPUPct, r.CPUKnown)
	}
	if p.calledWSL() {
		t.Fatal("macOS must never call wsl")
	}
}

// R5.20: memory unknown holds new jobs; CPU unknown decides on memory only.
func TestUnknownLoad(t *testing.T) {
	s := NewSampler(linux(map[string]string{})) // nothing readable
	r := s.Sample()
	if r.MemoryKnown || r.UnknownReason == "" {
		t.Fatalf("report = %+v", r)
	}
	if ok, why := Decide(r, true, 85, 60); ok || len(why) != 1 {
		t.Fatalf("unknown memory must hold the job, got ok=%v %v", ok, why)
	}
	if ok, _ := Decide(Report{}, false, 85, 60); ok {
		t.Fatal("no sample yet must hold the job")
	}
	// CPU unknown, memory under budget: the job may start, and the report says why.
	p := linux(map[string]string{"/proc/meminfo": meminfo, "/proc/stat": statA})
	rep := NewSampler(p).Sample()
	rep.MemoryPct = 40
	if ok, _ := Decide(rep, true, 85, 60); !ok {
		t.Fatal("CPU unknown must decide on memory only")
	}
	if rep.UnknownReason == "" || rep.CPUKnown {
		t.Fatalf("CPU unknown must be said: %+v", rep)
	}
	// Over names the numbers.
	got := Over(Report{MemoryKnown: true, MemoryPct: 91, CPUKnown: true, CPUPct: 72.5}, 85, 60)
	if len(got) != 2 || got[0] != "memory 91% > 85%" || got[1] != "CPU 72.5% > 60%" {
		t.Fatalf("over = %v", got)
	}
}

// R6.6: the gate reads the newest sample and never waits for one.
func TestLatestDoesNotMeasure(t *testing.T) {
	p := linux(map[string]string{"/proc/meminfo": meminfo, "/proc/stat": statA})
	s := NewSampler(p)
	if _, ok := s.Latest(); ok {
		t.Fatal("no sample yet")
	}
	s.Sample()
	p.files["/proc/meminfo"] = "MemTotal: 100 kB\nMemAvailable: 100 kB\n"
	r, ok := s.Latest()
	if !ok || r.MemoryPct != 85.0 {
		t.Fatalf("Latest = %+v %v; it must return the stored sample", r, ok)
	}
}

func BenchmarkSample(b *testing.B) {
	p := linux(map[string]string{"/proc/meminfo": meminfo, "/proc/stat": statA, "/proc/loadavg": "0.5 0 0 1/1 1"})
	s := NewSampler(p)
	for i := 0; i < b.N; i++ {
		s.Sample()
	}
}

func TestSampleUnder100ms(t *testing.T) {
	p := linux(map[string]string{"/proc/meminfo": meminfo, "/proc/stat": statA})
	s := NewSampler(p)
	start := time.Now()
	s.Sample()
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("a sample took %v, want under 100 ms (R6.6)", d)
	}
}

// R5.15 on the real machine: the OS probe measures something on Linux.
func TestOSProbeMeasuresThisMachine(t *testing.T) {
	if (OSProbe{}).GOOS() != "linux" {
		t.Skip("Linux only")
	}
	r := NewSampler(OSProbe{}).Sample()
	if !r.MemoryKnown || r.MemoryPct <= 0 || r.MemoryPct > 100 {
		t.Fatalf("memory = %+v", r)
	}
}

func TestSlotsFromBudget(t *testing.T) {
	const g = int64(1) << 30
	cases := []struct {
		cpus   int
		mem    int64
		bm, bc int
		want   int
		name   string
	}{
		{20, 32 * g, 85, 60, 12, "cpu 12, memory 18 -> 12"},
		{8, 8 * g, 85, 60, 4, "8 CPUs at 60% -> 4; memory 4"},
		{16, 4 * g, 85, 60, 2, "memory bound: 4 GiB * 85% / 1.5"},
		{2, 2 * g, 10, 10, 1, "never below one"},
		{128, 1024 * g, 95, 95, 64, "never above 64"},
		{0, 0, 85, 60, 1, "unknown machine"},
	}
	for _, c := range cases {
		if got := Slots(c.cpus, c.mem, c.bm, c.bc); got != c.want {
			t.Errorf("%s: Slots = %d, want %d", c.name, got, c.want)
		}
	}
}
