-- +goose Up
-- CRSP286: console run and job lists at 10k runs and 100k jobs (audit P-3).
-- The run page is ordered by the same expression as runSortKey in jobsdata_read.go; an expression index lets the
-- page stop at LIMIT instead of sorting every run.
CREATE INDEX runs_sort_key ON runs (((CAST(COALESCE(strftime('%s', created_at), 0) AS INTEGER) << 31) | ((run_id & 16777215) << 7) | (run_attempt & 127)) DESC);
CREATE INDEX runs_conclusion ON runs (conclusion);
CREATE INDEX runs_repository ON runs (repository_id);
CREATE INDEX runs_branch ON runs (head_branch);
-- Counting the jobs of a run by state (the per-run counts and the state filter) reads this index only.
CREATE INDEX jobs_run_state ON jobs (repository_id, run_id, run_attempt, state);

-- +goose Down
DROP INDEX jobs_run_state;
DROP INDEX runs_branch;
DROP INDEX runs_repository;
DROP INDEX runs_conclusion;
DROP INDEX runs_sort_key;
