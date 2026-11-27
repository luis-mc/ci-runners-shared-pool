-- +goose Up
-- CI run follow-ups, CIX24 (ci-run-insights D27): a run triggered by another run's completion (event
-- workflow_run, such as the Installers workflow after CI) is linked to the run that started it, so the
-- console can say a run is not finished while its follow-ups are. One row per follow-up run. source says how the
-- link is known: 'payload' when GitHub named the triggering run, 'inferred' when crsp matched the head sha and
-- the completion time (an inferred link is recomputed whenever a run of that sha changes).
CREATE TABLE run_follow_ups (
    repository_id INTEGER NOT NULL,
    run_id        INTEGER NOT NULL,
    parent_run_id INTEGER NOT NULL,
    source        TEXT NOT NULL CHECK (source IN ('payload', 'inferred')),
    updated_at    TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (repository_id, run_id)
) STRICT;

CREATE INDEX run_follow_ups_parent ON run_follow_ups (repository_id, parent_run_id);

-- +goose Down
DROP TABLE run_follow_ups;
