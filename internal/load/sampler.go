package load

import (
	"errors"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Defaults of R5.16 and the sizing of DL79.
const (
	// Window is how many samples the decision median covers (R5.16, DL176).
	Window = 5
	// SampleEvery is how often the agent samples (R5.16).
	SampleEvery = 5 * time.Second
	// RawMaxAge: a previous raw counter older than this is not used for a CPU delta.
	RawMaxAge = 10 * time.Minute
	// MinGiBPerCPU: a slot's CPU gets at least this much memory (DL79).
	MinGiBPerCPU = 1.5
)

// Report is the figure the machine decides on and sends in LoadReport.
type Report struct {
	SampledAt     time.Time
	MemoryPct     float64
	MemoryKnown   bool
	CPUPct        float64
	CPUKnown      bool
	UnknownReason string
	// WSLVMBytes is the WSL VM's memory: the larger of the vmmem working set
	// and its private bytes, and at least what its containers use (R5.17).
	WSLVMBytes int64
	// ContainersBytes is memory used by containers, when read.
	ContainersBytes int64
	// CgroupLimited is true when the figure is the worse of host and container (R5.19).
	CgroupLimited bool
	// HostUseP95Bytes is the rolling p95 of the host's own use over 7 days, set
	// when HostUseReady. HostUseSpan is how long the samples cover (OPS61).
	HostUseP95Bytes int64
	HostUseReady    bool
	HostUseSpan     time.Duration
}

type raw struct {
	at       time.Time
	memPct   float64
	memKnown bool
	cpuPct   float64
	cpuKnown bool
}

// Sampler takes samples and answers with the median of the last Window (R5.16).
// Sample never sleeps and never runs a slow command; Refresh reads the slow
// figures (Docker, WSL VM, macOS CPU) into a cache the samples read, so one
// sample stays under 100 ms (R6.6).
type Sampler struct {
	p Probe

	mu        sync.Mutex
	prev      Counters
	prevAt    time.Time
	havePrev  bool
	ring      []raw
	latest    Report
	haveOne   bool
	extras    extras
	cgPrev    cgSample
	cgPrevSet bool
	use       HostUse
}

type extras struct {
	wsBytes, privBytes int64
	containers         int64
	// poolContainers is the memory of the pool's containers (Linux, macOS) and
	// ownBytes that of crsp's own processes; both leave the host's own use.
	poolContainers, ownBytes int64
	dockerNote               string
	macCPU                   float64
	macCPUKnown              bool
	at                       time.Time
}

// NewSampler returns a sampler over p.
func NewSampler(p Probe) *Sampler { return &Sampler{p: p} }

// Sample takes one sample now, stores it and returns the report decided on
// the median of the last Window samples.
func (s *Sampler) Sample() Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.p.Now()
	r := raw{at: now}
	switch s.p.GOOS() {
	case "windows":
		pct, total, err := s.p.WinMemory()
		if err == nil {
			r.memPct, r.memKnown = s.adjustWSL(pct, total), true
		}
		if c, cerr := s.p.WinTimes(); cerr == nil {
			r.cpuPct, r.cpuKnown = s.cpuFrom(c, now)
		}
	case "darwin":
		if res, ok := s.p.Run([]string{"sysctl", "-n", "hw.memsize"}); ok && res.Code == 0 {
			if ms, err := strconv.ParseInt(strings.TrimSpace(res.Out), 10, 64); err == nil {
				if vm, ok2 := s.p.Run([]string{"vm_stat"}); ok2 && vm.Code == 0 {
					if pct, perr := ParseVMStat(vm.Out, ms); perr == nil {
						r.memPct, r.memKnown = pct, true
					}
				}
			}
		}
		if s.extras.macCPUKnown {
			r.cpuPct, r.cpuKnown = s.extras.macCPU, true
		} else {
			r.cpuPct, r.cpuKnown = s.loadavgCPU()
		}
	default:
		if txt, err := s.p.ReadFile("/proc/meminfo"); err == nil {
			if pct, _, perr := ParseMeminfo(txt); perr == nil {
				r.memPct, r.memKnown = pct, true
			}
		}
		if txt, err := s.p.ReadFile("/proc/stat"); err == nil {
			if c, perr := ParseProcStat(txt); perr == nil {
				r.cpuPct, r.cpuKnown = s.cpuFrom(c, now)
			}
		}
	}
	if own, ok := s.hostOwnBytes(); ok {
		s.use.Add(now, own)
	}
	cg := s.cgroup(now)
	s.ring = append(s.ring, r)
	if len(s.ring) > Window {
		s.ring = s.ring[len(s.ring)-Window:]
	}
	rep := s.decide(now, cg)
	s.latest, s.haveOne = rep, true
	return rep
}

