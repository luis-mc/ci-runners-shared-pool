package load

import (
	"testing"
	"time"
)

// R5.19: an agent in a container decides on the worse of host and container.
func TestCgroupWorseOfHostAndContainer(t *testing.T) {
	files := map[string]string{
		"/.dockerenv": "", "/proc/meminfo": meminfo, "/proc/stat": statA, // host 85%
		"/sys/fs/cgroup/memory.current": "950\n", "/sys/fs/cgroup/memory.max": "1000\n", // container 95%
		"/sys/fs/cgroup/cpu.stat": "usage_usec 1000000\n", "/sys/fs/cgroup/cpu.max": "200000 100000\n", // 2 CPUs
	}
	p := linux(files)
	s := NewSampler(p)
	r := s.Sample()
	if !r.CgroupLimited || r.MemoryPct != 95.0 {
		t.Fatalf("memory = %v limited=%v, want container 95", r.MemoryPct, r.CgroupLimited)
	}
	// 2 CPU-seconds used over 5 s on a 2-CPU cgroup = 20%.
	files["/sys/fs/cgroup/cpu.stat"] = "usage_usec 3000000\n"
	p.tick(5 * time.Second)
	r = s.Sample()
	if !r.CPUKnown || r.CPUPct < 19.9 {
		t.Fatalf("cpu = %v known=%v", r.CPUPct, r.CPUKnown)
	}
}

func TestCgroupHostWorseKeepsHost(t *testing.T) {
	files := map[string]string{
		"/run/.containerenv": "", "/proc/meminfo": meminfo, "/proc/stat": statA,
		"/sys/fs/cgroup/memory.current": "100\n", "/sys/fs/cgroup/memory.max": "1000\n",
	}
	r := NewSampler(linux(files)).Sample()
	if r.MemoryPct != 85.0 || !r.CgroupLimited {
		t.Fatalf("memory = %v, want host 85", r.MemoryPct)
	}
}

func TestUncappedCgroupAndNoContainer(t *testing.T) {
	files := map[string]string{"/.dockerenv": "", "/proc/meminfo": meminfo, "/proc/stat": statA,
		"/sys/fs/cgroup/memory.current": "100\n", "/sys/fs/cgroup/memory.max": "max\n"}
	if r := NewSampler(linux(files)).Sample(); r.CgroupLimited || r.MemoryPct != 85.0 {
		t.Fatalf("uncapped: %+v", r)
	}
	// Not in a container: cgroup files are ignored.
	files2 := map[string]string{"/proc/meminfo": meminfo, "/proc/stat": statA,
		"/sys/fs/cgroup/memory.current": "999\n", "/sys/fs/cgroup/memory.max": "1000\n"}
	if r := NewSampler(linux(files2)).Sample(); r.CgroupLimited || r.MemoryPct != 85.0 {
		t.Fatalf("no container: %+v", r)
	}
}

func TestCgroupParsers(t *testing.T) {
	if v, err := ParseCPUStat("usage_usec 42\nuser_usec 1\n"); err != nil || v != 42 {
		t.Fatalf("%v %v", v, err)
	}
	if _, err := ParseCPUStat("x 1"); err == nil {
		t.Fatal("missing usage_usec must fail")
	}
	if c, ok := ParseCPUMax("200000 100000"); !ok || c != 2 {
		t.Fatalf("%v %v", c, ok)
	}
	if _, ok := ParseCPUMax("max 100000"); ok {
		t.Fatal("uncapped must not be ok")
	}
}
