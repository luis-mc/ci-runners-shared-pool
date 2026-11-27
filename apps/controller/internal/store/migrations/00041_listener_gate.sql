-- +goose Up
-- OPS78: the listener gate of persistent runners on the hosts (runners R5.134 to
-- R5.140). One lease per GitHub job id keeps the label and the listener the
-- gate turned on for that job; `body` is the lease as JSON (the runner it names
-- included). One row per runner keeps the weekly wake-up. The gate setting (key
-- 'listener_gate' of the settings table) carries `enforce`; with no row it reads
-- as false, so the gate starts in observe mode and changes no listener.
CREATE TABLE listener_leases (
    job_id     INTEGER PRIMARY KEY,
    runner_id  TEXT NOT NULL,
    state      TEXT NOT NULL CHECK (state IN ('observed', 'started', 'stopping', 'finished', 'failed')),
    body       TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX listener_leases_runner ON listener_leases (runner_id, state);

CREATE TABLE listener_wakes (
    runner_id    TEXT PRIMARY KEY,
    last_wake    TEXT NOT NULL DEFAULT '',
    waking_since TEXT NOT NULL DEFAULT ''
) STRICT;

-- +goose Down
DROP TABLE listener_wakes;
DROP TABLE listener_leases;
