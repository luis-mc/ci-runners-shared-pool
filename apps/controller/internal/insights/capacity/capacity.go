// Package capacity sizes the pool from real demand (docs/features/ci-run-insights R5.15 to R5.18, R5.20):
// demand per label set, the G/G/c wait estimate with more slots, pool size and job slots per repository
// advice, and the CPU and memory summaries per job that size a slot. CIX8 contract: types and interfaces only.
package capacity

import (
	"context"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

// LabelSetLoad is the measured demand of one label set over a window (R5.15).
type LabelSetLoad struct {
	Labels       []string
	Slots        int     // slots that can take this label set
	Utilisation  float64 // busy slot minutes / available slot minutes; may exceed 1 while the queue grows
	JobsPerHour  float64
	MeanService  time.Duration // mean of run + hold
	CVArrivals   float64       // coefficient of variation of the gaps between arrivals
	CVService    float64       // coefficient of variation of run + hold
	P50Wait      time.Duration
	P95Wait      time.Duration
	P95WaitByDay []DayValue
}

// DayValue is one day's value (UTC day, year-month-day).
type DayValue struct {
	Day   string
	Value float64
}

// Estimate is the wait with extra slots (R5.16). Saturated means utilisation would stay at or over 1: no
// wait is given and MinSlots is the smallest slot count with utilisation under 0.85.
type Estimate struct {
	ExtraSlots  int
	MeanWait    time.Duration
	P95Wait     time.Duration
	Utilisation float64
	Saturated   bool
	MinSlots    int
}

// Estimator is the queue model of R5.16: Allen-Cunneen for G/G/c with Sakasegawa's waiting probability,
// Wq ≈ (ca² + cs²)/2 · ρ^(√(2(c+1)) − 1) / (c(1 − ρ)) · E[S]. The p95 is the mean scaled by the ratio
// p95/mean seen now. Pure: no clock, no I/O.
type Estimator interface {
	Estimate(load LabelSetLoad, extraSlots int) Estimate
}

// DemandReader measures LabelSetLoad from the jobs, slot and machine history.
type DemandReader interface {
	Demand(ctx context.Context, w insights.Window) ([]LabelSetLoad, error)
}

// Headroom is what one machine can still give, from measured load and job resources: the budget is 85
// percent memory and 60 percent CPU (R5.17, R5.18).
type Headroom struct {
	MachineID     string
	MachineName   string
	Labels        []string
	Slots         int
	FreeMemory    int64 // bytes under the 85 percent budget, at the p95 of measured load
	FreeCPUMillis int64 // CPU under the 60 percent budget, cores x 1000
	SlotMemory    int64 // p95 memory of the jobs a slot takes (job_resource_summary)
	SlotCPUMillis int64
	MaxExtraSlots int
}

// HeadroomReader measures every online machine's headroom.
type HeadroomReader interface {
	Headroom(ctx context.Context, w insights.Window) ([]Headroom, error)
}

// RepoLoad is one repository's use of the pool (R5.18).
type RepoLoad struct {
	RepositoryID  int64
	Repository    string
	ParallelLimit int           // repository_parallel_limit, 0 for none
	LimitWait     time.Duration // wait with the reason "repository parallel limit" while slots were free
	P95Concurrent int
	ShareOfSlots  float64 // share of busy slot minutes
	OthersWaited  time.Duration
}

// RepoLoadReader measures RepoLoad for every repository that uses the pool.
type RepoLoadReader interface {
	RepoLoads(ctx context.Context, w insights.Window) ([]RepoLoad, error)
}

// ResourceSummary is the summary of one job's samples (R5.20); a value in millis is CPU cores x 1000.
type ResourceSummary struct {
	JobID                                     int64
	JobIdent                                  string
	MachineID                                 string
	Samples                                   int
	CPUP50Millis, CPUP95Millis, CPUPeakMillis int64
	MemP50, MemP95, MemPeak                   int64
	UnknownReason                             string
	CompletedAt                               time.Time
}

// ResourceSample is one stored sample.
type ResourceSample struct {
	At          time.Time
	CPUMillis   int64
	MemoryBytes int64
	Known       bool
}

// Resources stores the agent's samples and writes the summary when the job ends (R5.19, R5.20).
type Resources interface {
	// Add stores a batch of samples of one job; final true writes the summary.
	Add(ctx context.Context, jobID int64, machineID, runnerID string, samples []ResourceSample, final bool, unknownReason string) error
	// Job returns the samples and the summary of one job (getJobResources).
	Job(ctx context.Context, jobID int64) ([]ResourceSample, ResourceSummary, error)
}

// The capacity analyser implements insights.Analyser with these kinds. KindPoolSize has the action
// machine_slots only when a machine's headroom takes the slot; KindRepoSlots has repo_parallel_limit.
var Kinds = []insights.Kind{insights.KindPoolSize, insights.KindRepoSlots}
