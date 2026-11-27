-- Build records (IMG1 owns this file; RES19 IMG-1, docs/features/registry R5.68 to R5.72). REG2 calls
-- SetImageBuildPushState only.

-- name: InsertImageBuild :exec
INSERT INTO image_builds (id, machine_id, profile_id, arch, image_tag, reason, typical_seconds) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetImageBuild :one
SELECT * FROM image_builds WHERE id = ?;

-- name: SetImageBuildProgress :exec
UPDATE image_builds SET state = ?, step = ?, steps_total = ?, step_name = ?, started_at = ?,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ?;

-- name: FinishImageBuild :exec
UPDATE image_builds SET state = ?, size_bytes = ?, failure_step = ?, failure_class = ?, failure_why = ?, log_tail = ?,
    finished_at = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ?;

-- name: SetImageBuildPushState :exec
UPDATE image_builds SET push_state = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?;

-- name: ListImageBuildsInFlight :many
SELECT * FROM image_builds WHERE state IN ('queued', 'building') OR push_state IN ('pushing', 'waiting_controller')
ORDER BY queued_at;

-- name: ListImageBuildsOfMachine :many
SELECT * FROM image_builds WHERE machine_id = ? ORDER BY queued_at DESC, rowid DESC LIMIT ?;

-- name: TypicalImageBuildSeconds :one
SELECT coalesce(avg(julianday(finished_at) - julianday(started_at)) * 86400.0, 0.0) AS seconds
FROM (SELECT started_at, finished_at FROM image_builds
      WHERE profile_id = ? AND arch = ? AND state = 'done' AND started_at <> '' AND finished_at <> ''
      ORDER BY finished_at DESC LIMIT 5);

-- name: ListImageBuilds :many
SELECT * FROM image_builds ORDER BY queued_at DESC, rowid DESC LIMIT ?;
