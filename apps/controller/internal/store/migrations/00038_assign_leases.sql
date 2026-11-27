-- +goose Up
-- OPS80: a runner is made for a job only when there is room (runners R5.110 to
-- R5.116). assign_leases holds one lease per GitHub job id, so a repeated
-- webhook or a restart never makes a second runner. assign_warm holds the one
-- ready runner per busy label set; assign_labelsets remembers when a label set
-- last had a job.

CREATE TABLE assign_leases (
    job_id         INTEGER PRIMARY KEY,
    run_id         INTEGER NOT NULL DEFAULT 0,
    run_attempt    INTEGER NOT NULL DEFAULT 1,
    repository     TEXT NOT NULL,
    job_name       TEXT NOT NULL DEFAULT '',
    labels         TEXT NOT NULL DEFAULT '[]',
    job_label      TEXT NOT NULL DEFAULT '',
    os             TEXT NOT NULL DEFAULT '',
    state          TEXT NOT NULL CHECK (state IN ('waiting', 'starting', 'running', 'done', 'gone')),
    machine_id     TEXT NOT NULL DEFAULT '',
    runner_name    TEXT NOT NULL DEFAULT '',
    runner_record  TEXT NOT NULL DEFAULT '',
    github_runner  INTEGER NOT NULL DEFAULT 0,
    cpus           INTEGER NOT NULL DEFAULT 1 CHECK (cpus >= 1),
    mem_gib        INTEGER NOT NULL DEFAULT 1 CHECK (mem_gib >= 1),
    class          TEXT NOT NULL DEFAULT '',
    queued_at      TEXT NOT NULL,
    started_at     TEXT NOT NULL DEFAULT '',
    passes         INTEGER NOT NULL DEFAULT 0 CHECK (passes >= 0),
    reserved_since TEXT NOT NULL DEFAULT '',
    reason         TEXT NOT NULL DEFAULT '',
    reason_code    TEXT NOT NULL DEFAULT '',
    fails          INTEGER NOT NULL DEFAULT 0 CHECK (fails >= 0),
    next_try       TEXT NOT NULL DEFAULT '',
    arch           TEXT NOT NULL DEFAULT '',
    alerted        INTEGER NOT NULL DEFAULT 0 CHECK (alerted IN (0, 1)),
    updated_at     TEXT NOT NULL
) STRICT;

CREATE INDEX assign_leases_state ON assign_leases (state);
CREATE INDEX assign_leases_machine ON assign_leases (machine_id, state);

CREATE TABLE assign_warm (
    id             TEXT PRIMARY KEY,
    machine_id     TEXT NOT NULL,
    label_key      TEXT NOT NULL,
    repository     TEXT NOT NULL,
    os             TEXT NOT NULL,
    labels         TEXT NOT NULL DEFAULT '[]',
    runner_name    TEXT NOT NULL,
    runner_record  TEXT NOT NULL DEFAULT '',
    github_runner  INTEGER NOT NULL DEFAULT 0,
    cpus           INTEGER NOT NULL DEFAULT 1 CHECK (cpus >= 1),
    mem_gib        INTEGER NOT NULL DEFAULT 1 CHECK (mem_gib >= 1),
    started_at     TEXT NOT NULL
) STRICT;

CREATE INDEX assign_warm_set ON assign_warm (label_key);

CREATE TABLE assign_labelsets (
    label_key  TEXT PRIMARY KEY,
    repository TEXT NOT NULL,
    os         TEXT NOT NULL,
    labels     TEXT NOT NULL DEFAULT '[]',
    cpus       INTEGER NOT NULL DEFAULT 1,
    mem_gib    INTEGER NOT NULL DEFAULT 1,
    last_busy  TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE assign_labelsets;
DROP TABLE assign_warm;
DROP TABLE assign_leases;
