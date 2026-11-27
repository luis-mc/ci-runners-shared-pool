package load_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/load"
)

// R5.40. Ported from test_load.py Parsers test_linux, test_macos and test_machine.py HostLoad test_parsers.
func TestMemoryParsers(t *testing.T) {
	avail := "MemTotal:       16000000 kB\nMemFree:         1000000 kB\nMemAvailable:    4000000 kB\n"
	if p, ok := load.ParseMeminfo(avail); !ok || p != 75 {
		t.Fatalf("MemAvailable: %v %v", p, ok)
	}
	old := "MemTotal:       10000 kB\nMemFree:         1000 kB\nBuffers:          500 kB\nCached:          1500 kB\n"
	if p, ok := load.ParseMeminfo(old); !ok || p != 70 {
		t.Fatalf("old kernel: %v %v", p, ok)
	}
	if _, ok := load.ParseMeminfo("nonsense"); ok {
		t.Fatal("parsed nonsense")
	}
	vm := "Mach Virtual Memory Statistics: (page size of 16384 bytes)\nPages free:        100.\nPages active:      500.\nPages inactive:    200.\nPages speculative: 100.\n"
	if p, ok := load.ParseVMStat(vm, 16384*1000); !ok || p != 60 {
		t.Fatalf("vm_stat: %v %v", p, ok)
	}
	if _, ok := load.ParseVMStat("", 100); ok {
		t.Fatal("parsed empty vm_stat")
	}
}

// R5.41. Ported from test_load.py Parsers test_windows_cpu_from_times and Cache test_delta_and_fallbacks.
func TestCPUParsersAndDelta(t *testing.T) {
	r, ok := load.ParseProcStat("cpu  100 0 100 700 100 0 0 0\ncpu0 1 1 1 1 1\n")
	if !ok || r.Total != 1000 || r.Busy != 200 {
		t.Fatalf("%+v %v", r, ok)
	}
	if _, ok := load.ParseProcStat("garbage"); ok {
		t.Fatal("garbage")
	}
	if w, ok := load.WindowsRaw(600, 800, 200); !ok || w.Busy != 400 || w.Total != 1000 {
		t.Fatalf("%+v", w)
	}
	if pct, ok := load.CPUBetween(load.Raw{Busy: 100, Total: 1000}, load.Raw{Busy: 200, Total: 1200}); !ok || pct != 50 {
		t.Fatalf("delta %v %v", pct, ok)
	}
	if _, ok := load.CPUBetween(load.Raw{Busy: 5, Total: 10}, load.Raw{Busy: 5, Total: 10}); ok {
		t.Fatal("no movement is no figure")
	}
	if pct, ok := load.ParseTopCPU("CPU usage: 3% user, 2% sys, 95.0% idle\nCPU usage: 10.0% user, 5.0% sys, 85.0% idle\n"); !ok || pct != 15 {
		t.Fatalf("top %v %v", pct, ok)
	}
	if pct, ok := load.LoadAvgCPU(8, 4); !ok || pct != 100 {
		t.Fatalf("loadavg cap %v", pct)
	}
	if pct, _ := load.LoadAvgCPU(1, 4); pct != 25 {
		t.Fatalf("loadavg %v", pct)
	}
}

type fakeProbe struct {
	mem                 float64
	memOK               bool
	raw                 load.Raw
	rawOK               bool
	avg                 float64
	avgOK               bool
	memCalls, rawCalls  int
	lastCPUFromLoadavgs int
}

func (f *fakeProbe) Memory() (float64, bool)  { f.memCalls++; return f.mem, f.memOK }
func (f *fakeProbe) CPURaw() (load.Raw, bool) { f.rawCalls++; return f.raw, f.rawOK }
func (f *fakeProbe) LoadAvgPerCore() (float64, bool) {
	f.lastCPUFromLoadavgs++
	return f.avg, f.avgOK
}

