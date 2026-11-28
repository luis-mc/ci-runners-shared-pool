-- +goose Up
-- CRSP331: debug access to a running job (docs/features/debug-access R5.1 to R5.4,
-- R5.17 to R5.19, R5.24 to R5.26, R6.4, R6.6).
--
-- debug_repo_settings holds the per-repository switch. A repository with no row
-- is off (R5.1): a new repository, one added by a config import and one the
-- controller has never seen all read as off. The CHECKs hold the spec's ranges
-- in the database too, so no path can widen them.
--
-- debug_session is one row per session, written in the same transaction as its
-- audit row (R6.4). ended_at is '' while the session is open. Nothing here holds
-- a secret: the reason is the user's one line, the source address is the
-- request's remote address.
--
-- debug_transcript is the redacted terminal output of one session (R5.25), at
-- most 5 MB per session (R6.6), kept until expires_at (30 days).
CREATE TABLE debug_repo_settings (
    repository_id INTEGER PRIMARY KEY,
    repository    TEXT NOT NULL,
    enabled       INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    who           TEXT NOT NULL DEFAULT 'admins' CHECK (who IN ('admins', 'owner')),
    limit_min     INTEGER NOT NULL DEFAULT 30 CHECK (limit_min BETWEEN 5 AND 120),
    idle_min      INTEGER NOT NULL DEFAULT 10 CHECK (idle_min BETWEEN 1 AND 60),
    hold_min      INTEGER NOT NULL DEFAULT 0 CHECK (hold_min BETWEEN 0 AND 30),
    persistent_ok INTEGER NOT NULL DEFAULT 0 CHECK (persistent_ok IN (0, 1)),
    updated_at    TEXT NOT NULL,
    updated_by    TEXT NOT NULL
) STRICT;

CREATE TABLE debug_session (
    id            TEXT PRIMARY KEY,
    job_id        INTEGER NOT NULL,
    repository_id INTEGER NOT NULL,
    repository    TEXT NOT NULL,
    machine_id    TEXT NOT NULL DEFAULT '',
    runner_name   TEXT NOT NULL DEFAULT '',
    backend       TEXT NOT NULL DEFAULT '',
    isolation     TEXT NOT NULL DEFAULT '',
    os            TEXT NOT NULL DEFAULT '',
    user_login    TEXT NOT NULL,
    user_id       INTEGER NOT NULL DEFAULT 0,
    reason        TEXT NOT NULL,
    source        TEXT NOT NULL,
    source_addr   TEXT NOT NULL DEFAULT '',
    opened_at     TEXT NOT NULL,
    ends_at       TEXT NOT NULL,
    last_input_at TEXT NOT NULL,
    limit_min     INTEGER NOT NULL,
    idle_min      INTEGER NOT NULL,
    extended      INTEGER NOT NULL DEFAULT 0 CHECK (extended IN (0, 1)),
    recorded      INTEGER NOT NULL DEFAULT 1 CHECK (recorded IN (0, 1)),
    ended_at      TEXT NOT NULL DEFAULT '',
    end_reason    TEXT NOT NULL DEFAULT '',
    ended_by      TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX debug_session_open ON debug_session (ended_at, job_id);
CREATE INDEX debug_session_job ON debug_session (job_id, opened_at DESC);
CREATE INDEX debug_session_opened ON debug_session (opened_at DESC);

CREATE TABLE debug_transcript (
    session_id TEXT PRIMARY KEY,
    text       TEXT NOT NULL DEFAULT '',
    bytes      INTEGER NOT NULL DEFAULT 0,
    truncated  INTEGER NOT NULL DEFAULT 0 CHECK (truncated IN (0, 1)),
    expires_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;
CREATE INDEX debug_transcript_expires ON debug_transcript (expires_at);

-- +goose Down
DROP INDEX debug_transcript_expires;
DROP TABLE debug_transcript;
DROP INDEX debug_session_opened;
DROP INDEX debug_session_job;
DROP INDEX debug_session_open;
DROP TABLE debug_session;
DROP TABLE debug_repo_settings;
