-- +goose Up
-- The repository state (installations, repositories with their pool switch,
-- policies, variables, findings, plans, organization pools) is kept in one row,
-- so a restart of the controller, such as an upgrade, keeps it (CRSP148).
CREATE TABLE repos_state (
    id       INTEGER PRIMARY KEY CHECK (id = 1),
    body     TEXT NOT NULL,
    saved_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

-- +goose Down
DROP TABLE repos_state;
