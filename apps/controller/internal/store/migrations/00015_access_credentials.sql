-- +goose Up
-- CRSP134: scoped access credentials for automation (CI). Only the hash of the
-- value is kept; the value is shown once when it is made. A credential is
-- bound to one scope (`parity`), not to a user's role, so it keeps working
-- when the person who made it is removed, and it can be revoked on its own.
CREATE TABLE access_credentials (
    id               TEXT PRIMARY KEY,              -- public id shown in Settings
    hash             TEXT NOT NULL UNIQUE,
    name             TEXT NOT NULL,
    scope            TEXT NOT NULL CHECK (scope IN ('parity')),
    created_by_id    INTEGER NOT NULL,
    created_by_login TEXT NOT NULL,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_used_at     TEXT,
    revoked_at       TEXT
) STRICT;

-- +goose Down
DROP TABLE access_credentials;
