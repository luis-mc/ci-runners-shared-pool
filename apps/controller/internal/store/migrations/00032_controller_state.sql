-- +goose Up
-- OPS59: the machines, their settings, keys, join codes and changes, and the
-- runner records and plans, live in the database, so an update or a restart of
-- the controller keeps them. Until now they sat in machines.json and in memory.
-- Each row holds the record as JSON in doc; the columns beside it are what a
-- query looks up. There is no foreign key: a runner whose machine is not known
-- yet at startup is kept, never dropped. Keys and codes are hashes only (R6.2).

CREATE TABLE ctl_machines (
    id      TEXT PRIMARY KEY,
    name    TEXT NOT NULL,
    removed INTEGER NOT NULL DEFAULT 0 CHECK (removed IN (0, 1)),
    doc     TEXT NOT NULL
) STRICT;

CREATE TABLE ctl_machine_settings (
    machine_id TEXT PRIMARY KEY,
    doc        TEXT NOT NULL
) STRICT;

CREATE TABLE ctl_machine_keys (
    hash       TEXT PRIMARY KEY,
    machine_id TEXT NOT NULL,
    revoked    INTEGER NOT NULL DEFAULT 0 CHECK (revoked IN (0, 1))
) STRICT;

CREATE INDEX ctl_machine_keys_machine ON ctl_machine_keys (machine_id);

CREATE TABLE ctl_join_codes (
    id        TEXT PRIMARY KEY,
    code_hash TEXT NOT NULL UNIQUE,
    doc       TEXT NOT NULL
) STRICT;

CREATE TABLE ctl_machine_changes (
    id         TEXT PRIMARY KEY,
    machine_id TEXT NOT NULL,
    open       INTEGER NOT NULL DEFAULT 0 CHECK (open IN (0, 1)),
    doc        TEXT NOT NULL
) STRICT;

CREATE INDEX ctl_machine_changes_machine ON ctl_machine_changes (machine_id, open);

CREATE TABLE ctl_runners (
    id         TEXT PRIMARY KEY,
    machine_id TEXT NOT NULL,
    name       TEXT NOT NULL,
    doc        TEXT NOT NULL
) STRICT;

CREATE INDEX ctl_runners_machine ON ctl_runners (machine_id);

CREATE TABLE ctl_runner_plans (
    id  TEXT PRIMARY KEY,
    doc TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE ctl_runner_plans;
DROP TABLE ctl_runners;
DROP TABLE ctl_machine_changes;
DROP TABLE ctl_join_codes;
DROP TABLE ctl_machine_keys;
DROP TABLE ctl_machine_settings;
DROP TABLE ctl_machines;
