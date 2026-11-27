-- +goose Up
-- Notification channels (notifications R5.7 to R5.9, R5.19 to R5.22; web-console
-- 4.9). A channel is configuration only. A webhook signing secret and an SMTP
-- password live in the OS secret store (internal/secrets), never here: the two
-- flags below only say whether one is set (notifications R6.2).
CREATE TABLE notification_channels (
    id                TEXT PRIMARY KEY,
    kind              TEXT NOT NULL CHECK (kind IN ('email', 'webhook', 'desktop')),
    name              TEXT NOT NULL,
    -- Email and desktop are personal (owner_login is the user); a webhook is pool-wide.
    scope             TEXT NOT NULL CHECK (scope IN ('personal', 'pool')),
    owner_login       TEXT NOT NULL DEFAULT '',
    enabled           INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    -- JSON array of event types.
    events            TEXT NOT NULL DEFAULT '[]',
    min_severity      TEXT NOT NULL DEFAULT 'warning' CHECK (min_severity IN ('info', 'warning', 'error')),
    address           TEXT NOT NULL DEFAULT '',
    url               TEXT NOT NULL DEFAULT '',
    has_signing       INTEGER NOT NULL DEFAULT 0 CHECK (has_signing IN (0, 1)),
    smtp_host         TEXT NOT NULL DEFAULT '',
    smtp_port         INTEGER NOT NULL DEFAULT 0,
    smtp_username     TEXT NOT NULL DEFAULT '',
    smtp_from         TEXT NOT NULL DEFAULT '',
    smtp_has_password INTEGER NOT NULL DEFAULT 0 CHECK (smtp_has_password IN (0, 1)),
    last_test_at      TEXT,
    last_test_ok      INTEGER CHECK (last_test_ok IN (0, 1)),
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

CREATE INDEX notification_channels_owner ON notification_channels (owner_login);

-- +goose Down
DROP INDEX notification_channels_owner;
DROP TABLE notification_channels;
