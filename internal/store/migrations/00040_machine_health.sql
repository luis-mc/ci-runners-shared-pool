-- +goose Up
-- OPS83: machine health (machines R5.70 to R5.77) and the job stats per machine and per runner.
-- machine_health: the last HealthReport of each machine, as protojson, with the OOM state the controller derived.
-- machine_disk_samples: 24 hours of free space per volume, pruned; the days until full come from the trend.
-- job_end_reasons: why crsp ended a job (its timer, memory, a stop); a job without a row ended by itself or by GitHub.
CREATE TABLE machine_health (
    machine_id  TEXT PRIMARY KEY,
    received_at TEXT NOT NULL,
    report      TEXT NOT NULL,
    oom_total   INTEGER NOT NULL DEFAULT 0,
    last_oom_at TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE TABLE machine_disk_samples (
    machine_id  TEXT NOT NULL,
    role        TEXT NOT NULL,
    path        TEXT NOT NULL,
    sampled_at  TEXT NOT NULL,
    free_bytes  INTEGER NOT NULL,
    total_bytes INTEGER NOT NULL,
    PRIMARY KEY (machine_id, role, path, sampled_at)
) STRICT, WITHOUT ROWID;

CREATE INDEX machine_disk_samples_at ON machine_disk_samples (sampled_at);

CREATE TABLE job_end_reasons (
    job_id      INTEGER PRIMARY KEY,
    reason      TEXT NOT NULL CHECK (reason IN ('timeout', 'over_memory', 'stopped')),
    detail      TEXT NOT NULL DEFAULT '',
    recorded_at TEXT NOT NULL
) STRICT;

-- The job stats read a machine or a runner over a window of completed_at.
CREATE INDEX jobs_machine_done ON jobs (machine_id, completed_at);
CREATE INDEX jobs_runner_done ON jobs (runner_id, completed_at);

-- +goose Down
DROP INDEX jobs_runner_done;
DROP INDEX jobs_machine_done;
DROP TABLE job_end_reasons;
DROP TABLE machine_disk_samples;
DROP TABLE machine_health;
