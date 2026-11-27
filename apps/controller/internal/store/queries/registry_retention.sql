-- Retention (REG6 owns this file; docs/features/registry R5.41 to R5.52): settings, recommendations, delete, cleanup.

-- name: GetRegistrySettingsRow :one
SELECT * FROM registry_settings WHERE id = 1;

-- name: UpdateRegistrySettingsRow :exec
UPDATE registry_settings SET folder = ?, standard_budget = ?, custom_budget = ?, unused_days = ?, keep_old_days = ?,
    variant_gone_days = ?, variant_unused_days = ?, cleanup_weekday = ?, cleanup_hour = ?, updated_by = ?,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = 1;

-- name: SetRegistryReadOnly :exec
UPDATE registry_settings SET read_only = ? WHERE id = 1;

-- name: ListOpenRegistryRecommendations :many
SELECT * FROM registry_recommendations WHERE state = 'open' ORDER BY decider DESC, created_at, id;

-- name: UpsertRegistryRecommendation :exec
INSERT INTO registry_recommendations (id, rule, image_id, other_image_id, kind, frees_bytes, decider, due_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (rule, image_id, kind) WHERE state = 'open' DO UPDATE SET
    other_image_id = excluded.other_image_id, frees_bytes = excluded.frees_bytes, decider = excluded.decider,
    due_at = excluded.due_at, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now');

-- name: CloseRegistryRecommendation :execrows
UPDATE registry_recommendations SET state = ?, closed_why = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ? AND state = 'open';

-- name: ListRegistryUnusedCandidates :many
SELECT i.* FROM registry_images i
WHERE i.state IN ('current', 'variant') AND (i.last_pull_at = '' OR i.last_pull_at < ?)
  AND NOT EXISTS (SELECT 1 FROM registry_pointers p WHERE p.image_id = i.id);

-- name: ListRegistrySupersededDue :many
SELECT * FROM registry_images WHERE state = 'superseded' AND keep_until <> '' AND keep_until <= ?;

-- name: ListRegistryVariantsDue :many
SELECT * FROM registry_images
WHERE state = 'variant'
  AND ((branch_gone_at <> '' AND branch_gone_at <= sqlc.arg(gone_before))
       OR (last_pull_at <> '' AND last_pull_at <= sqlc.arg(unused_before))
       OR (last_pull_at = '' AND promoted_at <= sqlc.arg(unused_before)));

-- name: MarkRegistryImageDeleted :execrows
UPDATE registry_images SET state = 'deleted', deleted_at = ?, deleted_by = ?,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ? AND state <> 'deleted';

-- name: ListDanglingRegistryLayers :many
SELECT l.* FROM registry_layers l
WHERE NOT EXISTS (
    SELECT 1 FROM registry_image_layers il JOIN registry_images i ON i.id = il.image_id
    WHERE il.layer_digest = l.digest AND i.state <> 'deleted');

-- name: DeleteRegistryLayer :exec
DELETE FROM registry_layers WHERE digest = ?;

-- name: InsertRegistryCleanup :one
INSERT INTO registry_cleanups (trigger, started_by) VALUES (?, ?) RETURNING id;

-- name: FinishRegistryCleanup :exec
UPDATE registry_cleanups SET state = ?, freed_bytes = ?, error = ?, finished_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ?;

-- name: LastRegistryCleanup :one
SELECT * FROM registry_cleanups ORDER BY id DESC LIMIT 1;

-- name: GetRegistryCleanup :one
SELECT * FROM registry_cleanups WHERE id = ?;

-- name: ListOldStagedRegistryImages :many
SELECT * FROM registry_images
WHERE state IN ('staged', 'refused') AND pushed_at <> '' AND pushed_at < ?
ORDER BY pushed_at, id;

-- name: ListRunningJobsOfRegistryImage :many
SELECT DISTINCT j.repository, j.workflow, j.name, j.started_at
FROM registry_pulls p JOIN jobs j ON CAST(j.id AS TEXT) = p.job_id
WHERE p.image_id = ? AND j.state = 'running'
ORDER BY j.started_at, j.name;

-- name: ListInFlightPullMachinesOfRegistryImage :many
SELECT DISTINCT machine_id FROM registry_pulls
WHERE image_id = ? AND state IN ('pulling', 'waiting_controller')
ORDER BY machine_id;

-- name: CountLiveRegistryTokenLeasesNaming :one
SELECT count(*) FROM registry_token_leases
WHERE expires_at > ? AND instr(names, '"' || CAST(sqlc.arg(name) AS TEXT) || '"') > 0;

-- name: SumRegistrySharedLayers :many
SELECT l.charged_to, CAST(COALESCE(sum(l.size_bytes), 0) AS INTEGER) AS bytes FROM registry_layers l
WHERE EXISTS (
    SELECT 1 FROM registry_image_layers il JOIN registry_images i ON i.id = il.image_id
    WHERE il.layer_digest = l.digest AND i.state <> 'deleted' AND i.kind <> l.charged_to)
GROUP BY l.charged_to;
