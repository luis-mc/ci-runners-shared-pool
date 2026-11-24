-- +goose Up
-- CRSP227: the parity check is gone, and with it the scoped access
-- credentials that existed only for it (00015). The table is dropped; no
-- other table refers to it.
DROP TABLE access_credentials;

-- +goose Down
CREATE TABLE access_credentials (
    id               TEXT PRIMARY KEY,
    hash             TEXT NOT NULL UNIQUE,
    name             TEXT NOT NULL,
    scope            TEXT NOT NULL CHECK (scope IN ('parity')),
    created_by_id    INTEGER NOT NULL,
    created_by_login TEXT NOT NULL,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_used_at     TEXT,
    revoked_at       TEXT
) STRICT;
