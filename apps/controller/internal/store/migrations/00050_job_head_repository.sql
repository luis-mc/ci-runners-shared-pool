-- +goose Up
-- CRSP363: the repository a job's run came from (docs/features/debug-access R5.5).
-- GitHub names it as head_repository on a workflow run. A pull request from a
-- fork has a head repository that differs from the job's repository. One row per
-- job, written only when the source named it: no row means unknown, and unknown
-- is treated as a fork. Kept in its own table so no earlier table changes.
CREATE TABLE job_head_repository (
    job_id          INTEGER PRIMARY KEY REFERENCES jobs (id) ON DELETE CASCADE,
    head_repository TEXT NOT NULL CHECK (head_repository <> '')
) STRICT;

-- +goose Down
DROP TABLE job_head_repository;
