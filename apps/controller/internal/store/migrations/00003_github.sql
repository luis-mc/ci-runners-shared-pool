-- +goose Up
-- Milestone 1: installations, repositories and their policy, managed
-- repository variables, runs-on findings and webhook de-duplication
-- (github-integration R5.17 to R5.30, R5.36; scheduling R5.4, R5.8 to R5.10).

CREATE TABLE installations (
    id                   INTEGER PRIMARY KEY,     -- GitHub installation id
    account_login        TEXT NOT NULL,
    account_type         TEXT NOT NULL CHECK (account_type IN ('User', 'Organization')),
    repository_selection TEXT NOT NULL DEFAULT 'selected' CHECK (repository_selection IN ('all', 'selected')),
    permissions          TEXT NOT NULL DEFAULT '{}', -- JSON, as granted (R5.24)
    suspended_at         TEXT,
    removed_at           TEXT,
    updated_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

CREATE TABLE repositories (
    id              INTEGER PRIMARY KEY,          -- GitHub repository id
    installation_id INTEGER NOT NULL REFERENCES installations (id),
    full_name       TEXT NOT NULL,
    owner           TEXT NOT NULL,
    name            TEXT NOT NULL,
    visibility      TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('public', 'private', 'internal')),
    default_branch  TEXT NOT NULL DEFAULT 'main',
    archived        INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1)),
    pool_on         INTEGER NOT NULL DEFAULT 0 CHECK (pool_on IN (0, 1)),
    pool_changed_at TEXT,
    pool_changed_by TEXT,
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

CREATE INDEX repositories_installation ON repositories (installation_id);
CREATE INDEX repositories_full_name ON repositories (full_name);

-- One policy per enabled repository (github-integration R5.25).
CREATE TABLE repo_policies (
    repository_id      INTEGER PRIMARY KEY REFERENCES repositories (id) ON DELETE CASCADE,
    priority           INTEGER NOT NULL DEFAULT 0 CHECK (priority BETWEEN -100 AND 100),
    max_parallel_jobs  INTEGER NOT NULL DEFAULT 2 CHECK (max_parallel_jobs BETWEEN 1 AND 256),
    allow_fork_prs     INTEGER NOT NULL DEFAULT 0 CHECK (allow_fork_prs IN (0, 1)),
    labels             TEXT NOT NULL DEFAULT '[]', -- JSON list, generic labels only
    runner_groups      TEXT NOT NULL DEFAULT '[]', -- JSON list
    budget_memory_pct  INTEGER CHECK (budget_memory_pct BETWEEN 1 AND 100),
    budget_cpu_pct     INTEGER CHECK (budget_cpu_pct BETWEEN 1 AND 100),
    updated_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_by         TEXT NOT NULL DEFAULT 'system'
) STRICT;

-- The CRSP_RUNS_ON_* variables as the controller last wrote them (R5.28).
CREATE TABLE managed_variables (
    repository_id INTEGER NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    name          TEXT NOT NULL CHECK (name IN ('CRSP_RUNS_ON_LINUX', 'CRSP_RUNS_ON_WINDOWS', 'CRSP_RUNS_ON_MACOS')),
    value         TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'ok' CHECK (status IN ('ok', 'missing', 'wrong', 'not_managed')),
    written_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    checked_at    TEXT,
    PRIMARY KEY (repository_id, name)
) STRICT;

CREATE TABLE runs_on_findings (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    repository_id INTEGER NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    path          TEXT NOT NULL,
    line          INTEGER NOT NULL,
    labels        TEXT NOT NULL DEFAULT '[]',
    kind          TEXT NOT NULL CHECK (kind IN ('no_runner_with_labels', 'not_generic', 'uses_variable', 'hosted')),
    text          TEXT NOT NULL,
    found_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

CREATE INDEX runs_on_findings_repo ON runs_on_findings (repository_id);

CREATE TABLE webhook_deliveries (
    delivery_id TEXT PRIMARY KEY,
    event       TEXT NOT NULL,
    received_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

-- Plans for repository and runner changes: nothing changes until applied
-- (web-console R5.18, R5.19; runners R5.1, R5.6).
CREATE TABLE plans (
    id         TEXT PRIMARY KEY,
    area       TEXT NOT NULL CHECK (area IN ('repositories', 'runners')),
    state      TEXT NOT NULL DEFAULT 'planned'
               CHECK (state IN ('planned', 'applying', 'done', 'failed', 'stopped', 'stale')),
    state_hash TEXT NOT NULL,                     -- the state the plan was made from
    steps      TEXT NOT NULL DEFAULT '[]',        -- JSON list of steps with their outcome
    created_by TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

-- +goose Down
DROP TABLE plans;
DROP TABLE webhook_deliveries;
DROP TABLE runs_on_findings;
DROP TABLE managed_variables;
DROP TABLE repo_policies;
DROP TABLE repositories;
DROP TABLE installations;
