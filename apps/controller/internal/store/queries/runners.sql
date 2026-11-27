-- Runners, their state changes and usage (runners R5.2 to R5.6, R5.12).

-- name: InsertRunner :exec
INSERT INTO runners (id, name, machine_id, os, arch, kind, backend, isolation, labels, scope_kind,
    repository_id, organization, runner_group, runner_version, state, managed, service_name, account, folder,
    route_pool, route_queue, size_class, keep_dependencies, dependency_cache_bytes, unclean_reason)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetRunner :one
SELECT * FROM runners WHERE id = ?;

-- name: GetRunnerByService :one
SELECT * FROM runners WHERE machine_id = ? AND service_name = ?;

-- name: ListRunners :many
SELECT * FROM runners
WHERE (sqlc.narg(machine_id) IS NULL OR machine_id = sqlc.narg(machine_id))
  AND (sqlc.narg(os) IS NULL OR os = sqlc.narg(os))
  AND (sqlc.narg(state) IS NULL OR state = sqlc.narg(state))
  AND (sqlc.narg(managed) IS NULL OR managed = sqlc.narg(managed))
ORDER BY machine_id, name;

-- name: SetRunnerState :exec
UPDATE runners SET state = ?, current_job_id = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?;

-- name: SetRunnerRegistration :exec
UPDATE runners SET github_runner_id = ?, runner_version = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?;

-- name: SetRunnerHygiene :exec
UPDATE runners SET keep_dependencies = ?, dependency_cache_bytes = ?, unclean_reason = ?,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?;

-- name: DeleteRunner :exec
DELETE FROM runners WHERE id = ?;

-- name: InsertRunnerEvent :exec
INSERT INTO runner_events (runner_id, from_state, to_state, detail) VALUES (?, ?, ?, ?);

-- name: UpsertRunnerUsage :exec
INSERT INTO runner_usage (runner_id, repository, run_id, run_attempt, job, cpu_ms,
    memory_peak_bytes, memory_median_bytes, disk_used_bytes, wall_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (runner_id, repository, run_id, run_attempt, job) DO UPDATE SET
    cpu_ms = excluded.cpu_ms, memory_peak_bytes = excluded.memory_peak_bytes,
    memory_median_bytes = excluded.memory_median_bytes, disk_used_bytes = excluded.disk_used_bytes,
    wall_ms = excluded.wall_ms, reported_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now');

-- name: DeleteRunnerUsageBefore :exec
DELETE FROM runner_usage WHERE reported_at < ?;
