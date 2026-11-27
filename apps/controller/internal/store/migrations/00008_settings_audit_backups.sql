-- +goose Up
-- Milestone 1: controller settings, the full audit row (observability R5.24
-- to R5.26) and backups with their drills (operations R5.21 to R5.27).

-- One JSON value per key: retention, backup time, queue order, pool defaults.
-- Secrets never go here.
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_by TEXT NOT NULL DEFAULT 'system'
) STRICT;

-- The audit row grows to the fields of R5.24.
ALTER TABLE audit_log ADD COLUMN actor_id TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN source TEXT NOT NULL DEFAULT 'system';
ALTER TABLE audit_log ADD COLUMN target_type TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN result TEXT NOT NULL DEFAULT 'done';

CREATE INDEX audit_log_actor ON audit_log (actor, at);
CREATE INDEX audit_log_action ON audit_log (action, at);

-- Append-only: no row is ever edited (R5.26). Only retention deletes rows.
-- +goose StatementBegin
CREATE TRIGGER audit_log_no_update BEFORE UPDATE ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'audit_log is append-only');
END;
-- +goose StatementEnd

CREATE TABLE backups (
    id             TEXT PRIMARY KEY,
    file           TEXT NOT NULL UNIQUE,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    size_bytes     INTEGER NOT NULL DEFAULT 0,
    schema_version INTEGER NOT NULL DEFAULT 0,
    state          TEXT NOT NULL DEFAULT 'writing' CHECK (state IN ('writing', 'good', 'failed')),
    check_result   TEXT NOT NULL DEFAULT '',
    checked_at     TEXT,
    deleted_at     TEXT
) STRICT;

CREATE TABLE restore_drills (
    id          TEXT PRIMARY KEY,
    backup_id   TEXT REFERENCES backups (id) ON DELETE SET NULL,
    started_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    finished_at TEXT,
    state       TEXT NOT NULL DEFAULT 'running' CHECK (state IN ('running', 'passed', 'failed')),
    detail      TEXT NOT NULL DEFAULT ''
) STRICT;

-- +goose Down
DROP TABLE restore_drills;
DROP TABLE backups;
DROP TRIGGER audit_log_no_update;
DROP INDEX audit_log_action;
DROP INDEX audit_log_actor;
ALTER TABLE audit_log DROP COLUMN result;
ALTER TABLE audit_log DROP COLUMN target_type;
ALTER TABLE audit_log DROP COLUMN source;
ALTER TABLE audit_log DROP COLUMN actor_id;
DROP TABLE settings;
