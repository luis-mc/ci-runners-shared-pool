-- +goose Up
-- CRSP25: the controller writes RUNS_ON_LINUX, RUNS_ON_WINDOWS and RUNS_ON_MACOS
-- (github-integration R5.28). The table accepts both the new names and the old
-- CRSP_RUNS_ON_* names; the controller deletes the old variables from GitHub
-- and then their rows, so an upgrade leaves no old name behind.
CREATE TABLE managed_variables_new (
    repository_id INTEGER NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    name          TEXT NOT NULL CHECK (name IN (
        'RUNS_ON_LINUX', 'RUNS_ON_WINDOWS', 'RUNS_ON_MACOS',
        'CRSP_RUNS_ON_LINUX', 'CRSP_RUNS_ON_WINDOWS', 'CRSP_RUNS_ON_MACOS')),
    value         TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'ok' CHECK (status IN ('ok', 'missing', 'wrong', 'not_managed')),
    written_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    checked_at    TEXT,
    PRIMARY KEY (repository_id, name)
) STRICT;
INSERT INTO managed_variables_new SELECT repository_id, name, value, status, written_at, checked_at FROM managed_variables;
DROP TABLE managed_variables;
ALTER TABLE managed_variables_new RENAME TO managed_variables;

-- +goose Down
DELETE FROM managed_variables WHERE name LIKE 'RUNS_ON_%';
CREATE TABLE managed_variables_old (
    repository_id INTEGER NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    name          TEXT NOT NULL CHECK (name IN ('CRSP_RUNS_ON_LINUX', 'CRSP_RUNS_ON_WINDOWS', 'CRSP_RUNS_ON_MACOS')),
    value         TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'ok' CHECK (status IN ('ok', 'missing', 'wrong', 'not_managed')),
    written_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    checked_at    TEXT,
    PRIMARY KEY (repository_id, name)
) STRICT;
INSERT INTO managed_variables_old SELECT repository_id, name, value, status, written_at, checked_at FROM managed_variables;
DROP TABLE managed_variables;
ALTER TABLE managed_variables_old RENAME TO managed_variables;
