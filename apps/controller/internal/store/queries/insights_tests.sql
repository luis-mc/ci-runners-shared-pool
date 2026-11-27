-- Parsed test results (CIX8 contract, docs/features/ci-run-insights R5.33 to R5.37).

-- name: InsertTestResult :exec
INSERT INTO test_results (repository_id, run_id, run_attempt, job_id, job_ident, head_sha, report_id, format, test_id, suite, name, file, kind, result, duration_ms, retries, assertions, finished_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: DeleteTestResultsForReport :exec
DELETE FROM test_results WHERE report_id = ?;

-- name: ListTestResultsBetween :many
SELECT * FROM test_results
WHERE repository_id = ? AND finished_at >= sqlc.arg(from_at) AND finished_at < sqlc.arg(to_at)
ORDER BY id
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: ListTestResultsForRun :many
SELECT * FROM test_results WHERE repository_id = ? AND run_id = ? ORDER BY job_ident, test_id, run_attempt;

-- name: DeleteTestResultsBefore :execrows
DELETE FROM test_results WHERE finished_at < ?;

-- name: DeleteTestResultsForJobLog :exec
-- Rows read from a job log have report_id 0; they are replaced per job attempt, never all at once.
DELETE FROM test_results WHERE report_id = 0 AND repository_id = ? AND run_id = ? AND run_attempt = ? AND job_id = ? AND format = ?;
