-- Registry core (REG1 owns this file; docs/features/registry R5.1 to R5.12): staging, promotion, immutable tags,
-- token leases.

-- name: InsertRegistryImage :exec
INSERT INTO registry_images (
    id, kind, name, image_tag, profile_id, arch, state, manifest_digest, staged_name, stack_hash, base_name,
    base_digest, key_input, source_repository, commit_sha, branch, pushed_by, pushed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: PromoteRegistryImage :execrows
UPDATE registry_images SET state = ?, promoted_at = ?, base_name = ?, size_bytes = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ? AND state = 'staged';

-- name: RefuseRegistryImage :execrows
UPDATE registry_images SET state = 'refused', refused_why = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ? AND state = 'staged';

-- name: GetRegistryTag :one
SELECT * FROM registry_image_tags WHERE name = ? AND tag = ?;

-- name: InsertRegistryTag :exec
INSERT INTO registry_image_tags (name, tag, image_id, manifest_digest) VALUES (?, ?, ?, ?);

-- name: InsertRegistryTokenLease :exec
INSERT INTO registry_token_leases (jti, machine_id, names, expires_at) VALUES (?, ?, ?, ?);

-- name: ListLiveRegistryTokenLeases :many
SELECT * FROM registry_token_leases WHERE expires_at > ? ORDER BY expires_at;

-- name: DeleteEndedRegistryTokenLeases :execrows
DELETE FROM registry_token_leases WHERE expires_at <= ?;

-- name: GetRegistryTokenLease :one
SELECT * FROM registry_token_leases WHERE jti = ?;

-- name: DeleteRegistryTokenLease :execrows
DELETE FROM registry_token_leases WHERE jti = ?;
