package jobsample

import (
	"context"
	"sync"
	"time"
)

// JobCounters is what a Windows Job Object says of the processes in it (OPS66 R5.62): the two times are the
// total user and kernel time in 100 ns units (JOBOBJECT_BASIC_AND_IO_ACCOUNTING_INFORMATION), PeakMemory is
// PeakJobMemoryUsed (JOBOBJECT_EXTENDED_LIMIT_INFORMATION), Terminated is TotalTerminatedProcesses, the
// processes the system ended for a limit.
type JobCounters struct {
	UserTime100ns, KernelTime100ns int64
	PeakMemory                     int64
	Terminated                     int64
}

// JobQuerier reads a job by its name. The Windows build has the real one; a test has a fake.
type JobQuerier interface {
	Query(name string) (JobCounters, error)
}

// JobObjectSampler samples the Job Object around the runner's process tree. A Job Object keeps no current
// memory, only its peak, so the memory of a sample is the peak so far: it never falls, and the largest of the
// samples is the peak the system kept.
type JobObjectSampler struct {
	Q JobQuerier

	mu   sync.Mutex
	prev map[string]cpuMark
}

// NewJobObjectSampler returns a sampler over q.
func NewJobObjectSampler(q JobQuerier) *JobObjectSampler {
	return &JobObjectSampler{Q: q, prev: map[string]cpuMark{}}
}

// Source implements Sampler.
func (*JobObjectSampler) Source() Source { return JobObject }

// Interval implements Sampler: one query a second is two system calls.
func (*JobObjectSampler) Interval() time.Duration { return Interval }

// Sample implements Sampler.
func (j *JobObjectSampler) Sample(ctx context.Context, t Target, now time.Time) (Sample, string, error) {
	if err := ctx.Err(); err != nil {
		return Sample{}, "", err
	}
	if t.JobObject == "" {
		return Sample{At: now}, "no job object for this job", nil
	}
	c, err := j.Q.Query(t.JobObject)
	if err != nil {
		return Sample{At: now}, "job object not readable", nil
	}
	usec := (c.UserTime100ns + c.KernelTime100ns) / 10
	j.mu.Lock()
	p, had := j.prev[t.JobObject]
	j.prev[t.JobObject] = cpuMark{usec: usec, at: now}
	j.mu.Unlock()
	s := Sample{At: now, MemoryBytes: c.PeakMemory, MemPeakBytes: c.PeakMemory, OOMKilled: c.Terminated > 0, Known: true}
	if had {
		s.CPUMillis = cpuMillis(usec-p.usec, now.Sub(p.at))
	}
	return s, "", nil
}

// Forget drops the state of a job that ended.
func (j *JobObjectSampler) Forget(t Target) {
	j.mu.Lock()
	delete(j.prev, t.JobObject)
	j.mu.Unlock()
}