// R5.41. Ported from test_load.py Cache test_no_sleep_and_delta_from_saved_raw and test_unmeasurable_raises.
func TestCPUDeltaAndFallbacks(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	p := &fakeProbe{mem: 50, memOK: true, raw: load.Raw{Busy: 100, Total: 1000}, rawOK: true, avg: 30, avgOK: true}
	s := &load.Sampler{Probe: p, CachePath: filepath.Join(dir, "load.json"), Now: func() time.Time { return now }}
	// first sample: no saved raw, so CPU is the load average per core
	r := s.Sample()
	if !r.CPUKnown || r.CPUPct != 30 {
		t.Fatalf("first %+v", r)
	}
	// 20 s later: delta from the saved raw
	now = now.Add(20 * time.Second)
	p.raw = load.Raw{Busy: 300, Total: 1200}
	r = s.Sample()
	if !r.CPUKnown {
		t.Fatalf("second %+v", r)
	}
	// raw older than 10 minutes is not used
	now = now.Add(11 * time.Minute)
	p.raw = load.Raw{Busy: 400, Total: 1300}
	p.avg = 80
	if r = s.Sample(); r.CPUPct != 80 {
		t.Fatalf("stale raw used: %+v", r)
	}
	// nothing measurable: unknown, and the gate fails open
	now = now.Add(time.Hour)
	u := &load.Sampler{Probe: &fakeProbe{}, Now: func() time.Time { return now }}
	if r = u.Sample(); r.Measurable() {
		t.Fatalf("unmeasurable %+v", r)
	}
}

// R5.42. Ported from test_load.py Cache test_reuse_within_ttl_and_smoothing, test_unwritable_cache_still_works,
// test_default_cache_is_per_user_not_shared_temp, test_write_leaves_no_temp_file.
func TestReuseAndSmoothing(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	p := &fakeProbe{mem: 40, memOK: true, avg: 10, avgOK: true}
	s := &load.Sampler{Probe: p, CachePath: filepath.Join(dir, "sub", "load.json"), Now: func() time.Time { return now }}
	s.Sample()
	now = now.Add(5 * time.Second) // within 10 s: reused, one measurement for a burst of hooks
	s.Sample()
	if p.memCalls != 1 {
		t.Fatalf("memory read %d times in a burst", p.memCalls)
	}
	// smoothing: mean of the CPU of up to 3 samples in 30 s, memory the latest
	now = now.Add(10 * time.Second)
	p.avg, p.mem = 40, 60
	r := s.Sample() // samples: 10, 40 -> 25
	if r.CPUPct != 25 || r.MemoryPct != 60 {
		t.Fatalf("smoothed %+v", r)
	}
	now = now.Add(11 * time.Second)
	p.avg = 70
	r = s.Sample() // 10, 40, 70 -> 40
	if r.CPUPct != 40 {
		t.Fatalf("smoothed %+v", r)
	}
	now = now.Add(11 * time.Second)
	p.avg = 100
	r = s.Sample() // window is 30 s: 40, 70, 100 -> 70, oldest dropped
	if r.CPUPct != 70 {
		t.Fatalf("window %+v", r)
	}
	// no temp file is left behind
	entries, _ := os.ReadDir(filepath.Join(dir, "sub"))
	if len(entries) != 1 {
		t.Fatalf("files %v", entries)
	}
	// an unwritable cache still works
	bad := &load.Sampler{Probe: p, CachePath: filepath.Join(dir, "load.json", "x", "y"), Now: func() time.Time { return now }}
	if err := os.WriteFile(filepath.Join(dir, "load.json"), []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := bad.Sample(); !r.MemoryKnown {
		t.Fatalf("unwritable cache broke the sample: %+v", r)
	}
	// the default cache lives under the user's cache folder, not a shared temp folder
	if d := load.DefaultCachePath(); d != "" && filepath.Dir(d) == os.TempDir() {
		t.Fatalf("default cache in shared temp: %s", d)
	}
}

// R5.43. Ported from test_machine.py HostLoad test_fresh_host_file_wins_over_own_figures,
// test_stale_host_file_falls_back_to_own_figures, test_no_host_file_is_own, test_write_mode_writes_host_load.
func TestPublishedLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host-load.json")
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	own := load.Reading{MemoryPct: 10, MemoryKnown: true, At: now}
	if r := load.Combine(path, own, now); r.Source != "own" || r.MemoryPct != 10 {
		t.Fatalf("no file: %+v", r)
	}
	host := load.Reading{MemoryPct: 80, MemoryKnown: true, CPUPct: 50, CPUKnown: true, At: now.Add(-30 * time.Second)}
	if err := load.WriteHost(path, host); err != nil {
		t.Fatal(err)
	}
	if r := load.Combine(path, own, now); r.Source != "file" || r.MemoryPct != 80 {
		t.Fatalf("fresh: %+v", r)
	}
	host.At = now.Add(-90 * time.Second)
	_ = load.WriteHost(path, host)
	if r := load.Combine(path, own, now); r.Source != "own" || r.MemoryPct != 10 {
		t.Fatalf("stale file, own figure: %+v", r)
	}
	if r := load.Combine(path, load.Reading{}, now); r.Source != "stale" || r.MemoryPct != 80 {
		t.Fatalf("stale file, no own figure: %+v", r)
	}
}

