-- +goose Up
-- Milestone 1: runners, managed or found on a machine, their state changes and
-- usage (runners R5.2, R5.3, R5.5, R5.12, R6.7, R6.8). Tokens, JIT configs and
-- account passwords have no column (runners R6.2).

CREATE TABLE runners (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL,
    machine_id       TEXT NOT NULL REFERENCES machines (id),
    os               TEXT NOT NULL CHECK (os IN ('linux', 'windows', 'macos')),
    arch             TEXT NOT NULL CHECK (arch IN ('x64', 'arm64')),
    kind             TEXT NOT NULL CHECK (kind IN ('persistent', 'ephemeral')),
    backend          TEXT NOT NULL,
    isolation        TEXT NOT NULL CHECK (isolation IN ('microVM', 'VM', 'container', 'persistent')),
    labels           TEXT NOT NULL DEFAULT '[]',
    scope_kind       TEXT NOT NULL CHECK (scope_kind IN ('repository', 'organization')),
    repository_id    INTEGER REFERENCES repositories (id),
    organization     TEXT,
    runner_group     TEXT,
    github_runner_id INTEGER,
    runner_version   TEXT NOT NULL DEFAULT '',
    state            TEXT NOT NULL DEFAULT 'creating'
                     CHECK (state IN ('creating', 'idle', 'busy', 'draining', 'updating', 'offline', 'failed', 'removing')),
    managed          INTEGER NOT NULL DEFAULT 1 CHECK (managed IN (0, 1)),
    service_name     TEXT NOT NULL DEFAULT '',
    account          TEXT NOT NULL DEFAULT '',
    folder           TEXT NOT NULL DEFAULT '',
    current_job_id   INTEGER,                     -- job_history.id
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (machine_id, name)
) STRICT;

CREATE INDEX runners_machine ON runners (machine_id);

CREATE TABLE runner_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    runner_id  TEXT NOT NULL REFERENCES runners (id) ON DELETE CASCADE,
    at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    from_state TEXT NOT NULL,
    to_state   TEXT NOT NULL,
    detail     TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX runner_events_runner ON runner_events (runner_id, at);

-- Usage per runner and job, kept 90 days (runners R5.12, R6.7).
CREATE TABLE runner_usage (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    runner_id           TEXT NOT NULL,
    repository          TEXT NOT NULL,
    run_id              INTEGER NOT NULL,
    run_attempt         INTEGER NOT NULL,
    job                 TEXT NOT NULL,
    cpu_ms              INTEGER NOT NULL DEFAULT 0,
    memory_peak_bytes   INTEGER NOT NULL DEFAULT 0,
    memory_median_bytes INTEGER NOT NULL DEFAULT 0,
    disk_used_bytes     INTEGER NOT NULL DEFAULT 0,
    wall_ms             INTEGER NOT NULL DEFAULT 0,
    reported_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (runner_id, repository, run_id, run_attempt, job)
) STRICT;

-- +goose Down
DROP TABLE runner_usage;
DROP TABLE runner_events;
DROP TABLE runners;
