-- +goose Up
-- Milestone 1: machines, their keys, join codes, settings, load and changes
-- that wait for a drain (machines R5.1 to R5.39, R6.2, R6.8; scheduling R5.1).
-- Join codes and machine keys are stored only as hashes (R6.2).

CREATE TABLE machines (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    os            TEXT NOT NULL CHECK (os IN ('linux', 'windows', 'macos')),
    arch          TEXT NOT NULL CHECK (arch IN ('x64', 'arm64')),
    facts         TEXT NOT NULL DEFAULT '{}',     -- JSON (R5.10)
    backends      TEXT NOT NULL DEFAULT '[]',     -- JSON, as detected (R5.11)
    state         TEXT NOT NULL DEFAULT 'active'
                  CHECK (state IN ('active', 'draining', 'drained', 'removing')),
    is_controller INTEGER NOT NULL DEFAULT 0 CHECK (is_controller IN (0, 1)),
    agent_version TEXT NOT NULL DEFAULT '',
    online        INTEGER NOT NULL DEFAULT 0 CHECK (online IN (0, 1)),
    last_seen_at  TEXT,
    joined_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    removed_at    TEXT
) STRICT;

CREATE TABLE machine_keys (
    hash       TEXT PRIMARY KEY,
    machine_id TEXT NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    revoked_at TEXT
) STRICT;

CREATE INDEX machine_keys_machine ON machine_keys (machine_id);

CREATE TABLE join_codes (
    id         TEXT PRIMARY KEY,
    code_hash  TEXT NOT NULL UNIQUE,
    kind       TEXT NOT NULL DEFAULT 'code' CHECK (kind IN ('code', 'local')),
    created_by TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    expires_at TEXT NOT NULL,
    used_at    TEXT,
    revoked_at TEXT,
    machine_id TEXT REFERENCES machines (id) ON DELETE SET NULL
) STRICT;

-- One settings row per machine: machines R5.22 and scheduling R5.1 together
-- (the scheduling spec's machine_sched lives here).
CREATE TABLE machine_settings (
    machine_id            TEXT PRIMARY KEY REFERENCES machines (id) ON DELETE CASCADE,
    version               INTEGER NOT NULL DEFAULT 1,
    budget_memory_pct     INTEGER NOT NULL DEFAULT 85 CHECK (budget_memory_pct BETWEEN 10 AND 95),
    budget_cpu_pct        INTEGER NOT NULL DEFAULT 60 CHECK (budget_cpu_pct BETWEEN 10 AND 95),
    job_slots             INTEGER NOT NULL DEFAULT 2 CHECK (job_slots BETWEEN 0 AND 64),
    light_slots           INTEGER NOT NULL DEFAULT 1 CHECK (light_slots BETWEEN 0 AND 64),
    heavy_slots           INTEGER NOT NULL DEFAULT 1 CHECK (heavy_slots BETWEEN 0 AND 64),
    light_jobs            TEXT NOT NULL DEFAULT '[]',
    heavy_jobs            TEXT NOT NULL DEFAULT '[]',
    slot_max_age_minutes  INTEGER NOT NULL DEFAULT 120 CHECK (slot_max_age_minutes > 0),
    gate_poll_seconds     INTEGER NOT NULL DEFAULT 15 CHECK (gate_poll_seconds > 0),
    gate_max_wait_minutes INTEGER NOT NULL DEFAULT 30 CHECK (gate_max_wait_minutes > 0),
    gate_on_timeout       TEXT NOT NULL DEFAULT 'fail' CHECK (gate_on_timeout IN ('fail', 'run')),
    allowed_backends      TEXT NOT NULL DEFAULT '["persistent"]',
    cache_cap_bytes       INTEGER NOT NULL DEFAULT 21474836480, -- 20 GiB
    drain_timeout_minutes INTEGER NOT NULL DEFAULT 60 CHECK (drain_timeout_minutes BETWEEN 5 AND 1440),
    windows_symlink_right INTEGER NOT NULL DEFAULT 0 CHECK (windows_symlink_right IN (0, 1)),
    updated_at            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_by            TEXT NOT NULL DEFAULT 'system'
) STRICT;

-- Load at 15-second steps for 24 hours (R6.8); older rows are dropped.
CREATE TABLE machine_load (
    machine_id TEXT NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    sampled_at TEXT NOT NULL,
    memory_pct REAL,
    cpu_pct    REAL,
    slots_used INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (machine_id, sampled_at)
) STRICT;

-- A change that waits for a drain, and what it waits for (R5.30 to R5.34).
CREATE TABLE machine_changes (
    id          TEXT PRIMARY KEY,
    machine_id  TEXT NOT NULL REFERENCES machines (id) ON DELETE CASCADE,
    kind        TEXT NOT NULL
                CHECK (kind IN ('drain', 'settings', 'remove', 'agent_update', 'runner_update', 'service_restart')),
    state       TEXT NOT NULL DEFAULT 'waiting'
                CHECK (state IN ('waiting', 'running', 'done', 'cancelled', 'needs_you')),
    detail      TEXT NOT NULL DEFAULT '{}',       -- JSON: the settings to apply, the jobs waited for
    started_by  TEXT NOT NULL,
    started_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    deadline_at TEXT NOT NULL,
    finished_at TEXT
) STRICT;

CREATE INDEX machine_changes_open ON machine_changes (machine_id, state);

-- Pool defaults for new machines live in the settings table (00008).

-- +goose Down
DROP TABLE machine_changes;
DROP TABLE machine_load;
DROP TABLE machine_settings;
DROP TABLE join_codes;
DROP TABLE machine_keys;
DROP TABLE machines;
