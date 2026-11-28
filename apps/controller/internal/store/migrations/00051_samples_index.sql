-- +goose Up
-- CRSP290: the forecast reads the newest finished jobs of one job key in one repository (audit F2). With
-- (job_ident, completed_at) the repository was a filter after the index, and the repository fallback sorted every
-- finished job of the repository; this index serves both reads in order.
CREATE INDEX jobs_sample ON jobs (repository_id, job_ident, completed_at);

-- +goose Down
DROP INDEX jobs_sample;
