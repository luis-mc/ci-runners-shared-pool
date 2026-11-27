-- +goose Up
-- CI run insights (CIX8 contract, docs/features/ci-run-insights). Findings and the data they are made from:
-- stored CI logs and report files (owner decision D4: on the owner's machines only, redacted), parsed test
-- results, CPU and memory samples per job, workflow file checks and guardrail reports. Times are UTC RFC3339
-- text, an empty string means unknown. JSON columns hold canonical JSON text. Numbers 00022 to 00029 stay
-- reserved for the registry wave.

-- One finding (R5.7). One open row per (kind, scope, repository_id, subject); a resolved row is history (R5.8).
CREATE TABLE insight_findings (
    id                 INTEGER PRIMARY KEY,
    kind               TEXT NOT NULL,                              -- insights.Kind, such as wait.hold
    scope              TEXT NOT NULL CHECK (scope IN ('repo', 'pool')),
    repository_id      INTEGER NOT NULL DEFAULT 0,                 -- 0 for the pool scope
    repository         TEXT NOT NULL DEFAULT '',                   -- owner/repo, empty for the pool scope
    subject            TEXT NOT NULL DEFAULT '',                   -- stable key inside the kind
    area               TEXT NOT NULL DEFAULT '',                   -- waits, pool, runs, workflows, guardrails, tests
    title              TEXT NOT NULL DEFAULT '',                   -- what is wrong, plain words
    fix                TEXT NOT NULL DEFAULT '',                   -- the fix, plain words
    snippet            TEXT NOT NULL DEFAULT '',                   -- optional workflow snippet, shown as text
    evidence           TEXT NOT NULL DEFAULT '{}',                 -- JSON, the numbers the screens show
    minutes_saved_week INTEGER NOT NULL DEFAULT 0,                 -- estimate, whole minutes (R5.9)
    guardrail          TEXT NOT NULL DEFAULT '' CHECK (guardrail IN ('', 'G1', 'G2', 'G3', 'G4', 'G5', 'G6', 'G7')),
    action_kind        TEXT NOT NULL DEFAULT '' CHECK (action_kind IN ('', 'machine_slots', 'repo_parallel_limit', 'light_lane')),
    first_seen         TEXT NOT NULL,
    last_seen          TEXT NOT NULL,
    resolved_at        TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE UNIQUE INDEX insight_findings_open ON insight_findings (kind, scope, repository_id, subject) WHERE resolved_at = '';
CREATE INDEX insight_findings_rank ON insight_findings (resolved_at, minutes_saved_week DESC, first_seen);
CREATE INDEX insight_findings_repository ON insight_findings (repository_id, resolved_at);

-- One analyser pass (R6.3, R6.8): how long it took and whether it may resolve findings.
CREATE TABLE insight_analyser_runs (
    id          INTEGER PRIMARY KEY,
    analyser    TEXT NOT NULL,
    window_from TEXT NOT NULL,
    window_to   TEXT NOT NULL,
    started_at  TEXT NOT NULL,
    finished_at TEXT NOT NULL DEFAULT '',
    ok          INTEGER NOT NULL DEFAULT 0,                        -- 1: a full, successful window (may resolve)
    found       INTEGER NOT NULL DEFAULT 0,
    resolved    INTEGER NOT NULL DEFAULT 0,
    error       TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX insight_analyser_runs_started ON insight_analyser_runs (analyser, started_at DESC);

-- An action plan for a setup change (R5.10): Review, then the work, then Done.
CREATE TABLE insight_plans (
    id          TEXT NOT NULL PRIMARY KEY,
    finding_id  INTEGER NOT NULL,
    action_kind TEXT NOT NULL CHECK (action_kind IN ('machine_slots', 'repo_parallel_limit', 'light_lane')),
    state       TEXT NOT NULL DEFAULT 'draft' CHECK (state IN ('draft', 'running', 'done', 'failed', 'stopped')),
    changes     TEXT NOT NULL DEFAULT '[]',                        -- JSON array of {target, field, from, to, words}
    results     TEXT NOT NULL DEFAULT '[]',                        -- JSON array of {target, field, state, error}
    created_by  TEXT NOT NULL DEFAULT '',                          -- GitHub login
    created_at  TEXT NOT NULL,
    started_at  TEXT NOT NULL DEFAULT '',
    finished_at TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX insight_plans_finding ON insight_plans (finding_id);

-- Stored job logs (R5.2): the redacted, gzipped file under the CI data folder and its retention.
CREATE TABLE ci_job_logs (
    job_id        INTEGER PRIMARY KEY,                             -- the GitHub job id, as jobs.id
    repository_id INTEGER NOT NULL,
    run_id        INTEGER NOT NULL,
    run_attempt   INTEGER NOT NULL,
    path          TEXT NOT NULL DEFAULT '',                        -- relative to the data dir
    bytes         INTEGER NOT NULL DEFAULT 0,                      -- stored (gzipped) size
    raw_bytes     INTEGER NOT NULL DEFAULT 0,
    lines         INTEGER NOT NULL DEFAULT 0,
    sha256        TEXT NOT NULL DEFAULT '',
    state         TEXT NOT NULL CHECK (state IN ('stored', 'too_large', 'missing', 'failed', 'expired')),
    reason        TEXT NOT NULL DEFAULT '',
    fetched_at    TEXT NOT NULL,
    expires_at    TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX ci_job_logs_repository ON ci_job_logs (repository_id, fetched_at);
CREATE INDEX ci_job_logs_expires ON ci_job_logs (expires_at);

-- Stored report files from run artifacts (R5.3): test reports and guardrail reports.
CREATE TABLE ci_report_files (
    id            INTEGER PRIMARY KEY,
    repository_id INTEGER NOT NULL,
    run_id        INTEGER NOT NULL,
    run_attempt   INTEGER NOT NULL DEFAULT 1,
    artifact_id   INTEGER NOT NULL,
    artifact_name TEXT NOT NULL DEFAULT '',
    member        TEXT NOT NULL DEFAULT '',                        -- file name inside the artifact zip
    format        TEXT NOT NULL DEFAULT '',                        -- tests.Format or 'guardrails'
    path          TEXT NOT NULL DEFAULT '',
    bytes         INTEGER NOT NULL DEFAULT 0,
    state         TEXT NOT NULL CHECK (state IN ('stored', 'parsed', 'too_large', 'unknown_format', 'broken', 'failed', 'expired')),
    reason        TEXT NOT NULL DEFAULT '',
    fetched_at    TEXT NOT NULL,
    expires_at    TEXT NOT NULL DEFAULT '',
    UNIQUE (repository_id, artifact_id, member)
) STRICT;

CREATE INDEX ci_report_files_run ON ci_report_files (repository_id, run_id);
CREATE INDEX ci_report_files_expires ON ci_report_files (expires_at);

-- One test case run (R5.33). assertions -1 means the format does not report them (R5.37).
CREATE TABLE test_results (
    id            INTEGER PRIMARY KEY,
    repository_id INTEGER NOT NULL,
    run_id        INTEGER NOT NULL,
    run_attempt   INTEGER NOT NULL,
    job_id        INTEGER NOT NULL DEFAULT 0,                      -- 0 when the report is per run
    job_ident     TEXT NOT NULL DEFAULT '',
    head_sha      TEXT NOT NULL DEFAULT '',
    report_id     INTEGER NOT NULL DEFAULT 0,                      -- ci_report_files.id, 0 from a job log
    format        TEXT NOT NULL,
    test_id       TEXT NOT NULL,                                   -- normalised format + suite + name
    suite         TEXT NOT NULL DEFAULT '',
    name          TEXT NOT NULL,
    file          TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL DEFAULT 'unit' CHECK (kind IN ('unit', 'integration', 'e2e')),
    result        TEXT NOT NULL CHECK (result IN ('passed', 'failed', 'skipped', 'error')),
    duration_ms   INTEGER NOT NULL DEFAULT 0,
    retries       INTEGER NOT NULL DEFAULT 0,
    assertions    INTEGER NOT NULL DEFAULT -1,
    finished_at   TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX test_results_test ON test_results (repository_id, test_id, finished_at);
CREATE INDEX test_results_run ON test_results (repository_id, run_id, run_attempt);
CREATE INDEX test_results_finished ON test_results (finished_at);

-- CPU and memory of one job, sampled by the agent every 5 s (R5.19).
CREATE TABLE job_resource_samples (
    job_id       INTEGER NOT NULL,
    sampled_at   TEXT NOT NULL,
    machine_id   TEXT NOT NULL DEFAULT '',
    runner_id    TEXT NOT NULL DEFAULT '',
    cpu_millis   INTEGER NOT NULL DEFAULT 0,                       -- CPU cores used x 1000
    memory_bytes INTEGER NOT NULL DEFAULT 0,
    known        INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (job_id, sampled_at)
) STRICT;

CREATE INDEX job_resource_samples_at ON job_resource_samples (sampled_at);

-- The summary written when the job ends (R5.20).
CREATE TABLE job_resource_summary (
    job_id          INTEGER PRIMARY KEY,
    job_ident       TEXT NOT NULL DEFAULT '',
    machine_id      TEXT NOT NULL DEFAULT '',
    samples         INTEGER NOT NULL DEFAULT 0,
    cpu_p50_millis  INTEGER NOT NULL DEFAULT 0,
    cpu_p95_millis  INTEGER NOT NULL DEFAULT 0,
    cpu_peak_millis INTEGER NOT NULL DEFAULT 0,
    mem_p50_bytes   INTEGER NOT NULL DEFAULT 0,
    mem_p95_bytes   INTEGER NOT NULL DEFAULT 0,
    mem_peak_bytes  INTEGER NOT NULL DEFAULT 0,
    unknown_reason  TEXT NOT NULL DEFAULT '',
    completed_at    TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX job_resource_summary_key ON job_resource_summary (job_ident, completed_at);

-- Workflow file checks per commit (R5.29).
CREATE TABLE workflow_checks (
    repository_id INTEGER NOT NULL,
    head_sha      TEXT NOT NULL,
    workflow      TEXT NOT NULL,                                   -- the file path, .github/workflows/ci.yml
    check_id      TEXT NOT NULL,                                   -- workflows.CheckID, such as workflow.timeout
    job           TEXT NOT NULL DEFAULT '',                        -- the job id in the file, empty for the file
    status        TEXT NOT NULL CHECK (status IN ('ok', 'fail', 'skip')),
    line          INTEGER NOT NULL DEFAULT 0,
    detail        TEXT NOT NULL DEFAULT '',
    guardrail     TEXT NOT NULL DEFAULT '',
    default_branch INTEGER NOT NULL DEFAULT 0,                     -- 1: a default-branch commit
    checked_at    TEXT NOT NULL,
    PRIMARY KEY (repository_id, head_sha, workflow, check_id, job)
) STRICT;

CREATE INDEX workflow_checks_latest ON workflow_checks (repository_id, default_branch, checked_at);

-- Guardrail state per repository, from the guard report or crsp's own checks (R5.31, R5.32).
CREATE TABLE guardrail_reports (
    repository_id INTEGER NOT NULL,
    run_id        INTEGER NOT NULL DEFAULT 0,                      -- 0 for a crsp check
    head_sha      TEXT NOT NULL DEFAULT '',
    guardrail     TEXT NOT NULL CHECK (guardrail IN ('G1', 'G2', 'G3', 'G4', 'G5', 'G6', 'G7')),
    source        TEXT NOT NULL CHECK (source IN ('guard_report', 'crsp')),
    state         TEXT NOT NULL CHECK (state IN ('pass', 'break', 'off', 'unknown')),
    count         INTEGER NOT NULL DEFAULT 0,
    known_count   INTEGER NOT NULL DEFAULT -1,                     -- the guard's known list, -1 not reported
    reason        TEXT NOT NULL DEFAULT '',
    breaks        TEXT NOT NULL DEFAULT '[]',                      -- JSON array of {file, line, message}
    reported_at   TEXT NOT NULL,
    PRIMARY KEY (repository_id, run_id, guardrail, source)
) STRICT;

CREATE INDEX guardrail_reports_latest ON guardrail_reports (repository_id, guardrail, reported_at);

-- The weekly local digest files (R5.38).
CREATE TABLE insight_digests (
    week       TEXT NOT NULL PRIMARY KEY,                          -- 2026-W47
    path       TEXT NOT NULL,                                      -- relative to the data dir
    top        TEXT NOT NULL DEFAULT '[]',                         -- JSON array of finding ids
    written_at TEXT NOT NULL
) STRICT;

-- The Job page faults (R5.39 to R5.41): who ran the job, where its steps came from, when crsp last re-read it.
ALTER TABLE jobs ADD COLUMN runner_origin TEXT NOT NULL DEFAULT '' CHECK (runner_origin IN ('', 'crsp', 'other', 'hosted'));
ALTER TABLE jobs ADD COLUMN runner_group TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN runner_labels TEXT NOT NULL DEFAULT '[]';
ALTER TABLE jobs ADD COLUMN steps_source TEXT NOT NULL DEFAULT '' CHECK (steps_source IN ('', 'events', 'rest'));
ALTER TABLE jobs ADD COLUMN checked_at TEXT NOT NULL DEFAULT '';

CREATE INDEX jobs_stale ON jobs (state, updated_at);

-- +goose Down
DROP INDEX jobs_stale;
ALTER TABLE jobs DROP COLUMN checked_at;
ALTER TABLE jobs DROP COLUMN steps_source;
ALTER TABLE jobs DROP COLUMN runner_labels;
ALTER TABLE jobs DROP COLUMN runner_group;
ALTER TABLE jobs DROP COLUMN runner_origin;
DROP TABLE insight_digests;
DROP TABLE guardrail_reports;
DROP TABLE workflow_checks;
DROP TABLE job_resource_summary;
DROP TABLE job_resource_samples;
DROP TABLE test_results;
DROP TABLE ci_report_files;
DROP TABLE ci_job_logs;
DROP TABLE insight_plans;
DROP TABLE insight_analyser_runs;
DROP TABLE insight_findings;
