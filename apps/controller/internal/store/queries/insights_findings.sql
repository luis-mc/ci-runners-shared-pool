-- CI run insights findings, analyser runs, plans and digests (CIX8 contract, docs/features/ci-run-insights
-- R5.7 to R5.11, R5.38). The engine (internal/insights) upserts open findings and resolves the ones a full,
-- successful window no longer reports. A finding is dismissed for 7 days at most (D17, hidden_until).

-- name: GetOpenInsightFinding :one
SELECT * FROM insight_findings
WHERE kind = ? AND scope = ? AND repository_id = ? AND subject = ? AND resolved_at = '';

-- name: InsertInsightFinding :one
INSERT INTO insight_findings (kind, scope, repository_id, repository, subject, area, title, fix, snippet, evidence, minutes_saved_week, guardrail, action_kind, first_seen, last_seen)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: UpdateInsightFindingSeen :exec
UPDATE insight_findings
SET title = ?, fix = ?, snippet = ?, evidence = ?, minutes_saved_week = ?, action_kind = ?, last_seen = ?
WHERE id = ? AND resolved_at = '';

-- name: ResolveInsightFindingsNotSeen :execrows
UPDATE insight_findings SET resolved_at = sqlc.arg(now)
WHERE kind = sqlc.arg(kind) AND resolved_at = '' AND last_seen < sqlc.arg(seen_before);

-- name: GetInsightFinding :one
SELECT * FROM insight_findings WHERE id = ?;

-- name: ListOpenInsightFindings :many
SELECT * FROM insight_findings
WHERE resolved_at = '' AND hidden_until <= sqlc.arg(now)
  AND (sqlc.arg(repository_id) = 0 OR repository_id = sqlc.arg(repository_id))
  AND (sqlc.arg(area) = '' OR area = sqlc.arg(area))
  AND (sqlc.arg(guardrail) = '' OR guardrail = sqlc.arg(guardrail))
ORDER BY minutes_saved_week DESC,
         CASE WHEN minutes_saved_week = 0 THEN COALESCE(NULLIF(guardrail, ''), 'H') ELSE '' END ASC,
         first_seen ASC, id ASC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountOpenInsightFindings :one
SELECT COUNT(*) AS findings, CAST(COALESCE(SUM(minutes_saved_week), 0) AS INTEGER) AS minutes
FROM insight_findings WHERE resolved_at = '' AND hidden_until <= sqlc.arg(now);

-- name: DeleteResolvedInsightFindingsBefore :execrows
DELETE FROM insight_findings WHERE resolved_at != '' AND resolved_at < ?;

-- name: InsertInsightAnalyserRun :one
INSERT INTO insight_analyser_runs (analyser, window_from, window_to, started_at)
VALUES (?, ?, ?, ?)
RETURNING id;

-- name: FinishInsightAnalyserRun :exec
UPDATE insight_analyser_runs SET finished_at = ?, ok = ?, found = ?, resolved = ?, error = ? WHERE id = ?;

-- name: ListInsightAnalyserRuns :many
SELECT * FROM insight_analyser_runs WHERE analyser = ? ORDER BY started_at DESC LIMIT ?;

-- name: InsertInsightPlan :exec
INSERT INTO insight_plans (id, finding_id, action_kind, changes, created_by, created_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetInsightPlan :one
SELECT * FROM insight_plans WHERE id = ?;

-- name: UpdateInsightPlanState :exec
UPDATE insight_plans SET state = ?, results = ?, started_at = ?, finished_at = ? WHERE id = ?;

-- name: UpsertInsightDigest :exec
INSERT INTO insight_digests (week, path, top, written_at) VALUES (?, ?, ?, ?)
ON CONFLICT (week) DO UPDATE SET path = excluded.path, top = excluded.top, written_at = excluded.written_at;

-- name: ListInsightDigests :many
SELECT * FROM insight_digests ORDER BY week DESC LIMIT ?;

-- name: DeleteInsightDigest :exec
DELETE FROM insight_digests WHERE week = ?;

-- CIX23 (D17): hide one open finding until a time; it shows again after that.
-- name: DismissInsightFinding :execrows
UPDATE insight_findings SET hidden_until = sqlc.arg(hidden_until) WHERE id = sqlc.arg(id) AND resolved_at = '';

-- CIX11 additions (engine, Do next list, digest).

-- name: ListResolvedInsightFindings :many
SELECT * FROM insight_findings
WHERE resolved_at != ''
  AND (sqlc.arg(repository_id) = 0 OR repository_id = sqlc.arg(repository_id))
  AND (sqlc.arg(area) = '' OR area = sqlc.arg(area))
  AND (sqlc.arg(guardrail) = '' OR guardrail = sqlc.arg(guardrail))
ORDER BY resolved_at DESC, id DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountOpenInsightFindingsFiltered :one
SELECT COUNT(*) FROM insight_findings
WHERE resolved_at = '' AND hidden_until <= sqlc.arg(now)
  AND (sqlc.arg(repository_id) = 0 OR repository_id = sqlc.arg(repository_id))
  AND (sqlc.arg(area) = '' OR area = sqlc.arg(area))
  AND (sqlc.arg(guardrail) = '' OR guardrail = sqlc.arg(guardrail));

-- name: CountResolvedInsightFindingsFiltered :one
SELECT COUNT(*) FROM insight_findings
WHERE resolved_at != ''
  AND (sqlc.arg(repository_id) = 0 OR repository_id = sqlc.arg(repository_id))
  AND (sqlc.arg(area) = '' OR area = sqlc.arg(area))
  AND (sqlc.arg(guardrail) = '' OR guardrail = sqlc.arg(guardrail));

-- name: ListInsightFindingsOpenedBetween :many
SELECT * FROM insight_findings WHERE first_seen >= sqlc.arg(from_at) AND first_seen < sqlc.arg(to_at)
ORDER BY minutes_saved_week DESC, first_seen ASC, id ASC;

-- name: ListInsightFindingsResolvedBetween :many
SELECT * FROM insight_findings WHERE resolved_at != '' AND resolved_at >= sqlc.arg(from_at) AND resolved_at < sqlc.arg(to_at)
ORDER BY minutes_saved_week DESC, resolved_at ASC, id ASC;

-- name: GetInsightDigest :one
SELECT * FROM insight_digests WHERE week = ?;
