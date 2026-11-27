-- CPU and memory per job (CIX8 contract, docs/features/ci-run-insights R5.19, R5.20).

-- name: InsertJobResourceSample :exec
INSERT INTO job_resource_samples (job_id, sampled_at, machine_id, runner_id, cpu_millis, memory_bytes, known)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (job_id, sampled_at) DO NOTHING;

-- name: ListJobResourceSamples :many
SELECT * FROM job_resource_samples WHERE job_id = ? ORDER BY sampled_at;

-- name: DeleteJobResourceSamplesBefore :execrows
DELETE FROM job_resource_samples WHERE sampled_at < ?;

-- name: UpsertJobResourceSummary :exec
INSERT INTO job_resource_summary (job_id, job_ident, machine_id, samples, cpu_p50_millis, cpu_p95_millis, cpu_peak_millis, mem_p50_bytes, mem_p95_bytes, mem_peak_bytes, unknown_reason, completed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (job_id) DO UPDATE SET
    job_ident = excluded.job_ident, machine_id = excluded.machine_id, samples = excluded.samples,
    cpu_p50_millis = excluded.cpu_p50_millis, cpu_p95_millis = excluded.cpu_p95_millis, cpu_peak_millis = excluded.cpu_peak_millis,
    mem_p50_bytes = excluded.mem_p50_bytes, mem_p95_bytes = excluded.mem_p95_bytes, mem_peak_bytes = excluded.mem_peak_bytes,
    unknown_reason = excluded.unknown_reason, completed_at = excluded.completed_at;

-- name: GetJobResourceSummary :one
SELECT * FROM job_resource_summary WHERE job_id = ?;

-- name: ListJobResourceSummariesForKey :many
SELECT * FROM job_resource_summary WHERE job_ident = ? AND completed_at >= ? ORDER BY completed_at;

-- name: DeleteJobResourceSummaryBefore :execrows
DELETE FROM job_resource_summary WHERE completed_at != '' AND completed_at < ?;
