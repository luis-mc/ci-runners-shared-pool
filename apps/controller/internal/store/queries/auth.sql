-- Sign-in, users, sessions, CLI tokens, setup codes and tray links
-- (github-integration R5.1, R5.9 to R5.15; tray R5.7, R6.1).

-- name: GetGitHubApp :one
SELECT * FROM github_app ORDER BY created_at DESC LIMIT 1;

-- name: UpsertGitHubApp :exec
INSERT INTO github_app (id, slug, client_id, owner_login, owner_type, html_url, webhook_active, permissions)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET slug = excluded.slug, client_id = excluded.client_id,
    owner_login = excluded.owner_login, owner_type = excluded.owner_type,
    html_url = excluded.html_url, webhook_active = excluded.webhook_active,
    permissions = excluded.permissions, gone_at = NULL;

-- name: InsertSetupCode :exec
INSERT INTO setup_codes (code_hash, expires_at) VALUES (?, ?);

-- name: UseSetupCode :execrows
UPDATE setup_codes SET used_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE code_hash = ? AND used_at IS NULL AND expires_at > strftime('%Y-%m-%dT%H:%M:%fZ', 'now');

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: GetUser :one
SELECT * FROM users WHERE github_user_id = ?;

-- name: GetOwner :one
SELECT * FROM users WHERE role = 'owner';

-- name: ListUsers :many
SELECT * FROM users ORDER BY role = 'owner' DESC, login;

-- name: InsertUser :exec
INSERT INTO users (github_user_id, login, role, added_by) VALUES (?, ?, ?, ?);

-- name: UpdateUserRole :execrows
UPDATE users SET role = ? WHERE github_user_id = ?;

-- name: RecordSignIn :exec
UPDATE users SET login = ?, last_sign_in_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE github_user_id = ?;

-- name: DeleteUser :execrows
DELETE FROM users WHERE github_user_id = ? AND role <> 'owner';

-- name: GetPreferences :one
SELECT * FROM user_preferences WHERE github_user_id = ?;

-- name: SetPreferences :exec
INSERT INTO user_preferences (github_user_id, theme) VALUES (?, ?)
ON CONFLICT (github_user_id) DO UPDATE SET theme = excluded.theme, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now');

-- name: InsertSession :exec
INSERT INTO sessions (id_hash, github_user_id, csrf_hash, source) VALUES (?, ?, ?, ?);

-- name: GetActiveSession :one
SELECT s.id_hash, s.csrf_hash, s.created_at, u.github_user_id, u.login, u.role
FROM sessions s JOIN users u ON u.github_user_id = s.github_user_id
WHERE s.id_hash = ? AND s.ended_at IS NULL;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id_hash = ?;

-- name: EndSession :exec
UPDATE sessions SET ended_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id_hash = ? AND ended_at IS NULL;

-- name: EndUserSessions :exec
UPDATE sessions SET ended_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE github_user_id = ? AND ended_at IS NULL;

-- name: InsertCliToken :exec
INSERT INTO cli_tokens (id, hash, github_user_id, name, expires_at) VALUES (?, ?, ?, ?, ?);

-- name: GetActiveCliToken :one
SELECT t.id, t.expires_at, u.github_user_id, u.login, u.role
FROM cli_tokens t JOIN users u ON u.github_user_id = t.github_user_id
WHERE t.hash = ? AND t.revoked_at IS NULL AND t.expires_at > strftime('%Y-%m-%dT%H:%M:%fZ', 'now');

-- name: ListCliTokens :many
SELECT t.id, t.name, t.created_at, t.expires_at, t.last_used_at, t.revoked_at,
       u.github_user_id, u.login
FROM cli_tokens t JOIN users u ON u.github_user_id = t.github_user_id
WHERE sqlc.narg(user_id) IS NULL OR t.github_user_id = sqlc.narg(user_id)
ORDER BY t.created_at DESC;

-- name: RevokeCliToken :execrows
UPDATE cli_tokens SET revoked_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ? AND revoked_at IS NULL;

-- name: RevokeUserCliTokens :exec
UPDATE cli_tokens SET revoked_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE github_user_id = ? AND revoked_at IS NULL;

-- name: InsertLinkCode :exec
INSERT INTO link_codes (code_hash, github_user_id, purpose, target_path, expires_at) VALUES (?, ?, ?, ?, ?);

-- name: UseLinkCode :one
UPDATE link_codes SET used_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE code_hash = ? AND used_at IS NULL AND expires_at > strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
RETURNING github_user_id, purpose, target_path;

-- name: GetTrayBinding :one
SELECT * FROM tray_bindings WHERE machine_id = ? AND os_user_id = ?;

-- name: UpsertTrayBinding :exec
INSERT INTO tray_bindings (machine_id, os_user_id, github_user_id) VALUES (?, ?, ?)
ON CONFLICT (machine_id, os_user_id) DO UPDATE SET github_user_id = excluded.github_user_id;
