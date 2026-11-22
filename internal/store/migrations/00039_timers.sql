-- +goose Up
-- OPS81: the job and run clocks of crsp (scheduling R5.103 to R5.110). Times are
-- UTC RFC3339 text, an empty string means unknown or not started. Durations are
-- milliseconds, 0 means unknown. The console reads these later. They sit in their
-- own tables, keyed like jobs and runs, so the jobs upsert stays as it is.

-- state is running, grace, stopped or ended.
CREATE TABLE job_clocks (
    job_id           INTEGER PRIMARY KEY,
    repository_id    INTEGER NOT NULL,
    repository       TEXT NOT NULL DEFAULT '',
    run_id           INTEGER NOT NULL,
    run_attempt      INTEGER NOT NULL,
    started_at       TEXT NOT NULL DEFAULT '',
    limit_sec        INTEGER NOT NULL DEFAULT 0,
    extended         INTEGER NOT NULL DEFAULT 0,
    state            TEXT NOT NULL DEFAULT 'running' CHECK (state IN ('running', 'grace', 'stopped', 'ended')),
    deadline         TEXT NOT NULL DEFAULT '',
    note             TEXT NOT NULL DEFAULT '',
    machine_id       TEXT NOT NULL DEFAULT '',
    runner_ref       TEXT NOT NULL DEFAULT '',
    queued_ms        INTEGER NOT NULL DEFAULT 0,
    startup_ms       INTEGER NOT NULL DEFAULT 0,
    hold_ms          INTEGER NOT NULL DEFAULT 0,
    run_ms           INTEGER NOT NULL DEFAULT 0,
    job_timer_ms     INTEGER NOT NULL DEFAULT 0,
    run_timer_ms     INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX job_clocks_state ON job_clocks (state);
CREATE INDEX job_clocks_run ON job_clocks (repository_id, run_id, run_attempt);

CREATE TABLE run_clocks (
    repository_id    INTEGER NOT NULL,
    run_id           INTEGER NOT NULL,
    run_attempt      INTEGER NOT NULL,
    repository       TEXT NOT NULL DEFAULT '',
    started_at       TEXT NOT NULL DEFAULT '',
    limit_sec        INTEGER NOT NULL DEFAULT 0,
    extended         INTEGER NOT NULL DEFAULT 0,
    state            TEXT NOT NULL DEFAULT 'running' CHECK (state IN ('running', 'grace', 'stopped', 'ended')),
    deadline         TEXT NOT NULL DEFAULT '',
    note             TEXT NOT NULL DEFAULT '',
    run_timer_ms     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (repository_id, run_id, run_attempt)
) STRICT;

CREATE INDEX run_clocks_state ON run_clocks (state);

-- +goose Down
DROP TABLE run_clocks;
DROP TABLE job_clocks;
