-- +goose Up
-- The Jobs section (observability R5.7 to R5.12, R6.8; OPS34 owner decisions D1 to D14): runs and their
-- attempts, jobs, steps, daily summaries and the workflow graph cache. Times are UTC RFC3339 text, an empty
-- string means unknown. gh_jobs and job_history of 00006 and 00007 stay as they are.
CREATE TABLE runs (
    repository_id INTEGER NOT NULL,
    run_id        INTEGER NOT NULL,
    run_attempt   INTEGER NOT NULL,
    repository    TEXT NOT NULL DEFAULT '',
    workflow      TEXT NOT NULL DEFAULT '',
    workflow_name TEXT NOT NULL DEFAULT '',
    event         TEXT NOT NULL DEFAULT '',
    head_branch   TEXT NOT NULL DEFAULT '',
    head_sha      TEXT NOT NULL DEFAULT '',
    pull_request  INTEGER NOT NULL DEFAULT 0,
    task_id       TEXT NOT NULL DEFAULT '',
    worktree      TEXT NOT NULL DEFAULT '',
    html_url      TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL DEFAULT '',
    started_at    TEXT NOT NULL DEFAULT '',
    completed_at  TEXT NOT NULL DEFAULT '',
    conclusion    TEXT NOT NULL DEFAULT '',
    updated_at    TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (repository_id, run_id, run_attempt)
) STRICT;

CREATE INDEX runs_created ON runs (created_at DESC, run_id DESC);
CREATE INDEX runs_run ON runs (run_id);

CREATE TABLE jobs (
    id            INTEGER PRIMARY KEY,
    repository_id INTEGER NOT NULL,
    repository    TEXT NOT NULL DEFAULT '',
    run_id        INTEGER NOT NULL,
    run_attempt   INTEGER NOT NULL,
    workflow      TEXT NOT NULL DEFAULT '',
    name          TEXT NOT NULL DEFAULT '',
    job_type      TEXT NOT NULL DEFAULT '',
    matrix        TEXT NOT NULL DEFAULT '[]',
    job_ident       TEXT NOT NULL DEFAULT '',
    branch        TEXT NOT NULL DEFAULT '',
    head_sha      TEXT NOT NULL DEFAULT '',
    event         TEXT NOT NULL DEFAULT '',
    labels        TEXT NOT NULL DEFAULT '[]',
    state         TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'running', 'done')),
    result        TEXT NOT NULL DEFAULT '',
    html_url      TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL DEFAULT '',
    queued_at     TEXT NOT NULL DEFAULT '',
    slot_at       TEXT NOT NULL DEFAULT '',
    started_at    TEXT NOT NULL DEFAULT '',
    completed_at  TEXT NOT NULL DEFAULT '',
    cleanup_ms    INTEGER NOT NULL DEFAULT 0,
    machine_id    TEXT NOT NULL DEFAULT '',
    machine_name  TEXT NOT NULL DEFAULT '',
    runner_id     TEXT NOT NULL DEFAULT '',
    runner_name   TEXT NOT NULL DEFAULT '',
    runner_kind   TEXT NOT NULL DEFAULT '',
    backend       TEXT NOT NULL DEFAULT '',
    isolation     TEXT NOT NULL DEFAULT '',
    cache         TEXT NOT NULL DEFAULT '',
    wait_reason   TEXT NOT NULL DEFAULT '',
    wait_text     TEXT NOT NULL DEFAULT '',
    worktree      TEXT NOT NULL DEFAULT '',
    light         INTEGER NOT NULL DEFAULT 0,
    fc_lo_ms      INTEGER NOT NULL DEFAULT 0,
    fc_mid_ms     INTEGER NOT NULL DEFAULT 0,
    fc_hi_ms      INTEGER NOT NULL DEFAULT 0,
    fc_basis      TEXT NOT NULL DEFAULT '',
    fc_samples    INTEGER NOT NULL DEFAULT 0,
    updated_at    TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX jobs_state ON jobs (state, id);
CREATE INDEX jobs_repository ON jobs (repository_id, id);
CREATE INDEX jobs_run ON jobs (repository_id, run_id, run_attempt);
CREATE INDEX jobs_key ON jobs (job_ident, completed_at);
CREATE INDEX jobs_runner ON jobs (runner_name);
CREATE INDEX jobs_machine ON jobs (machine_id);
CREATE INDEX jobs_type ON jobs (job_type);
CREATE INDEX jobs_completed ON jobs (completed_at);

CREATE TABLE job_steps (
    job_id       INTEGER NOT NULL,
    number       INTEGER NOT NULL,
    name         TEXT NOT NULL DEFAULT '',
    state        TEXT NOT NULL DEFAULT '',
    result       TEXT NOT NULL DEFAULT '',
    started_at   TEXT NOT NULL DEFAULT '',
    completed_at TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (job_id, number)
) STRICT;

CREATE INDEX job_steps_completed ON job_steps (completed_at);

-- crsp facts that arrived before the job row; merged on the job's upsert, dropped after a day.
CREATE TABLE job_facts_pending (
    job_id     INTEGER PRIMARY KEY,
    json       TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE TABLE job_daily (
    day         TEXT NOT NULL,
    repository  TEXT NOT NULL,
    job_type    TEXT NOT NULL,
    jobs        INTEGER NOT NULL DEFAULT 0,
    failed      INTEGER NOT NULL DEFAULT 0,
    wait_p50_ms INTEGER NOT NULL DEFAULT 0,
    wait_p90_ms INTEGER NOT NULL DEFAULT 0,
    run_p50_ms  INTEGER NOT NULL DEFAULT 0,
    run_p90_ms  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (day, repository, job_type)
) STRICT;

CREATE TABLE workflow_graphs (
    repository_id INTEGER NOT NULL,
    workflow      TEXT NOT NULL,
    head_sha      TEXT NOT NULL,
    ok            INTEGER NOT NULL DEFAULT 0,
    needs         TEXT NOT NULL DEFAULT '{}',
    dynamic       TEXT NOT NULL DEFAULT '{}',
    fetched_at    TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (repository_id, workflow, head_sha)
) STRICT;

-- +goose Down
DROP TABLE workflow_graphs;
DROP TABLE job_daily;
DROP TABLE job_facts_pending;
DROP TABLE job_steps;
DROP TABLE jobs;
DROP TABLE runs;
