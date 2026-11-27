-- Replace and rollback (REG7 owns this file; docs/features/registry R5.53 to R5.58).

-- name: InsertRegistryReplacePlan :exec
INSERT INTO registry_replace_plans (id, new_image_id, old_image_id, keep_days, repositories, machines, steps_total, created_by)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetRegistryReplacePlan :one
SELECT * FROM registry_replace_plans WHERE id = ?;

-- name: GetActiveRegistryReplacePlanForImage :one
SELECT * FROM registry_replace_plans
WHERE (new_image_id = sqlc.arg(image_id) OR old_image_id = sqlc.arg(image_id)) AND state IN ('draft', 'running')
ORDER BY created_at DESC LIMIT 1;

-- name: SetRegistryReplacePlanState :execrows
UPDATE registry_replace_plans SET state = ?, steps_done = ?, error = ?, started_at = ?, finished_at = ?, keep_until = ?
WHERE id = ?;

-- name: SetRegistryReplacePlanRolledBack :execrows
UPDATE registry_replace_plans SET state = 'rolled_back', rolled_back_by = ?, rolled_back_at = ?
WHERE id = ? AND state = 'done';

-- name: SupersedeRegistryImage :execrows
UPDATE registry_images SET state = 'superseded', replaced_by_id = ?, keep_until = ?,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ? AND state IN ('current', 'variant');

-- name: RestoreRegistryImage :execrows
UPDATE registry_images SET state = 'current', replaced_by_id = '', keep_until = '',
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ? AND state = 'superseded';

-- name: ListRegistryReplacePlansByState :many
SELECT * FROM registry_replace_plans WHERE state = ? ORDER BY created_at;
