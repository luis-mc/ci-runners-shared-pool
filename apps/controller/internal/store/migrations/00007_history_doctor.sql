-- +goose Up
-- Milestone 1: job history (observability R5.7 to R5.12, R6.11) and the
-- doctor's runs and findings (doctor 7.5).

-- One row per job attempt. A finished row never changes (R5.12).
CREATE TABLE job_history (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    repository_id     INTEGER NOT NULL,
    repository        TEXT NOT NULL,
    run_id            INTEGER NOT NULL,
    run_attempt       INTEGER NOT NULL,
    github_job_id     INTEGER NOT NULL,
    workflow          TEXT NOT NULL DEFAULT '',
    job_name          TEXT NOT NULL,
    head_sha          TEXT NOT NULL DEFAULT '',
    trigger           TEXT NOT NULL DEFAULT '',
    labels            TEXT NOT NULL DEFAULT '[]',
    machine_id        TEXT,
    runner_id         TEXT,
    runner_name       TEXT,
    backend           TEXT,
    isolation         TEXT,
    os                TEXT,
    state             TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'running', 'done')),
    created_at        TEXT,                       -- GitHub's created_at
    queued_at         TEXT,                       -- slot queue
    slot_at           TEXT,
    started_at        TEXT,
    completed_at      TEXT,
    github_wait_ms    INTEGER,                    -- NULL means unknown (R5.9)
    slot_wait_ms      INTEGER,
    run_ms            INTEGER,
    result            TEXT CHECK (result IN ('success', 'failure', 'cancelled', 'timed_out', 'lost')),
    html_url          TEXT NOT NULL DEFAULT '',
    UNIQUE (repository_id, run_id, run_attempt, github_job_id)
) STRICT;

CREATE INDEX job_history_completed ON job_history (completed_at DESC);
CREATE INDEX job_history_repository ON job_history (repository_id, id DESC);
CREATE INDEX job_history_machine ON job_history (machine_id, id DESC);
CREATE INDEX job_history_result ON job_history (result, id DESC);
CREATE INDEX job_history_state ON job_history (state);

CREATE TABLE doctor_runs (
    id          TEXT PRIMARY KEY,
    trigger     TEXT NOT NULL CHECK (trigger IN ('web', 'cli', 'schedule', 'system')),
    state       TEXT NOT NULL DEFAULT 'running' CHECK (state IN ('running', 'done', 'failed')),
    scope       TEXT NOT NULL DEFAULT '{}',
    started_by  TEXT NOT NULL DEFAULT 'system',
    started_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    finished_at TEXT,
    worst       TEXT CHECK (worst IN ('ok', 'info', 'skip', 'warn', 'fail')),
    counts      TEXT NOT NULL DEFAULT '{}'
) STRICT;

CREATE TABLE doctor_findings (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id       TEXT NOT NULL REFERENCES doctor_runs (id) ON DELETE CASCADE,
    check_id     TEXT NOT NULL,
    subject_kind TEXT NOT NULL,
    subject_id   TEXT NOT NULL,
    subject_name TEXT NOT NULL,
    severity     TEXT NOT NULL CHECK (severity IN ('ok', 'info', 'skip', 'warn', 'fail')),
    title        TEXT NOT NULL,
    detail       TEXT NOT NULL DEFAULT '',
    fix          TEXT NOT NULL DEFAULT '{}',       -- JSON {text, command, link}
    needs_admin  INTEGER NOT NULL DEFAULT 0 CHECK (needs_admin IN (0, 1)),
    milestone    INTEGER NOT NULL DEFAULT 1,
    checked_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

CREATE INDEX doctor_findings_run ON doctor_findings (run_id);

-- The latest severity per check and subject, for the event diff.
CREATE TABLE doctor_state (
    check_id     TEXT NOT NULL,
    subject_kind TEXT NOT NULL,
    subject_id   TEXT NOT NULL,
    severity     TEXT NOT NULL,
    finding_id   INTEGER REFERENCES doctor_findings (id) ON DELETE SET NULL,
    since        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (check_id, subject_kind, subject_id)
) STRICT;

-- +goose Down
DROP TABLE doctor_state;
DROP TABLE doctor_findings;
DROP TABLE doctor_runs;
DROP TABLE job_history;