// cpuFrom is the delta of raw counters against the previous reading; with no
// usable previous reading it falls back to the load average per core, else
// unknown (R5.15, R5.20).
func (s *Sampler) cpuFrom(c Counters, now time.Time) (float64, bool) {
	prev, prevAt, have := s.prev, s.prevAt, s.havePrev
	s.prev, s.prevAt, s.havePrev = c, now, true
	if have && now.Sub(prevAt) >= 0 && now.Sub(prevAt) <= RawMaxAge && c.Total > prev.Total {
		return CPUBetween(prev, c), true
	}
	return s.loadavgCPU()
}

func (s *Sampler) loadavgCPU() (float64, bool) {
	if s.p.GOOS() != "linux" {
		return 0, false
	}
	txt, err := s.p.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, false
	}
	l, err := ParseLoadAvg(txt)
	if err != nil || s.p.NumCPU() <= 0 {
		return 0, false
	}
	v := round1(100 * l / float64(s.p.NumCPU()))
	if v > 100 {
		v = 100
	}
	return v, true
}

// adjustWSL replaces the (trimmed) working-set part of the host's used memory
// with the VM figure: max(working set, private bytes, container use) (R5.17).
func (s *Sampler) adjustWSL(pct float64, total int64) float64 {
	if total <= 0 {
		return pct
	}
	e := s.extras
	vm := wslVM(e.wsBytes, e.privBytes, e.containers)
	if vm == 0 {
		return pct
	}
	used := float64(total) * pct / 100
	used = used - float64(e.wsBytes) + float64(vm)
	if used > float64(total) {
		used = float64(total)
	}
	if used < 0 {
		used = 0
	}
	return round1(100 * used / float64(total))
}

func wslVM(ws, priv, containers int64) int64 {
	vm := ws
	if priv > vm {
		vm = priv
	}
	if containers > vm {
		vm = containers
	}
	return vm
}

// decide builds the Report from the ring: median per figure over the samples
// where it was known; the worse of host and container when in a container.
func (s *Sampler) decide(now time.Time, cg cgSample) Report {
	var mems, cpus []float64
	for _, r := range s.ring {
		if r.memKnown {
			mems = append(mems, r.memPct)
		}
		if r.cpuKnown {
			cpus = append(cpus, r.cpuPct)
		}
	}
	rep := Report{SampledAt: now}
	if len(mems) > 0 {
		rep.MemoryKnown, rep.MemoryPct = true, round1(Median(mems))
	}
	if len(cpus) > 0 {
		rep.CPUKnown, rep.CPUPct = true, round1(Median(cpus))
	}
	if cg.memKnown && (!rep.MemoryKnown || cg.memPct > rep.MemoryPct) {
		rep.MemoryKnown, rep.MemoryPct, rep.CgroupLimited = true, cg.memPct, true
	} else if cg.memKnown {
		rep.CgroupLimited = true
	}
	if cg.cpuKnown && (!rep.CPUKnown || cg.cpuPct > rep.CPUPct) {
		rep.CPUKnown, rep.CPUPct, rep.CgroupLimited = true, cg.cpuPct, true
	}
	if s.p.GOOS() == "windows" {
		rep.WSLVMBytes = wslVM(s.extras.wsBytes, s.extras.privBytes, s.extras.containers)
		rep.ContainersBytes = s.extras.containers
	}
	st := s.use.Stats(now)
	rep.HostUseReady, rep.HostUseP95Bytes, rep.HostUseSpan = st.Ready, st.P95Bytes, st.Span
	switch {
	case !rep.MemoryKnown:
		rep.UnknownReason = "memory cannot be measured on this machine"
	case !rep.CPUKnown:
		rep.UnknownReason = "CPU cannot be measured yet; deciding on memory only"
	}
	return rep
}

