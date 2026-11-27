-- Job history (observability R5.7 to R5.12, R5.15, R6.11).

-- name: UpsertJobHistory :one
INSERT INTO job_history (repository_id, repository, run_id, run_attempt, github_job_id, workflow, job_name,
    head_sha, trigger, labels, created_at, html_url)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (repository_id, run_id, run_attempt, github_job_id) DO UPDATE SET
    workflow = excluded.workflow
WHERE job_history.state <> 'done'
RETURNING id;

-- name: SetJobSlot :exec
UPDATE job_history SET machine_id = ?, runner_id = ?, runner_name = ?, backend = ?, isolation = ?, os = ?,
    queued_at = ?, slot_at = ?, slot_wait_ms = ?
WHERE id = ? AND state <> 'done';

-- name: SetJobStarted :exec
UPDATE job_history SET state = 'running', started_at = ?, github_wait_ms = ? WHERE id = ? AND state <> 'done';

-- name: SetJobCompleted :exec
UPDATE job_history SET state = 'done', completed_at = ?, run_ms = ?, result = ? WHERE id = ? AND state <> 'done';

-- name: GetJobHistory :one
SELECT * FROM job_history WHERE id = ?;

-- name: ListJobHistory :many
SELECT * FROM job_history
WHERE (sqlc.narg(state) IS NULL OR state = sqlc.narg(state))
  AND (sqlc.narg(repository_id) IS NULL OR repository_id = sqlc.narg(repository_id))
  AND (sqlc.narg(machine_id) IS NULL OR machine_id = sqlc.narg(machine_id))
  AND (sqlc.narg(backend) IS NULL OR backend = sqlc.narg(backend))
  AND (sqlc.narg(result) IS NULL OR result = sqlc.narg(result))
  AND (sqlc.narg(from_at) IS NULL OR completed_at >= sqlc.narg(from_at))
  AND (sqlc.narg(to_at) IS NULL OR completed_at <= sqlc.narg(to_at))
  AND id < sqlc.arg(before_id)
ORDER BY id DESC
LIMIT sqlc.arg(page_size);

-- name: SumRunMinutesSince :many
SELECT coalesce(os, '') AS os, CAST(coalesce(sum((run_ms + 59999) / 60000), 0) AS INTEGER) AS minutes
FROM job_history WHERE state = 'done' AND completed_at >= ? AND run_ms IS NOT NULL
GROUP BY os;

-- name: DeleteJobHistoryBefore :execrows
DELETE FROM job_history WHERE state = 'done' AND completed_at < ?;
