-- Debug access to a running job (docs/features/debug-access, CRSP331).

-- name: GetDebugRepoSettings :one
SELECT * FROM debug_repo_settings WHERE repository_id = ?;

-- name: UpsertDebugRepoSettings :exec
INSERT INTO debug_repo_settings (repository_id, repository, enabled, who, limit_min, idle_min, hold_min, persistent_ok, updated_at, updated_by)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (repository_id) DO UPDATE SET repository = excluded.repository, enabled = excluded.enabled, who = excluded.who,
    limit_min = excluded.limit_min, idle_min = excluded.idle_min, hold_min = excluded.hold_min,
    persistent_ok = excluded.persistent_ok, updated_at = excluded.updated_at, updated_by = excluded.updated_by;

-- name: InsertDebugSession :exec
INSERT INTO debug_session (id, job_id, repository_id, repository, machine_id, runner_name, backend, isolation, os,
    user_login, user_id, reason, source, source_addr, opened_at, ends_at, last_input_at, limit_min, idle_min, extended, recorded)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetDebugSession :one
SELECT * FROM debug_session WHERE id = ?;

-- name: ListOpenDebugSessions :many
SELECT * FROM debug_session WHERE ended_at = '' ORDER BY opened_at;

-- name: ListDebugSessionsOfJob :many
SELECT * FROM debug_session WHERE job_id = ? ORDER BY opened_at DESC LIMIT ?;

-- name: ListDebugSessions :many
SELECT * FROM debug_session ORDER BY opened_at DESC LIMIT ?;

-- name: ExtendDebugSession :execrows
UPDATE debug_session SET ends_at = ?, extended = 1 WHERE id = ? AND ended_at = '' AND extended = 0;

-- name: TouchDebugSession :execrows
UPDATE debug_session SET last_input_at = ? WHERE id = ? AND ended_at = '';

-- name: SetDebugSessionEndsAt :execrows
UPDATE debug_session SET ends_at = ? WHERE id = ? AND ended_at = '' AND ends_at > ?;

-- name: EndDebugSession :execrows
UPDATE debug_session SET ended_at = ?, end_reason = ?, ended_by = ? WHERE id = ? AND ended_at = '';

-- name: AppendDebugTranscript :exec
INSERT INTO debug_transcript (session_id, text, bytes, truncated, expires_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (session_id) DO UPDATE SET text = debug_transcript.text || excluded.text,
    bytes = debug_transcript.bytes + excluded.bytes, truncated = MAX(debug_transcript.truncated, excluded.truncated),
    updated_at = excluded.updated_at;

-- name: GetDebugTranscript :one
SELECT * FROM debug_transcript WHERE session_id = ?;

-- name: DeleteExpiredDebugTranscripts :execrows
DELETE FROM debug_transcript WHERE expires_at <= ?;

-- name: SumDebugTranscriptBytes :one
SELECT CAST(COALESCE(SUM(bytes), 0) AS INTEGER) AS total FROM debug_transcript;

-- name: OldestDebugTranscript :one
SELECT session_id, bytes FROM debug_transcript ORDER BY updated_at ASC LIMIT 1;

-- name: DeleteDebugTranscript :exec
DELETE FROM debug_transcript WHERE session_id = ?;
