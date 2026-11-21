package jobsample

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"sync"
	"time"
)

// FileReader reads a file; os.ReadFile in production, a fake in tests.
type FileReader func(path string) ([]byte, error)

// CgroupSampler reads a container or VM job's cgroup v2 files: cpu.stat usage_usec and memory.current.
// CPU is the usage delta over the wall delta since the previous sample of the same job.
type CgroupSampler struct {
	Read FileReader

	mu      sync.Mutex
	prev    map[string]cpuMark
	oomBase map[string]int64 // oom_kill count at the first sample of a job
}

type cpuMark struct {
	usec int64
	at   time.Time
}

// NewCgroupSampler returns a sampler over read.
func NewCgroupSampler(read FileReader) *CgroupSampler {
	return &CgroupSampler{Read: read, prev: map[string]cpuMark{}, oomBase: map[string]int64{}}
}

// Source implements Sampler.
func (*CgroupSampler) Source() Source { return Cgroup }

// Interval implements Sampler: four small file reads a second cost far under 0.1 percent of a core.
func (*CgroupSampler) Interval() time.Duration { return Interval }

// Sample implements Sampler. The first sample of a job has CPUMillis 0: there is no earlier reading.
func (c *CgroupSampler) Sample(ctx context.Context, t Target, now time.Time) (Sample, string, error) {
	if err := ctx.Err(); err != nil {
		return Sample{}, "", err
	}
	if t.CgroupPath == "" {
		return Sample{At: now}, "no cgroup path for this job", nil
	}
	stat, err := c.Read(t.CgroupPath + "/cpu.stat")
	if err != nil {
		return Sample{At: now}, "cgroup cpu.stat not readable", nil
	}
	usec, ok := fieldInt(stat, "usage_usec")
	if !ok {
		return Sample{At: now}, "cgroup cpu.stat has no usage_usec", nil
	}
	memRaw, err := c.Read(t.CgroupPath + "/memory.current")
	if err != nil {
		return Sample{At: now}, "cgroup memory.current not readable", nil
	}
	mem, err := strconv.ParseInt(string(bytes.TrimSpace(memRaw)), 10, 64)
	if err != nil {
		return Sample{At: now}, "cgroup memory.current not a number", nil
	}
	key := t.RunnerID + "/" + strconv.FormatInt(t.JobID, 10) + "/" + t.CgroupPath
	c.mu.Lock()
	p, had := c.prev[key]
	c.prev[key] = cpuMark{usec: usec, at: now}
	c.mu.Unlock()
	s := Sample{At: now, MemoryBytes: mem, Known: true}
	if had {
		s.CPUMillis = cpuMillis(usec-p.usec, now.Sub(p.at))
	}
	// memory.peak needs kernel 5.19; memory.events is old. A missing file leaves the value 0 (no peak, no OOM).
	if raw, err := c.Read(t.CgroupPath + "/memory.peak"); err == nil {
		if peak, err := strconv.ParseInt(string(bytes.TrimSpace(raw)), 10, 64); err == nil {
			s.MemPeakBytes = peak
		}
	}
	if raw, err := c.Read(t.CgroupPath + "/memory.events"); err == nil {
		if kills, ok := fieldInt(raw, "oom_kill"); ok {
			c.mu.Lock()
			base, seen := c.oomBase[key]
			if !seen {
				c.oomBase[key] = kills
				base = kills
			}
			c.mu.Unlock()
			s.OOMKilled = kills > base
		}
	}
	return s, "", nil
}

// Forget drops the state of a job that ended.
func (c *CgroupSampler) Forget(t Target) {
	c.mu.Lock()
	k := t.RunnerID + "/" + strconv.FormatInt(t.JobID, 10) + "/" + t.CgroupPath
	delete(c.prev, k)
	delete(c.oomBase, k)
	c.mu.Unlock()
}

// cpuMillis is cores x 1000 for usedMicros of CPU time over wall.
func cpuMillis(usedMicros int64, wall time.Duration) int64 {
	if usedMicros <= 0 || wall <= 0 {
		return 0
	}
	return usedMicros * 1000 / wall.Microseconds()
}

// fieldInt finds "name <int>" in a key-value file such as cpu.stat.
func fieldInt(data []byte, name string) (int64, bool) {
	for len(data) > 0 {
		var line []byte
		line, data, _ = bytes.Cut(data, []byte("\n"))
		k, v, ok := bytes.Cut(line, []byte(" "))
		if !ok || string(k) != name {
			continue
		}
		n, err := strconv.ParseInt(string(bytes.TrimSpace(v)), 10, 64)
		return n, err == nil
	}
	return 0, false
}

var errNoProc = errors.New("process table not readable")
