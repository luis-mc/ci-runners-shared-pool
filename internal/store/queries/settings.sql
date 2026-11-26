-- Settings, the audit log and backups (observability R5.24 to R5.32,
-- operations R5.21 to R5.27).

-- name: GetSetting :one
SELECT value FROM settings WHERE key = ?;

-- name: ListSettings :many
SELECT * FROM settings ORDER BY key;

-- name: SetSetting :exec
INSERT INTO settings (key, value, updated_by) VALUES (?, ?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), updated_by = excluded.updated_by;

-- name: InsertAuditEvent :one
INSERT INTO audit_log (at, actor, actor_id, source, action, target_type, subject, detail, result, prev_hash, hash)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: LastAuditHash :one
SELECT hash FROM audit_log ORDER BY id DESC LIMIT 1;

-- name: ListAuditChain :many
SELECT * FROM audit_log WHERE id > ? ORDER BY id ASC LIMIT ?;

-- name: ListAuditEvents :many
SELECT * FROM audit_log
WHERE (sqlc.narg(actor) IS NULL OR actor = sqlc.narg(actor))
  AND (sqlc.narg(action) IS NULL OR action = sqlc.narg(action))
  AND (sqlc.narg(target_type) IS NULL OR target_type = sqlc.narg(target_type))
  AND (sqlc.narg(target_id) IS NULL OR subject = sqlc.narg(target_id))
  AND (sqlc.narg(from_at) IS NULL OR at >= sqlc.narg(from_at))
  AND (sqlc.narg(to_at) IS NULL OR at <= sqlc.narg(to_at))
  AND id < sqlc.arg(before_id)
ORDER BY id DESC
LIMIT sqlc.arg(page_size);

-- name: DeleteAuditBefore :execrows
DELETE FROM audit_log WHERE at < ?;

-- name: InsertBackup :exec
INSERT INTO backups (id, file, schema_version) VALUES (?, ?, ?);

-- name: FinishBackup :exec
UPDATE backups SET state = ?, size_bytes = ?, check_result = ?, checked_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?;

-- name: GetBackup :one
SELECT * FROM backups WHERE id = ?;

-- name: ListBackups :many
SELECT * FROM backups WHERE deleted_at IS NULL ORDER BY created_at DESC;

-- name: MarkBackupDeleted :exec
UPDATE backups SET deleted_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?;

-- name: InsertRestoreDrill :exec
INSERT INTO restore_drills (id, backup_id) VALUES (?, ?);

-- name: FinishRestoreDrill :exec
UPDATE restore_drills SET state = ?, detail = ?, finished_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?;

-- name: LastRestoreDrill :one
SELECT * FROM restore_drills ORDER BY started_at DESC LIMIT 1;