// Latest returns the newest report without measuring: the job-started gate
// never waits for a sample (R6.6). ok is false before the first sample.
func (s *Sampler) Latest() (Report, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latest, s.haveOne
}

// Decide answers whether a new job may start under the budget (R5.20, R5.23):
// unknown memory holds the job, unknown CPU decides on memory only.
func Decide(r Report, haveSample bool, budgetMemPct, budgetCPUPct int) (ok bool, reasons []string) {
	if !haveSample || !r.MemoryKnown {
		why := r.UnknownReason
		if why == "" {
			why = "load is unknown"
		}
		return false, []string{"load unknown: " + why}
	}
	if o := Over(r, float64(budgetMemPct), float64(budgetCPUPct)); len(o) > 0 {
		return false, o
	}
	return true, nil
}

// Slots sizes a machine's job slots from its budget (DL79, DL176): each slot
// needs one CPU and MinGiBPerCPU of memory inside the budget; there is always
// at least one slot when the budget leaves any room.
func Slots(cpus int, totalMemBytes int64, budgetMemPct, budgetCPUPct int) int {
	if cpus <= 0 || totalMemBytes <= 0 {
		return 1
	}
	byCPU := cpus * budgetCPUPct / 100
	gib := float64(totalMemBytes) / (1 << 30)
	byMem := int(gib * float64(budgetMemPct) / 100 / MinGiBPerCPU)
	n := byCPU
	if byMem < n {
		n = byMem
	}
	if n < 1 {
		n = 1
	}
	if n > 64 {
		n = 64
	}
	return n
}

// ---------------------------------------------------------------- cgroup v2

type cgSample struct {
	memPct   float64
	memKnown bool
	cpuPct   float64
	cpuKnown bool
	usec     int64
	at       time.Time
}

// CgroupDir is where cgroup v2 is mounted.
const CgroupDir = "/sys/fs/cgroup"

// InContainer reports a container: /.dockerenv or /run/.containerenv exists.
func InContainer(p Probe) bool {
	return p.GOOS() == "linux" && (p.Exists("/.dockerenv") || p.Exists("/run/.containerenv"))
}

// cgroup reads the container's own figures (R5.19): memory.current over
// memory.max, and the cpu.stat usage delta over the CPUs the cgroup may use.
func (s *Sampler) cgroup(now time.Time) cgSample {
	var out cgSample
	if !InContainer(s.p) {
		return out
	}
	if cur, err := s.p.ReadFile(path.Join(CgroupDir, "memory.current")); err == nil {
		if mx, err2 := s.p.ReadFile(path.Join(CgroupDir, "memory.max")); err2 == nil {
			c, e1 := strconv.ParseInt(strings.TrimSpace(cur), 10, 64)
			m, e2 := strconv.ParseInt(strings.TrimSpace(mx), 10, 64)
			if e1 == nil && e2 == nil && m > 0 {
				out.memPct, out.memKnown = round1(100*float64(c)/float64(m)), true
			}
		}
	}
	if txt, err := s.p.ReadFile(path.Join(CgroupDir, "cpu.stat")); err == nil {
		if usec, perr := ParseCPUStat(txt); perr == nil {
			out.usec, out.at = usec, now
			cpus := float64(s.p.NumCPU())
			if mt, merr := s.p.ReadFile(path.Join(CgroupDir, "cpu.max")); merr == nil {
				if c, ok := ParseCPUMax(mt); ok {
					cpus = c
				}
			}
			if s.cgPrevSet && cpus > 0 {
				dt := now.Sub(s.cgPrev.at).Seconds()
				if dt > 0 && dt <= RawMaxAge.Seconds() && usec >= s.cgPrev.usec {
					v := 100 * float64(usec-s.cgPrev.usec) / 1e6 / dt / cpus
					if v > 100 {
						v = 100
					}
					out.cpuPct, out.cpuKnown = round1(v), true
				}
			}
			s.cgPrev, s.cgPrevSet = out, true
		}
	}
	return out
}

// ErrNoWindows is returned by a Windows-only read on another OS.
var ErrNoWindows = errors.New("not Windows")
