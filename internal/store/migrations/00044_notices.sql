-- +goose Up
-- Notices and their deliveries (notifications R5.2, R5.3, R5.11 to R5.14, R5.23, R5.32, R6.4, R6.5).
-- notify_event holds one state per event key (type plus subject). A notice is made only when the
-- state changes; each notice has one delivery per channel that carries it, written in the same
-- transaction as the state change (R6.4). Nothing here holds a secret or a webhook address: a
-- delivery names its channel and a target that is a host or an address, never a path or a token.
CREATE TABLE notify_event (
    key            TEXT PRIMARY KEY,
    type           TEXT NOT NULL,
    subject_kind   TEXT NOT NULL,
    subject_id     TEXT NOT NULL,
    subject_name   TEXT NOT NULL,
    state          TEXT NOT NULL CHECK (state IN ('firing', 'resolved')),
    tone           TEXT NOT NULL,
    severity       TEXT NOT NULL DEFAULT '',
    summary        TEXT NOT NULL DEFAULT '',
    link           TEXT NOT NULL DEFAULT '',
    first_fired_at TEXT NOT NULL,
    last_change_at TEXT NOT NULL,
    flaps          INTEGER NOT NULL DEFAULT 0,
    open_notice_id TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE TABLE notices (
    id           TEXT PRIMARY KEY,
    event_key    TEXT NOT NULL,
    type         TEXT NOT NULL,
    subject_kind TEXT NOT NULL,
    subject_id   TEXT NOT NULL,
    subject_name TEXT NOT NULL,
    state        TEXT NOT NULL CHECK (state IN ('firing', 'resolved')),
    tone         TEXT NOT NULL,
    severity     TEXT NOT NULL DEFAULT '',
    summary      TEXT NOT NULL,
    link         TEXT NOT NULL DEFAULT '',
    flaps        INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL
) STRICT;
CREATE INDEX notices_created ON notices (created_at DESC);
CREATE INDEX notices_key ON notices (event_key, created_at DESC);

CREATE TABLE notice_deliveries (
    id            TEXT PRIMARY KEY,
    notice_id     TEXT NOT NULL REFERENCES notices (id) ON DELETE CASCADE,
    channel_id    TEXT NOT NULL,
    channel_kind  TEXT NOT NULL,
    channel_name  TEXT NOT NULL,
    owner_login   TEXT NOT NULL DEFAULT '',
    target        TEXT NOT NULL DEFAULT '',
    -- queued, waiting_tray, delivered, failed, held, grouped, dropped
    status        TEXT NOT NULL CHECK (status IN ('queued', 'waiting_tray', 'delivered', 'failed', 'held', 'grouped', 'dropped')),
    attempt       INTEGER NOT NULL DEFAULT 0,
    next_try_at   TEXT NOT NULL,
    last_error    TEXT NOT NULL DEFAULT '',
    -- A grouped delivery says how many notices it stands for (R5.13); a summary delivery too (R5.12).
    group_count   INTEGER NOT NULL DEFAULT 0,
    burst_group     TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    delivered_at  TEXT
) STRICT;
CREATE INDEX notice_deliveries_due ON notice_deliveries (status, next_try_at);
CREATE INDEX notice_deliveries_notice ON notice_deliveries (notice_id);
CREATE INDEX notice_deliveries_channel ON notice_deliveries (channel_id, created_at DESC);

-- +goose Down
DROP INDEX notice_deliveries_channel;
DROP INDEX notice_deliveries_notice;
DROP INDEX notice_deliveries_due;
DROP TABLE notice_deliveries;
DROP INDEX notices_key;
DROP INDEX notices_created;
DROP TABLE notices;
DROP TABLE notify_event;
