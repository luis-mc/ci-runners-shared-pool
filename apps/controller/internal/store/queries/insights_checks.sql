-- Workflow file checks and guardrail reports (CIX8 contract, docs/features/ci-run-insights R5.29 to R5.32).

-- name: UpsertWorkflowCheck :exec
INSERT INTO workflow_checks (repository_id, head_sha, workflow, check_id, job, status, line, detail, guardrail, default_branch, checked_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (repository_id, head_sha, workflow, check_id, job) DO UPDATE SET
    status = excluded.status, line = excluded.line, detail = excluded.detail, guardrail = excluded.guardrail,
    default_branch = excluded.default_branch, checked_at = excluded.checked_at;

-- name: HasWorkflowChecksForCommit :one
SELECT COUNT(*) FROM workflow_checks WHERE repository_id = ? AND head_sha = ?;

-- name: LatestDefaultBranchCheckedCommit :one
SELECT head_sha FROM workflow_checks
WHERE repository_id = ? AND default_branch = 1
ORDER BY checked_at DESC LIMIT 1;

-- name: ListWorkflowChecksForCommit :many
SELECT * FROM workflow_checks WHERE repository_id = ? AND head_sha = ? ORDER BY workflow, job, check_id;

-- name: DeleteWorkflowChecksBefore :execrows
-- The caller passes the latest default-branch commit of the repository, which is always kept (R6.2).
DELETE FROM workflow_checks WHERE repository_id = ? AND checked_at < ? AND head_sha != sqlc.arg(keep_sha);

-- name: UpsertGuardrailReport :exec
INSERT INTO guardrail_reports (repository_id, run_id, head_sha, guardrail, source, state, count, known_count, reason, breaks, reported_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (repository_id, run_id, guardrail, source) DO UPDATE SET
    head_sha = excluded.head_sha, state = excluded.state, count = excluded.count, known_count = excluded.known_count,
    reason = excluded.reason, breaks = excluded.breaks, reported_at = excluded.reported_at;

-- name: ListGuardrailReportsSince :many
SELECT * FROM guardrail_reports WHERE repository_id = ? AND reported_at >= ? ORDER BY guardrail, reported_at;

-- name: DeleteGuardrailReportsBefore :execrows
DELETE FROM guardrail_reports WHERE reported_at < ?;

-- name: ListGuardrailStatusRows :many
-- Guardrail rows of one repository (repository_id > 0) or of all (0) since a time, with the repository name (R5.32).
SELECT g.repository_id, r.full_name, g.run_id, g.head_sha, g.guardrail, g.source, g.state, g.count, g.known_count,
       g.reason, g.breaks, g.reported_at
FROM guardrail_reports g JOIN repositories r ON r.id = g.repository_id
WHERE (sqlc.arg(repository_id) = 0 OR g.repository_id = sqlc.arg(repository_id)) AND g.reported_at >= sqlc.arg(since)
ORDER BY g.repository_id, g.guardrail, g.reported_at;
