-- +goose Up
-- OPS67: the size of a CI job by scan and count (scheduling R5.80 to R5.88).
-- job_size_scans: the scan guess of each job of a repository at its last scan.
-- job_size_rules: overrides; origin 'console' is the console setting, 'file'
-- is the sizes: map of .crsp.yml read by the scan. job_size_measured is for
-- measured runs (OPS66 fills it). job_size_files: the state of .crsp.yml.

CREATE TABLE job_size_scans (
    repository  TEXT NOT NULL,
    job         TEXT NOT NULL,
    name        TEXT NOT NULL DEFAULT '',
    workflow    TEXT NOT NULL DEFAULT '',
    cpu_units   INTEGER NOT NULL,
    mem_units   INTEGER NOT NULL,
    cpu         REAL NOT NULL,
    gib         REAL NOT NULL,
    class       TEXT NOT NULL CHECK (class IN ('light', 'normal', 'heavy')),
    confidence  TEXT NOT NULL CHECK (confidence IN ('high', 'low')),
    tests       INTEGER NOT NULL DEFAULT 0,
    prev_tests  INTEGER NOT NULL DEFAULT 0,
    est_sec     INTEGER NOT NULL DEFAULT 0,
    reasons     TEXT NOT NULL DEFAULT '[]',
    services    TEXT NOT NULL DEFAULT '[]',
    commit_sha  TEXT NOT NULL,
    scanned_at  TEXT NOT NULL,
    reset_note  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (repository, job)
) STRICT;

CREATE TABLE job_size_rules (
    repository TEXT NOT NULL,
    pattern    TEXT NOT NULL,
    origin     TEXT NOT NULL CHECK (origin IN ('console', 'file')),
    class      TEXT NOT NULL CHECK (class IN ('light', 'normal', 'heavy')),
    PRIMARY KEY (repository, pattern, origin)
) STRICT;

CREATE TABLE job_size_measured (
    repository TEXT NOT NULL,
    job        TEXT NOT NULL,
    cpu_units  INTEGER NOT NULL,
    mem_units  INTEGER NOT NULL,
    runs       INTEGER NOT NULL,
    PRIMARY KEY (repository, job)
) STRICT;

CREATE TABLE job_size_files (
    repository TEXT PRIMARY KEY,
    error      TEXT NOT NULL DEFAULT ''
) STRICT;

-- +goose Down
DROP TABLE job_size_files;
DROP TABLE job_size_measured;
DROP TABLE job_size_rules;
DROP TABLE job_size_scans;
