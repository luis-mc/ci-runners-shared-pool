-- +goose Up
-- The savings report reads the finished jobs of a date range (observability R5.19): serve the window by index.
CREATE INDEX jobs_done_at ON jobs (state, completed_at);

-- +goose Down
DROP INDEX jobs_done_at;