// R5.45. Ported from test_load.py Limits test_over_names_the_numbers.
func TestOverNamesTheNumbers(t *testing.T) {
	b := load.Budget{MemoryPct: 85, CPUPct: 60}
	over, why := load.Over(load.Reading{MemoryPct: 91, MemoryKnown: true, CPUPct: 70, CPUKnown: true}, b)
	if !over || !reflect.DeepEqual(why, []string{"memory 91% > 85%", "CPU 70% > 60%"}) {
		t.Fatalf("%v %v", over, why)
	}
	if over, _ := load.Over(load.Reading{MemoryPct: 85, MemoryKnown: true, CPUPct: 60, CPUKnown: true}, b); over {
		t.Fatal("at the budget is not over")
	}
	if over, _ := load.Over(load.Reading{MemoryPct: 99}, b); over {
		t.Fatal("an unknown figure counted")
	}
}

// R5.46. Ported from test_load.py Parsers test_process_names.
func TestTopProcesses(t *testing.T) {
	ps := load.ParsePS("node 90.0 5.0\n/usr/bin/java 10.0 40.0\npostgres 1.0 20.0\nsome long name 0.5 0.1\nbad line\n")
	cpu, mem := load.TopNames(ps, 2)
	if !reflect.DeepEqual(cpu, []string{"node", "java"}) || !reflect.DeepEqual(mem, []string{"java", "postgres"}) {
		t.Fatalf("%v %v", cpu, mem)
	}
	win := load.ParsePowerShellCSV("\"ProcessName\",\"CPU\",\"WorkingSet64\"\r\n\"chrome\",\"12.5\",\"900\"\r\n\"idle\",\"\",\"1\"\r\n")
	if len(win) != 2 || win[0].Name != "chrome" {
		t.Fatalf("%+v", win)
	}
	// a failure to list processes never fails anything
	_ = load.TopProcessText()
}

// R5.40/R5.41 on the real operating system: the probe returns a plausible figure or says unknown.
func TestRealProbeIsSane(t *testing.T) {
	s := &load.Sampler{Probe: load.NewProbe()}
	r := s.Sample()
	if r.MemoryKnown && (r.MemoryPct < 0 || r.MemoryPct > 100) {
		t.Fatalf("memory %v", r.MemoryPct)
	}
	if r.CPUKnown && (r.CPUPct < 0 || r.CPUPct > 100) {
		t.Fatalf("cpu %v", r.CPUPct)
	}
}
