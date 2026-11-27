-- +goose Up
-- CRSP292 (audit round 3, R3-1): the savings report, the overview, the timeline and the event-stream snapshot read the
-- done jobs that completed inside a window. With jobs_stale (state, updated_at) the planner walked every done job and
-- filtered on completed_at; with (state, completed_at) the window is one range of the index.
CREATE INDEX jobs_state_completed ON jobs (state, completed_at);

-- +goose Down
DROP INDEX jobs_state_completed;
