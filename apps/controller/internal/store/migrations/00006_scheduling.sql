-- +goose Up
-- Milestone 1: GitHub's view of jobs (demand), the per-machine slot queue
-- with tickets that expire, held slots and the slot log (scheduling R5.6 to
-- R5.39; owner decisions for M1 on queue order). The background work queue
-- is the job_queue table of 00001; this is the CI job queue. Free means no
-- row. Ages are judged on the controller clock (scheduling R6.4).

-- Jobs as GitHub reports them (webhook, poll, reconcile; github-integration
-- R5.36 to R5.38). One row per GitHub job id.
CREATE TABLE gh_jobs (
    id                 INTEGER PRIMARY KEY,       -- GitHub job id
    repository_id      INTEGER NOT NULL REFERENCES repositories (id),
    run_id             INTEGER NOT NULL,
    run_attempt        INTEGER NOT NULL,
    workflow           TEXT NOT NULL DEFAULT '',
    job_name           TEXT NOT NULL,
    head_sha           TEXT NOT NULL DEFAULT '',
    head_branch        TEXT NOT NULL DEFAULT '',
    event              TEXT NOT NULL DEFAULT '',
    from_fork          INTEGER NOT NULL DEFAULT 0 CHECK (from_fork IN (0, 1)),
    labels             TEXT NOT NULL DEFAULT '[]',
    status             TEXT NOT NULL CHECK (status IN ('queued', 'in_progress', 'completed')),
    conclusion         TEXT,
    runner_name        TEXT,
    placed_machine_id  TEXT REFERENCES machines (id),
    wait_reason        TEXT,
    wait_text          TEXT,
    created_at_github  TEXT,
    started_at_github  TEXT,
    completed_at_github TEXT,
    updated_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

CREATE INDEX gh_jobs_status ON gh_jobs (status);
CREATE INDEX gh_jobs_run ON gh_jobs (repository_id, run_id, run_attempt);

-- One ticket per waiting job on a machine (scheduling R5.6). A ticket expires
-- when its gate is gone, it passes the slot maximum age, or its beat is late
-- (R5.34); expires_at is the next time the sweeper must look at it.
CREATE TABLE slot_tickets (
    id             TEXT PRIMARY KEY,              -- the agent's gate id
    machine_id     TEXT NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    repository     TEXT NOT NULL,
    repository_id  INTEGER,
    run_id         INTEGER NOT NULL,
    run_attempt    INTEGER NOT NULL,
    job            TEXT NOT NULL,
    github_job_id  INTEGER,
    shard          TEXT NOT NULL DEFAULT '',
    runner_name    TEXT NOT NULL DEFAULT '',
    kind           TEXT NOT NULL DEFAULT 'ci' CHECK (kind IN ('ci', 'heavy')),
    repo_priority  INTEGER NOT NULL DEFAULT 0,
    event_class    INTEGER NOT NULL DEFAULT 1 CHECK (event_class IN (0, 1)),
    queued_at_ns   INTEGER NOT NULL,
    random_suffix  INTEGER NOT NULL,               -- breaks ties on the same nanosecond (R5.8)
    beat_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    expires_at     TEXT NOT NULL,
    holder_machine TEXT NOT NULL,
    holder_pid     INTEGER NOT NULL DEFAULT 0,
    gate_pid       INTEGER NOT NULL DEFAULT 0,
    wait_reason    TEXT,
    UNIQUE (machine_id, repository, run_id, run_attempt, job, shard, kind)
) STRICT;

CREATE INDEX slot_tickets_order ON slot_tickets (machine_id, repo_priority DESC, event_class, queued_at_ns, random_suffix);
CREATE INDEX slot_tickets_expiry ON slot_tickets (expires_at);

-- A held slot. The key makes a second take of the same slot fail (R5.14). The
-- claim is a random token; only its holder frees the slot (R5.15).
CREATE TABLE slots (
    machine_id     TEXT NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    kind           TEXT NOT NULL CHECK (kind IN ('ci', 'light', 'heavy')),
    number         INTEGER NOT NULL CHECK (number >= 1),
    claim          TEXT NOT NULL,
    repository     TEXT NOT NULL,
    repository_id  INTEGER,
    run_id         INTEGER NOT NULL,
    run_attempt    INTEGER NOT NULL,
    job            TEXT NOT NULL,
    github_job_id  INTEGER,
    shard          TEXT NOT NULL DEFAULT '',
    runner_name    TEXT NOT NULL DEFAULT '',
    holder_machine TEXT NOT NULL,
    holder_pid     INTEGER NOT NULL DEFAULT 0,
    gate_pid       INTEGER NOT NULL DEFAULT 0,
    queued_at      TEXT NOT NULL,
    taken_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    expires_at     TEXT NOT NULL,                  -- taken_at + slot maximum age (R5.33)
    PRIMARY KEY (machine_id, kind, number)
) STRICT;

CREATE INDEX slots_job ON slots (repository, run_id, run_attempt, job);
CREATE INDEX slots_expiry ON slots (expires_at);

-- One row per slot taken, slot freed and ticket dropped; 5000 per machine (R5.38, R5.39).
CREATE TABLE slot_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    machine_id  TEXT NOT NULL,
    at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    event       TEXT NOT NULL CHECK (event IN ('taken', 'freed', 'stale-freed', 'dropped', 'timeout')),
    kind        TEXT NOT NULL DEFAULT 'ci',
    number      INTEGER,
    repository  TEXT NOT NULL,
    run_id      INTEGER NOT NULL,
    run_attempt INTEGER NOT NULL,
    job         TEXT NOT NULL,
    shard       TEXT NOT NULL DEFAULT '',
    runner_name TEXT NOT NULL DEFAULT '',
    queued_at   TEXT,
    slot_at     TEXT,
    released_at TEXT,
    reason      TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX slot_log_machine ON slot_log (machine_id, id);

-- +goose Down
DROP TABLE slot_log;
DROP TABLE slots;
DROP TABLE slot_tickets;
DROP TABLE gh_jobs;
