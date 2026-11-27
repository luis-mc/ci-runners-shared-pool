-- Stored CI logs and report files (CIX8 contract, docs/features/ci-run-insights R5.2, R5.3, R5.6; owner
-- decision D4). The files live under the CI data folder; these rows index them for retention and the caps.

-- name: UpsertCIJobLog :exec
INSERT INTO ci_job_logs (job_id, repository_id, run_id, run_attempt, path, bytes, raw_bytes, lines, sha256, state, reason, fetched_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (job_id) DO UPDATE SET
    path = excluded.path, bytes = excluded.bytes, raw_bytes = excluded.raw_bytes, lines = excluded.lines,
    sha256 = excluded.sha256, state = excluded.state, reason = excluded.reason,
    fetched_at = excluded.fetched_at, expires_at = excluded.expires_at;

-- name: GetCIJobLog :one
SELECT * FROM ci_job_logs WHERE job_id = ?;

-- name: ListExpiredCIJobLogs :many
SELECT * FROM ci_job_logs WHERE state = 'stored' AND expires_at != '' AND expires_at < ? ORDER BY expires_at LIMIT ?;

-- name: ListCIJobLogsOldestFirst :many
SELECT * FROM ci_job_logs WHERE repository_id = ? AND state = 'stored' ORDER BY fetched_at ASC LIMIT ?;

-- name: MarkCIJobLogExpired :exec
UPDATE ci_job_logs SET state = 'expired', path = '', bytes = 0 WHERE job_id = ?;

-- name: InsertCIReportFile :one
INSERT INTO ci_report_files (repository_id, run_id, run_attempt, artifact_id, artifact_name, member, format, path, bytes, state, reason, fetched_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (repository_id, artifact_id, member) DO UPDATE SET
    format = excluded.format, path = excluded.path, bytes = excluded.bytes, state = excluded.state,
    reason = excluded.reason, fetched_at = excluded.fetched_at, expires_at = excluded.expires_at
RETURNING id;

-- name: ListCIReportFilesForRun :many
SELECT * FROM ci_report_files WHERE repository_id = ? AND run_id = ? ORDER BY id;

-- name: ListExpiredCIReportFiles :many
SELECT * FROM ci_report_files WHERE state IN ('stored', 'parsed') AND expires_at != '' AND expires_at < ? ORDER BY expires_at LIMIT ?;

-- name: MarkCIReportFileExpired :exec
UPDATE ci_report_files SET state = 'expired', path = '', bytes = 0 WHERE id = ?;

-- name: CIDataBytesByRepository :many
SELECT repository_id, CAST(SUM(bytes) AS INTEGER) AS bytes FROM (
    SELECT repository_id, bytes FROM ci_job_logs WHERE state = 'stored'
    UNION ALL
    SELECT repository_id, bytes FROM ci_report_files WHERE state IN ('stored', 'parsed')
) GROUP BY repository_id;

-- name: ListCIReportFilesOldestFirst :many
SELECT * FROM ci_report_files WHERE repository_id = ? AND state IN ('stored', 'parsed') ORDER BY fetched_at ASC, id ASC LIMIT ?;

-- name: GetCIReportFile :one
SELECT * FROM ci_report_files WHERE repository_id = ? AND artifact_id = ? AND member = ?;
