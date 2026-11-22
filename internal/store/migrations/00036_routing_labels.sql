-- +goose Up
-- OPS73: per-job routing labels and repository variables (github-integration
-- R5.101 to R5.108). routing_jobs: the variable crsp wrote for each routed job.
-- routing_labels: the labels crsp serves; a retiring label is removed once no
-- run created before `since` is queued or running. routing_runner_labels: the
-- custom labels crsp added to persistent runners. routing_prs: the one pull
-- request opened for a repository.

CREATE TABLE routing_jobs (
    repository TEXT NOT NULL,
    job        TEXT NOT NULL,
    var_name   TEXT NOT NULL,
    label      TEXT NOT NULL,
    class      TEXT NOT NULL,
    os         TEXT NOT NULL,
    value      TEXT NOT NULL,
    PRIMARY KEY (repository, job)
) STRICT;

CREATE TABLE routing_labels (
    repository TEXT NOT NULL,
    label      TEXT NOT NULL,
    state      TEXT NOT NULL CHECK (state IN ('active', 'retiring')),
    since      TEXT NOT NULL,
    jobs       TEXT NOT NULL DEFAULT '[]',
    PRIMARY KEY (repository, label)
) STRICT;

CREATE TABLE routing_runner_labels (
    repository  TEXT NOT NULL,
    runner_id   TEXT NOT NULL,
    github_id   INTEGER NOT NULL,
    label       TEXT NOT NULL,
    scope_owner TEXT NOT NULL,
    scope_repo  TEXT NOT NULL DEFAULT '',
    scope_org   TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (repository, runner_id, label)
) STRICT;

CREATE TABLE routing_prs (
    repository TEXT PRIMARY KEY,
    number     INTEGER NOT NULL,
    url        TEXT NOT NULL,
    opened_at  TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE routing_prs;
DROP TABLE routing_runner_labels;
DROP TABLE routing_labels;
DROP TABLE routing_jobs;
