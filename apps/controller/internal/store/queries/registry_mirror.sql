-- Pull-through mirror (REG9 owns this file; docs/features/registry R5.63 to R5.67).

-- name: GetRegistryMirrorSettings :one
SELECT * FROM registry_mirror_settings WHERE id = 1;

-- name: UpdateRegistryMirrorSettings :exec
UPDATE registry_mirror_settings SET enabled = ?, upstreams = ?, folder = ?, budget_bytes = ?, tag_recheck_minutes = ?,
    updated_by = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = 1;
