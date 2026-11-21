-- The measured peaks of each job run (OPS66, docs/features/observability R5.60 to R5.64).

-- name: UpsertJobRunPeak :exec
INSERT INTO job_run_peaks (job_id, repository, workflow, job_ident, runner, machine_id, run_id, source, peak_cpu_millis, peak_memory_bytes, samples, duration_ms, oom_killed, cut_short, good, unknown_reason, completed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (job_id) DO UPDATE SET
    repository = excluded.repository, workflow = excluded.workflow, job_ident = excluded.job_ident, runner = excluded.runner,
    machine_id = excluded.machine_id, run_id = excluded.run_id, source = excluded.source,
    peak_cpu_millis = excluded.peak_cpu_millis, peak_memory_bytes = excluded.peak_memory_bytes,
    samples = excluded.samples, duration_ms = excluded.duration_ms, oom_killed = excluded.oom_killed,
    cut_short = excluded.cut_short, good = excluded.good, unknown_reason = excluded.unknown_reason,
    completed_at = excluded.completed_at;

-- name: GetJobRunPeak :one
SELECT * FROM job_run_peaks WHERE job_id = ?;

-- name: ListGoodJobRunPeaksForKey :many
SELECT peak_cpu_millis, peak_memory_bytes FROM job_run_peaks
WHERE repository = ? AND workflow = ? AND job_ident = ? AND good = 1
ORDER BY completed_at DESC, job_id DESC
LIMIT ?;

-- name: DeleteJobRunPeaksBefore :execrows
DELETE FROM job_run_peaks WHERE completed_at < ?;

-- name: GetJobIdentity :one
SELECT repository, workflow, job_ident, runner_name, machine_id, run_id FROM jobs WHERE id = ?;
