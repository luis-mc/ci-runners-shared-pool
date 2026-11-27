// Package jobsample samples the CPU and memory of each running job on the agent (docs/features/ci-run-insights
// R5.19, R6.4): the job's container cgroup for the container and VM backends, the runner process tree for a
// persistent runner. Samples go to the controller as JobResourceSamples over the agent link (protocol
// version 4), only when the controller's HelloAck says version 4 or more. CIX8 contract: types and
// interfaces only.
package jobsample

import (
	"context"
	"time"
)

// The sampling periods; Batch is how often a batch is sent (R5.19, OPS66 R5.62). A reader that costs one or
// two small reads (cgroup files, a Job Object query) samples every second, so the peak CPU is a percentile
// over 1 s windows: BenchmarkRealReaders measures 6 us for the four cgroup files, 0.0006 percent of a core. The
// process-tree reader lists every process of the machine, about 5 us a process, so 2.5 ms on a 500-process
// machine and once per running job: at 1 s with 8 jobs that is 2 percent of a core, at 5 s 0.4 percent. So it
// samples every 5 s. Both stay under the 1 percent of R6.4.
const (
	Interval     = time.Second
	TreeInterval = 5 * time.Second
	Batch        = 15 * time.Second
)

// Source is what a sampler measures.
type Source string

// Sources.
const (
	Cgroup      Source = "cgroup"
	ProcessTree Source = "process_tree"
	JobObject   Source = "job_object"
)

// Sample is one reading of one job. CPUMillis is CPU cores used since the previous sample, times 1000.
type Sample struct {
	At          time.Time
	CPUMillis   int64
	MemoryBytes int64
	Known       bool
	// MemPeakBytes is the peak the system kept for the whole job so far (cgroup memory.peak, Job Object
	// PeakJobMemoryUsed); 0 when the reader has none.
	MemPeakBytes int64
	// OOMKilled is set when the system killed the job for memory (cgroup oom_kill, Job Object terminated by
	// a limit).
	OOMKilled bool
}

// Target is one running job the agent knows: its runner and where its processes live.
type Target struct {
	RunnerID   string
	Repository string
	RunID      int64
	RunAttempt int
	Job        string
	JobID      int64
	// CgroupPath is set for a container or VM backend; RootPID for a persistent runner.
	CgroupPath string
	RootPID    int
	// JobObject names the Windows Job Object around the runner's process tree (OPS66).
	JobObject string
}

// Sampler reads one target. It returns Known false and a reason when the value cannot be read (R5.19);
// it never fails the job. It uses under 0.5 percent of one CPU core per machine (R6.4).
type Sampler interface {
	Source() Source
	// Interval is the period this reader is sampled at.
	Interval() time.Duration
	Sample(ctx context.Context, t Target, now time.Time) (Sample, string, error)
}

// Sender sends batches over the agent link.
type Sender interface {
	Send(ctx context.Context, t Target, src Source, samples []Sample, final bool, unknownReason string) error
}

// Outcome is what the final batch says besides its samples (OPS66 R5.61).
type Outcome struct {
	OOMKilled       bool
	CutShort        bool
	MemoryPeakBytes int64
}

// OutcomeNoter is a Sender that takes the outcome of a job before its final batch.
type OutcomeNoter interface {
	NoteOutcome(t Target, o Outcome)
}

// Platform is the set of readers of one operating system (OPS66 R5.62). A field is nil where the system has
// no such reader.
type Platform struct {
	Cgroup      Sampler
	ProcessTree Sampler
	JobObject   Sampler
	// Jobs creates the Job Objects of Windows; nil elsewhere.
	Jobs JobAttacher
}

// JobAttacher puts a process into a new named Job Object and closes it at the job's end.
type JobAttacher interface {
	Attach(name string, pid int) error
	Close(name string)
}
