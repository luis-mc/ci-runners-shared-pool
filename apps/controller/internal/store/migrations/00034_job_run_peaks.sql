-- +goose Up
-- OPS66: the measured CPU and memory peaks of each job run, keyed by repository, workflow, job key and runner
-- (docs/features/observability R5.60 to R5.64). One row per job run, written when the agent's final batch
-- arrives. A summary per job key (the last 30 good runs, p90 of the peaks, "measured" from 5) is read from
-- this table, so it needs no table of its own. Numbers: CPU in cores x 1000, memory in bytes.

CREATE TABLE job_run_peaks (
    job_id             INTEGER PRIMARY KEY,                        -- the GitHub job id, as job_resource_summary
    repository         TEXT NOT NULL,
    workflow           TEXT NOT NULL DEFAULT '',
    job_ident            TEXT NOT NULL,
    runner             TEXT NOT NULL DEFAULT '',
    machine_id         TEXT NOT NULL DEFAULT '',
    run_id             INTEGER NOT NULL DEFAULT 0,
    source             TEXT NOT NULL DEFAULT '',                   -- cgroup, process_tree or job_object
    peak_cpu_millis    INTEGER NOT NULL DEFAULT 0,                 -- p95 over the windows
    peak_memory_bytes  INTEGER NOT NULL DEFAULT 0,                 -- the largest reading
    samples            INTEGER NOT NULL DEFAULT 0,
    duration_ms        INTEGER NOT NULL DEFAULT 0,
    oom_killed         INTEGER NOT NULL DEFAULT 0 CHECK (oom_killed IN (0, 1)),
    cut_short          INTEGER NOT NULL DEFAULT 0 CHECK (cut_short IN (0, 1)),
    good               INTEGER NOT NULL DEFAULT 0 CHECK (good IN (0, 1)),
    unknown_reason     TEXT NOT NULL DEFAULT '',
    completed_at       TEXT NOT NULL
) STRICT;

CREATE INDEX job_run_peaks_key ON job_run_peaks (repository, workflow, job_ident, good, completed_at);
CREATE INDEX job_run_peaks_runner ON job_run_peaks (repository, workflow, job_ident, runner, completed_at);

-- +goose Down
DROP TABLE job_run_peaks;
