package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/insights/capacity"
	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsize"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store/sqlcgen"
)

var _ capacity.PeakReader = (*ResourcesStore)(nil)

// AddBatch stores one batch of the agent. The samples go to job_resource_samples as in Add. With Final true
// it also writes the summary (R5.20) and the run's peaks (OPS66 R5.60): the shared jobsize.Measure over every
// stored sample with the flags of the final batch. The job's repository, workflow, job key and runner come
// from the jobs table when it knows the job, else from the batch (workflow stays empty).
func (r *ResourcesStore) AddBatch(ctx context.Context, b capacity.Batch) error {
	if err := r.Add(ctx, b.JobID, b.MachineID, b.RunnerID, b.Samples, b.Final, b.UnknownReason); err != nil {
		return err
	}
	if !b.Final {
		return nil
	}
	all, err := r.samples(ctx, b.JobID)
	if err != nil {
		return err
	}
	js := make([]jobsize.Sample, 0, len(all))
	for _, s := range all {
		js = append(js, jobsize.Sample{At: s.At, CPUMillis: s.CPUMillis, MemoryBytes: s.MemoryBytes, Known: s.Known})
	}
	m := jobsize.Measure(js, jobsize.Run{
		MemoryPeakBytes: b.MemoryPeakBytes, OOMKilled: b.OOMKilled, CutShort: b.CutShort, UnknownReason: b.UnknownReason,
	})
	repo, workflow, key, runner, machine, runID := b.Repository, "", b.Job, b.RunnerID, b.MachineID, b.RunID
	id, err := r.q.GetJobIdentity(ctx, b.JobID)
	switch {
	case err == nil:
		repo, workflow, runner, runID = id.Repository, id.Workflow, firstNonEmpty(id.RunnerName, runner), id.RunID
		key, machine = firstNonEmpty(id.JobIdent, key), firstNonEmpty(id.MachineID, machine)
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("read job identity: %w", err)
	}
	return r.q.UpsertJobRunPeak(ctx, sqlcgen.UpsertJobRunPeakParams{
		JobID: b.JobID, Repository: repo, Workflow: workflow, JobIdent: key, Runner: runner, MachineID: machine,
		RunID: runID, Source: b.Source, PeakCpuMillis: m.PeakCPUMillis, PeakMemoryBytes: m.PeakMemoryBytes,
		Samples: int64(m.Samples), DurationMs: m.DurationMS, OomKilled: b2i(m.OOMKilled), CutShort: b2i(m.CutShort),
		Good: b2i(m.Good()), UnknownReason: m.UnknownReason, CompletedAt: fmtTime(time.Now().UTC()),
	})
}

// Peaks implements capacity.PeakReader: the run's peaks and the summary of its job key, which reads the
// newest KeepRuns good runs of the key (R5.63).
func (r *ResourcesStore) Peaks(ctx context.Context, jobID int64) (capacity.RunPeak, jobsize.Summary, bool, error) {
	row, err := r.q.GetJobRunPeak(ctx, jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return capacity.RunPeak{}, jobsize.Summary{}, false, nil
	}
	if err != nil {
		return capacity.RunPeak{}, jobsize.Summary{}, false, fmt.Errorf("get job run peak: %w", err)
	}
	good, err := r.q.ListGoodJobRunPeaksForKey(ctx, sqlcgen.ListGoodJobRunPeaksForKeyParams{
		Repository: row.Repository, Workflow: row.Workflow, JobIdent: row.JobIdent, Limit: jobsize.KeepRuns,
	})
	if err != nil {
		return capacity.RunPeak{}, jobsize.Summary{}, false, fmt.Errorf("list good job run peaks: %w", err)
	}
	peaks := make([]jobsize.Peak, 0, len(good))
	for _, g := range good {
		peaks = append(peaks, jobsize.Peak{CPUMillis: g.PeakCpuMillis, MemoryBytes: g.PeakMemoryBytes, Good: true})
	}
	return capacity.RunPeak{
		JobID: row.JobID, Repository: row.Repository, Workflow: row.Workflow, JobKey: row.JobIdent, Runner: row.Runner,
		MachineID: row.MachineID, RunID: row.RunID, Source: row.Source, PeakCPUMillis: row.PeakCpuMillis,
		PeakMemoryBytes: row.PeakMemoryBytes, Samples: int(row.Samples), DurationMS: row.DurationMs,
		OOMKilled: row.OomKilled != 0, CutShort: row.CutShort != 0, Good: row.Good != 0,
		UnknownReason: row.UnknownReason, CompletedAt: parseTimeOpt(row.CompletedAt),
	}, jobsize.Summarise(peaks), true, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
