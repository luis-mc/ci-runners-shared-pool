-- Keys, pointers and lineage (REG3 owns this file; docs/features/registry R5.21 to R5.28).

-- name: GetRegistryPointer :one
SELECT * FROM registry_pointers WHERE repository = ? AND profile_id = ? AND arch = ?;

-- name: ListRegistryPointersTo :many
SELECT * FROM registry_pointers WHERE image_id = ? ORDER BY repository;

-- name: ListRegistryPointersOfRepository :many
SELECT * FROM registry_pointers WHERE repository = ? ORDER BY profile_id, arch;

-- name: UpsertRegistryPointer :exec
INSERT INTO registry_pointers (repository, profile_id, arch, image_id, previous_image_id, moved_at, moved_by)
VALUES (?, ?, ?, ?, '', ?, ?)
ON CONFLICT (repository, profile_id, arch) DO UPDATE SET
    previous_image_id = registry_pointers.image_id, image_id = excluded.image_id,
    moved_at = excluded.moved_at, moved_by = excluded.moved_by;

-- name: SetRegistryImageLineage :exec
UPDATE registry_images SET replaces_id = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?;

-- name: ListRegistryImagesOfLineage :many
SELECT * FROM registry_images
WHERE kind = ? AND profile_id = ? AND arch = ? AND source_repository = ? AND state <> 'deleted'
ORDER BY promoted_at DESC;

-- name: SetRegistryBranchGone :execrows
UPDATE registry_images SET branch_gone_at = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE source_repository = ? AND branch = ? AND branch_gone_at = '';

-- name: ListRegistryPointersOfProfile :many
SELECT * FROM registry_pointers WHERE profile_id = ? AND arch = ? ORDER BY repository;
