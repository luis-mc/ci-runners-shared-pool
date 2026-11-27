package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/capacity"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// ResourcesStore is capacity.Resources over the job_resource_* tables (R5.19, R5.20).
type ResourcesStore struct{ *Store }

var _ capacity.Resources = (*ResourcesStore)(nil)

// Resources returns the CPU and memory view of the store.
func (s *Store) Resources() *ResourcesStore { return &ResourcesStore{s} }

// Add stores a batch of samples of one job. With final true it also writes the summary from every stored
// sample of the job; unknownReason is kept in the summary when no sample was known. A repeated sample time
// is ignored, so a resent batch changes nothing.
func (r *ResourcesStore) Add(ctx context.Context, jobID int64, machineID, runnerID string, samples []capacity.ResourceSample, final bool, unknownReason string) error {
	for _, x := range samples {
		known := int64(0)
		if x.Known {
			known = 1
		}
		if err := r.q.InsertJobResourceSample(ctx, sqlcgen.InsertJobResourceSampleParams{
			JobID: jobID, SampledAt: fmtTime(x.At), MachineID: machineID, RunnerID: runnerID,
			CpuMillis: x.CPUMillis, MemoryBytes: x.MemoryBytes, Known: known,
		}); err != nil {
			return fmt.Errorf("store job resource sample: %w", err)
		}
	}
	if !final {
		return nil
	}
	all, err := r.samples(ctx, jobID)
	if err != nil {
		return err
	}
	var ident string
	err = r.db.QueryRowContext(ctx, `SELECT job_ident FROM jobs WHERE id = ?`, jobID).Scan(&ident)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read job ident: %w", err)
	}
	s := capacity.Summarise(jobID, ident, machineID, all, unknownReason, time.Now().UTC())
	return r.q.UpsertJobResourceSummary(ctx, sqlcgen.UpsertJobResourceSummaryParams{
		JobID: jobID, JobIdent: s.JobIdent, MachineID: s.MachineID, Samples: int64(s.Samples),
		CpuP50Millis: s.CPUP50Millis, CpuP95Millis: s.CPUP95Millis, CpuPeakMillis: s.CPUPeakMillis,
		MemP50Bytes: s.MemP50, MemP95Bytes: s.MemP95, MemPeakBytes: s.MemPeak,
		UnknownReason: s.UnknownReason, CompletedAt: fmtTime(s.CompletedAt),
	})
}

func (r *ResourcesStore) samples(ctx context.Context, jobID int64) ([]capacity.ResourceSample, error) {
	rows, err := r.q.ListJobResourceSamples(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("list job resource samples: %w", err)
	}
	out := make([]capacity.ResourceSample, 0, len(rows))
	for _, x := range rows {
		out = append(out, capacity.ResourceSample{At: parseTime(x.SampledAt), CPUMillis: x.CpuMillis, MemoryBytes: x.MemoryBytes, Known: x.Known != 0})
	}
	return out, nil
}

// Job returns the samples and the summary of one job. A job with no summary yet returns a zero summary
// (JobID 0) and its samples, so the Job page can draw a running job.
func (r *ResourcesStore) Job(ctx context.Context, jobID int64) ([]capacity.ResourceSample, capacity.ResourceSummary, error) {
	samples, err := r.samples(ctx, jobID)
	if err != nil {
		return nil, capacity.ResourceSummary{}, err
	}
	row, err := r.q.GetJobResourceSummary(ctx, jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return samples, capacity.ResourceSummary{}, nil
	}
	if err != nil {
		return nil, capacity.ResourceSummary{}, fmt.Errorf("get job resource summary: %w", err)
	}
	return samples, capacity.ResourceSummary{
		JobID: row.JobID, JobIdent: row.JobIdent, MachineID: row.MachineID, Samples: int(row.Samples),
		CPUP50Millis: row.CpuP50Millis, CPUP95Millis: row.CpuP95Millis, CPUPeakMillis: row.CpuPeakMillis,
		MemP50: row.MemP50Bytes, MemP95: row.MemP95Bytes, MemPeak: row.MemPeakBytes,
		UnknownReason: row.UnknownReason, CompletedAt: parseTimeOpt(row.CompletedAt),
	}, nil
}
