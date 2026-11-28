-- +goose Up
-- CRSP292 (audit round 3, R3-3): the stats hour grid, the insights wait source and the from/to filter of the job list
-- read jobs by created_at. Without an index each one scanned the whole table, so the cost grew with the age of the
-- database and not with the window.
CREATE INDEX jobs_created ON jobs (created_at);

-- +goose Down
DROP INDEX jobs_created;
