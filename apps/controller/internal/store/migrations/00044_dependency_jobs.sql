-- +goose Up
-- CRSP248: dependency setup jobs (dependency-setup DEP7, DEP10, DEP11). One row per job on a machine. The
-- record is kept so a controller restart, an agent restart or a machine reboot never loses the plan: a job
-- paused for a restart resumes at Verify when the agent returns. `added` is the undo list; `log` holds the
-- last 300 events (oldest first); `extra` holds the licence tick, the manual steps and the restart flag.
CREATE TABLE dependency_jobs (
    id             TEXT PRIMARY KEY,
    machine_id     TEXT NOT NULL,
    state          TEXT NOT NULL CHECK (state IN ('consented', 'installing', 'paused_restart', 'verifying', 'preparing', 'done', 'failed', 'stopped', 'undoing', 'undone')),
    choice_id      TEXT NOT NULL,
    item_ids       TEXT NOT NULL DEFAULT '[]',
    runner_os      TEXT NOT NULL,
    runner_kind    TEXT NOT NULL,
    actor          TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    error          TEXT NOT NULL DEFAULT '',
    engine_version TEXT NOT NULL DEFAULT '',
    added          TEXT NOT NULL DEFAULT '[]',
    last_event     TEXT NOT NULL DEFAULT '',
    log            TEXT NOT NULL DEFAULT '[]',
    extra          TEXT NOT NULL DEFAULT '{}'
) STRICT;
CREATE INDEX dependency_jobs_machine ON dependency_jobs (machine_id, created_at);

-- +goose Down
DROP INDEX dependency_jobs_machine;
DROP TABLE dependency_jobs;
