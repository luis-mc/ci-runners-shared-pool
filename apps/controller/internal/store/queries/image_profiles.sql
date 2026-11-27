-- Image profiles (IMG3 owns this file; docs/features/registry R5.77 to R5.80).

-- name: UpsertImageProfile :exec
INSERT INTO image_profiles (id, kind, repository, title, base_name, base_digest, pins, toolchains, services, labels, stacks, built_in)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
    kind = excluded.kind, repository = excluded.repository, title = excluded.title, base_name = excluded.base_name,
    base_digest = excluded.base_digest, pins = excluded.pins, toolchains = excluded.toolchains,
    services = excluded.services, labels = excluded.labels, stacks = excluded.stacks,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now');

-- name: InsertImageProfileIfAbsent :exec
INSERT INTO image_profiles (id, kind, repository, title, base_name, base_digest, pins, toolchains, services, labels, stacks, built_in)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO NOTHING;

-- name: GetImageProfile :one
SELECT * FROM image_profiles WHERE id = ?;

-- name: ListImageProfiles :many
SELECT * FROM image_profiles ORDER BY kind, id;

-- name: DeleteImageProfile :execrows
DELETE FROM image_profiles WHERE id = ? AND built_in = 0;

-- name: SetRepositoryProfile :exec
INSERT INTO repository_profiles (repository, profile_id) VALUES (?, ?)
ON CONFLICT (repository) DO UPDATE SET profile_id = excluded.profile_id,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now');

-- name: GetRepositoryProfile :one
SELECT profile_id FROM repository_profiles WHERE repository = ?;

-- name: ClearRepositoryProfile :execrows
DELETE FROM repository_profiles WHERE repository = ?;

-- name: ListRepositoryProfiles :many
SELECT * FROM repository_profiles ORDER BY repository;
