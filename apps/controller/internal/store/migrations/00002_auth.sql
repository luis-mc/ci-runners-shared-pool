-- +goose Up
-- Milestone 1: GitHub App record, users, sessions, CLI tokens, setup codes and
-- tray links (github-integration R5.1, R5.9 to R5.15; tray R5.6 to R5.8, R6.1).
-- No secret is stored: codes, session ids and tokens are kept only as SHA-256
-- hashes; the App private key, client secret and webhook secret live in the
-- OS secret store (github-integration R6.1).

CREATE TABLE github_app (
    id             INTEGER PRIMARY KEY,           -- the GitHub App id
    slug           TEXT NOT NULL,
    client_id      TEXT NOT NULL,                 -- public
    owner_login    TEXT NOT NULL,
    owner_type     TEXT NOT NULL CHECK (owner_type IN ('User', 'Organization')),
    html_url       TEXT NOT NULL,
    webhook_active INTEGER NOT NULL DEFAULT 0 CHECK (webhook_active IN (0, 1)),
    permissions    TEXT NOT NULL DEFAULT '{}',    -- JSON, as granted
    gone_at        TEXT,                          -- set when GitHub says the App is gone (R5.6)
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

CREATE TABLE setup_codes (
    code_hash  TEXT PRIMARY KEY,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    expires_at TEXT NOT NULL,
    used_at    TEXT
) STRICT;

CREATE TABLE users (
    github_user_id  INTEGER PRIMARY KEY,          -- identity is the numeric id (R5.9)
    login           TEXT NOT NULL,
    role            TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'viewer')),
    added_by        TEXT NOT NULL DEFAULT 'system',
    added_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_sign_in_at TEXT
) STRICT;

-- Exactly one owner (R5.10).
CREATE UNIQUE INDEX users_one_owner ON users (role) WHERE role = 'owner';

CREATE TABLE user_preferences (
    github_user_id INTEGER PRIMARY KEY REFERENCES users (github_user_id) ON DELETE CASCADE,
    theme          TEXT NOT NULL DEFAULT 'system' CHECK (theme IN ('system', 'dark', 'light')),
    updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

-- Web sessions end only at sign-out (owner decision for M1).
CREATE TABLE sessions (
    id_hash        TEXT PRIMARY KEY,
    github_user_id INTEGER NOT NULL REFERENCES users (github_user_id) ON DELETE CASCADE,
    csrf_hash      TEXT NOT NULL,
    source         TEXT NOT NULL DEFAULT 'web' CHECK (source IN ('web', 'tray')),
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_seen_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    ended_at       TEXT
) STRICT;

CREATE INDEX sessions_user ON sessions (github_user_id);

CREATE TABLE cli_tokens (
    id             TEXT PRIMARY KEY,              -- public id shown in Settings
    hash           TEXT NOT NULL UNIQUE,
    github_user_id INTEGER NOT NULL REFERENCES users (github_user_id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    expires_at     TEXT NOT NULL,                 -- 30 days (R5.15)
    last_used_at   TEXT,
    revoked_at     TEXT
) STRICT;

CREATE INDEX cli_tokens_user ON cli_tokens (github_user_id);

-- One-time tray sign-in links: 60 seconds, used once (tray R6.1).
CREATE TABLE link_codes (
    code_hash      TEXT PRIMARY KEY,
    github_user_id INTEGER NOT NULL REFERENCES users (github_user_id) ON DELETE CASCADE,
    purpose        TEXT NOT NULL CHECK (purpose IN ('sign_in', 'bind_tray')),
    target_path    TEXT NOT NULL DEFAULT '/',
    expires_at     TEXT NOT NULL,
    used_at        TEXT
) STRICT;

-- The GitHub user bound to an OS user of a machine (owner decision for M1).
CREATE TABLE tray_bindings (
    machine_id     TEXT NOT NULL,
    os_user_id     TEXT NOT NULL,                 -- Windows SID or Unix uid
    github_user_id INTEGER NOT NULL REFERENCES users (github_user_id) ON DELETE CASCADE,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (machine_id, os_user_id)
) STRICT;

-- +goose Down
DROP TABLE tray_bindings;
DROP TABLE link_codes;
DROP TABLE cli_tokens;
DROP TABLE sessions;
DROP TABLE user_preferences;
DROP TABLE users;
DROP TABLE setup_codes;
DROP TABLE github_app;
