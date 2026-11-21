package capacity

import (
	"context"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsize"
)

// Summarise is the summary of one job's samples (R5.20), written when the job ends. Only samples with
// Known true count. With none known the percentiles are 0 and UnknownReason says why (the agent's reason,
// or "no samples" when the job sent none).
func Summarise(jobID int64, jobIdent, machineID string, samples []ResourceSample, unknownReason string, completedAt time.Time) ResourceSummary {
	s := ResourceSummary{JobID: jobID, JobIdent: jobIdent, MachineID: machineID, CompletedAt: completedAt}
	var cpu, mem []int64
	for _, x := range samples {
		if !x.Known {
			continue
		}
		cpu = append(cpu, x.CPUMillis)
		mem = append(mem, x.MemoryBytes)
	}
	s.Samples = len(cpu)
	if len(cpu) == 0 {
		s.UnknownReason = unknownReason
		if s.UnknownReason == "" {
			s.UnknownReason = "no samples"
		}
		return s
	}
	s.CPUP50Millis, s.CPUP95Millis, s.CPUPeakMillis = pct(cpu, 50), pct(cpu, 95), pct(cpu, 100)
	s.MemP50, s.MemP95, s.MemPeak = pct(mem, 50), pct(mem, 95), pct(mem, 100)
	return s
}

// pct is the nearest-rank percentile of integers; p in 1..100.
func pct(v []int64, p int) int64 {
	s := append([]int64(nil), v...)
	sort.Slice(s, func(a, b int) bool { return s[a] < s[b] })
	i := (p*len(s)+99)/100 - 1
	if i < 0 {
		i = 0
	}
	return s[i]
}

// RunPeak is the measured peaks of one job run (OPS66, observability R5.60): CPU in cores x 1000 (p95 over
// the sample windows), memory in bytes (the largest reading or the peak the system kept).
type RunPeak struct {
	JobID                     int64
	Repository, Workflow      string
	JobKey, Runner, MachineID string
	RunID                     int64
	Source                    string
	PeakCPUMillis             int64
	PeakMemoryBytes           int64
	Samples                   int
	DurationMS                int64
	OOMKilled, CutShort, Good bool
	UnknownReason             string
	CompletedAt               time.Time
}

// Batch is one JobResourceSamples message of the agent, as the store takes it (OPS66). Repository, Job and
// RunID come from the agent when the jobs table does not know the job yet.
type Batch struct {
	JobID               int64
	MachineID, RunnerID string
	Repository, Job     string
	RunID               int64
	Source              string
	Samples             []ResourceSample
	Final               bool
	UnknownReason       string
	OOMKilled, CutShort bool
	MemoryPeakBytes     int64
}

// PeakReader reads the peaks of a job run and the summary of its job key (OPS66, R5.60, R5.63). found is
// false while the run has no final batch.
type PeakReader interface {
	Peaks(ctx context.Context, jobID int64) (run RunPeak, key jobsize.Summary, found bool, err error)
}
