-- +goose Up
-- The private image registry (docs/features/registry, REG10 contract; RES20 section 9; owner decisions of
-- 19 Nov 2026). Blobs live in <data_dir>/registry (Distribution v3); this store keeps the records: images,
-- tags, layers, pointers per repository, pulls, builds, recommendations, replace plans, scans and settings.
-- Times are UTC RFC3339 text, an empty string means unknown or never. Sizes are bytes. The owning task of
-- each table is named; only that task writes it.

-- REG1 writes at promotion, REG4 keeps sizes, REG3 lineage, REG6 and REG7 states.
CREATE TABLE registry_images (
    id                TEXT PRIMARY KEY,                       -- img_<random>
    kind              TEXT NOT NULL CHECK (kind IN ('standard', 'custom')),
    name              TEXT NOT NULL,                          -- crsp/std/<profile> or crsp/custom/<owner>-<repo>/<profile>
    image_tag         TEXT NOT NULL CHECK (length(image_tag) = 12),  -- the image key; it is also the tag
    profile_id        TEXT NOT NULL,
    arch              TEXT NOT NULL CHECK (arch IN ('amd64', 'arm64')),
    state             TEXT NOT NULL DEFAULT 'staged'
                      CHECK (state IN ('staged', 'refused', 'current', 'variant', 'superseded', 'deleted')),
    manifest_digest   TEXT NOT NULL,
    staged_name       TEXT NOT NULL DEFAULT '',               -- crsp/incoming/<machine>/...
    stack_hash        TEXT NOT NULL,
    base_name         TEXT NOT NULL DEFAULT '',
    base_digest       TEXT NOT NULL,
    key_input         TEXT NOT NULL DEFAULT '{}',             -- the canonical JSON the key hashes
    source_repository TEXT NOT NULL DEFAULT '',
    commit_sha        TEXT NOT NULL DEFAULT '',
    branch            TEXT NOT NULL DEFAULT '',               -- empty: the default branch
    branch_gone_at    TEXT NOT NULL DEFAULT '',               -- the branch merged or was deleted (REG6)
    pushed_by         TEXT NOT NULL DEFAULT '',               -- machine id
    pushed_at         TEXT NOT NULL DEFAULT '',
    promoted_at       TEXT NOT NULL DEFAULT '',
    refused_why       TEXT NOT NULL DEFAULT '',
    replaces_id       TEXT NOT NULL DEFAULT '',
    replaced_by_id    TEXT NOT NULL DEFAULT '',
    keep_until        TEXT NOT NULL DEFAULT '',
    size_bytes        INTEGER NOT NULL DEFAULT 0,
    unique_bytes      INTEGER NOT NULL DEFAULT 0,
    last_pull_at      TEXT NOT NULL DEFAULT '',
    scan_state        TEXT NOT NULL DEFAULT 'not_scanned'
                      CHECK (scan_state IN ('not_scanned', 'queued', 'scanning', 'done', 'failed')),
    deleted_at        TEXT NOT NULL DEFAULT '',
    deleted_by        TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

CREATE INDEX registry_images_key ON registry_images (image_tag, arch);
CREATE INDEX registry_images_state ON registry_images (state, kind);
CREATE INDEX registry_images_profile ON registry_images (profile_id, arch);

-- REG1. Tags are immutable: one row per (name, tag), the digest never changes.
CREATE TABLE registry_image_tags (
    name            TEXT NOT NULL,
    tag             TEXT NOT NULL,
    image_id        TEXT NOT NULL REFERENCES registry_images (id),
    manifest_digest TEXT NOT NULL,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (name, tag)
) STRICT;

-- +goose StatementBegin
CREATE TRIGGER registry_image_tags_immutable BEFORE UPDATE OF manifest_digest, name, tag ON registry_image_tags
BEGIN
    SELECT RAISE(ABORT, 'registry tags are immutable');
END;
-- +goose StatementEnd

-- REG4: layer accounting. charged_to is the kind of the oldest image that holds the layer.
CREATE TABLE registry_layers (
    digest        TEXT PRIMARY KEY,
    size_bytes    INTEGER NOT NULL DEFAULT 0,
    media_type    TEXT NOT NULL DEFAULT '',
    charged_to    TEXT NOT NULL DEFAULT 'standard' CHECK (charged_to IN ('standard', 'custom')),
    first_seen_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

CREATE TABLE registry_image_layers (
    image_id     TEXT NOT NULL REFERENCES registry_images (id),
    layer_digest TEXT NOT NULL REFERENCES registry_layers (digest),
    position     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (image_id, layer_digest)
) STRICT;

CREATE INDEX registry_image_layers_layer ON registry_image_layers (layer_digest);

-- REG3 (writes when a standard pin moves), REG7 (writes on replace and rollback). A repository that moves to
-- another image keeps no hold on the old one; another repository's pointer keeps it.
CREATE TABLE registry_pointers (
    repository        TEXT NOT NULL,                          -- owner/name
    profile_id        TEXT NOT NULL,
    arch              TEXT NOT NULL CHECK (arch IN ('amd64', 'arm64')),
    image_id          TEXT NOT NULL REFERENCES registry_images (id),
    previous_image_id TEXT NOT NULL DEFAULT '',
    moved_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    moved_by          TEXT NOT NULL DEFAULT 'crsp',
    PRIMARY KEY (repository, profile_id, arch)
) STRICT;

CREATE INDEX registry_pointers_image ON registry_pointers (image_id);

-- REG4 from REG2's PullReport: one row per pull or local use. Kept 30 days; registry_pull_daily 180 days.
CREATE TABLE registry_pulls (
    id              INTEGER PRIMARY KEY,
    pull_id         TEXT NOT NULL UNIQUE,                     -- the agent's id, so a resent report is one row
    machine_id      TEXT NOT NULL,
    image_id        TEXT NOT NULL DEFAULT '',
    image_tag       TEXT NOT NULL,
    manifest_digest TEXT NOT NULL DEFAULT '',
    source          TEXT NOT NULL CHECK (source IN ('local', 'pulled', 'built')),
    state           TEXT NOT NULL DEFAULT 'pulling'
                    CHECK (state IN ('pulling', 'waiting_controller', 'done', 'failed')),
    bytes_done      INTEGER NOT NULL DEFAULT 0,
    bytes_total     INTEGER NOT NULL DEFAULT 0,
    seconds         REAL NOT NULL DEFAULT 0,
    saved_seconds   REAL NOT NULL DEFAULT 0,
    repository      TEXT NOT NULL DEFAULT '',
    job_id          TEXT NOT NULL DEFAULT '',
    error           TEXT NOT NULL DEFAULT '',
    started_at      TEXT NOT NULL DEFAULT '',
    finished_at     TEXT NOT NULL DEFAULT '',
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

CREATE INDEX registry_pulls_image ON registry_pulls (image_id, started_at);
CREATE INDEX registry_pulls_machine ON registry_pulls (machine_id, started_at);

CREATE TABLE registry_pull_daily (
    image_id      TEXT NOT NULL,
    day           TEXT NOT NULL,                              -- YYYY-MM-DD, controller local time
    pulls         INTEGER NOT NULL DEFAULT 0,
    builds_saved  INTEGER NOT NULL DEFAULT 0,
    saved_seconds REAL NOT NULL DEFAULT 0,
    PRIMARY KEY (image_id, day)
) STRICT;

-- REG4 from REG2's PushReport: a push to staging and its outcome.
CREATE TABLE registry_pushes (
    id              INTEGER PRIMARY KEY,
    push_id         TEXT NOT NULL UNIQUE,
    machine_id      TEXT NOT NULL,
    image_tag       TEXT NOT NULL,
    staged_name     TEXT NOT NULL,
    target_name     TEXT NOT NULL,
    manifest_digest TEXT NOT NULL DEFAULT '',
    state           TEXT NOT NULL DEFAULT 'pushing'
                    CHECK (state IN ('pushing', 'waiting_controller', 'pushed', 'promoted', 'refused', 'failed')),
    image_id        TEXT NOT NULL DEFAULT '',
    seconds         REAL NOT NULL DEFAULT 0,
    error           TEXT NOT NULL DEFAULT '',
    started_at      TEXT NOT NULL DEFAULT '',
    finished_at     TEXT NOT NULL DEFAULT '',
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

-- IMG1: build records (RES19 7). One row per build of (machine, profile, arch, key). REG2 sets push_state.
CREATE TABLE image_builds (
    id              TEXT PRIMARY KEY,                         -- bld_<random>
    machine_id      TEXT NOT NULL,
    profile_id      TEXT NOT NULL DEFAULT '',
    arch            TEXT NOT NULL DEFAULT '',
    image_tag       TEXT NOT NULL,
    reason          TEXT NOT NULL DEFAULT ''
                    CHECK (reason IN ('', 'first_runner', 'layers_changed', 'pin_updated', 'base_updated', 'asked', 'stack_changed', 'not_in_registry')),
    state           TEXT NOT NULL DEFAULT 'queued'
                    CHECK (state IN ('queued', 'building', 'done', 'failed', 'cancelled')),
    step            INTEGER NOT NULL DEFAULT 0,
    steps_total     INTEGER NOT NULL DEFAULT 0,
    step_name       TEXT NOT NULL DEFAULT '',
    typical_seconds REAL NOT NULL DEFAULT 0,
    size_bytes      INTEGER NOT NULL DEFAULT 0,
    failure_step    TEXT NOT NULL DEFAULT '',
    failure_class   TEXT NOT NULL DEFAULT ''
                    CHECK (failure_class IN ('', 'network', 'checksum', 'disk', 'apt', 'timeout', 'other')),
    failure_why     TEXT NOT NULL DEFAULT '',
    log_tail        TEXT NOT NULL DEFAULT '',                 -- redacted, capped
    push_state      TEXT NOT NULL DEFAULT 'none'
                    CHECK (push_state IN ('none', 'pushing', 'waiting_controller', 'pushed', 'promoted', 'refused', 'failed')),
    queued_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    started_at      TEXT NOT NULL DEFAULT '',
    finished_at     TEXT NOT NULL DEFAULT '',
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

CREATE INDEX image_builds_machine ON image_builds (machine_id, queued_at DESC);
CREATE INDEX image_builds_profile ON image_builds (profile_id, arch, state);

-- REG6. One open row per (rule, image_id, kind). No dismiss column: a recommendation cannot be dismissed.
CREATE TABLE registry_recommendations (
    id             TEXT PRIMARY KEY,
    rule           TEXT NOT NULL
                   CHECK (rule IN ('replace', 'superseded', 'variant', 'unused', 'over_budget', 'dangling', 'profile_removed')),
    image_id       TEXT NOT NULL DEFAULT '',
    other_image_id TEXT NOT NULL DEFAULT '',
    kind           TEXT NOT NULL DEFAULT '' CHECK (kind IN ('', 'standard', 'custom')),
    frees_bytes    INTEGER NOT NULL DEFAULT 0,
    decider        TEXT NOT NULL CHECK (decider IN ('owner', 'crsp')),
    due_at         TEXT NOT NULL DEFAULT '',
    state          TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'done', 'gone')),
    closed_why     TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

CREATE UNIQUE INDEX registry_recommendations_open ON registry_recommendations (rule, image_id, kind) WHERE state = 'open';

-- REG7.
CREATE TABLE registry_replace_plans (
    id            TEXT PRIMARY KEY,                           -- rpl_<random>
    new_image_id  TEXT NOT NULL REFERENCES registry_images (id),
    old_image_id  TEXT NOT NULL REFERENCES registry_images (id),
    keep_days     INTEGER NOT NULL DEFAULT 7 CHECK (keep_days BETWEEN 0 AND 365),
    keep_until    TEXT NOT NULL DEFAULT '',
    repositories  TEXT NOT NULL DEFAULT '[]',                 -- JSON array of owner/name
    machines      TEXT NOT NULL DEFAULT '[]',                 -- JSON array of machine ids to warm
    state         TEXT NOT NULL DEFAULT 'draft'
                  CHECK (state IN ('draft', 'running', 'stopped', 'failed', 'done', 'rolled_back')),
    steps_done    INTEGER NOT NULL DEFAULT 0,
    steps_total   INTEGER NOT NULL DEFAULT 0,
    error         TEXT NOT NULL DEFAULT '',
    created_by    TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    started_at    TEXT NOT NULL DEFAULT '',
    finished_at   TEXT NOT NULL DEFAULT '',
    rolled_back_by TEXT NOT NULL DEFAULT '',
    rolled_back_at TEXT NOT NULL DEFAULT ''
) STRICT;

-- REG8: scan work rows and results. The SBOM and report are OCI referrers in the registry, named here.
CREATE TABLE registry_scans (
    image_id       TEXT PRIMARY KEY REFERENCES registry_images (id),
    state          TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'scanning', 'done', 'failed')),
    machine_id     TEXT NOT NULL DEFAULT '',
    claimed_at     TEXT NOT NULL DEFAULT '',
    attempts       INTEGER NOT NULL DEFAULT 0,
    packages       INTEGER NOT NULL DEFAULT 0,
    critical       INTEGER NOT NULL DEFAULT 0,
    high           INTEGER NOT NULL DEFAULT 0,
    medium         INTEGER NOT NULL DEFAULT 0,
    low            INTEGER NOT NULL DEFAULT 0,
    sbom_digest    TEXT NOT NULL DEFAULT '',
    report_digest  TEXT NOT NULL DEFAULT '',
    error          TEXT NOT NULL DEFAULT '',
    queued_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    finished_at    TEXT NOT NULL DEFAULT ''
) STRICT;

-- REG6. One row. Budgets never above half the free disk (checked in Go on save).
CREATE TABLE registry_settings (
    id                 INTEGER PRIMARY KEY CHECK (id = 1),
    folder             TEXT NOT NULL DEFAULT '',              -- empty: <data_dir>/registry
    standard_budget    INTEGER NOT NULL DEFAULT 40000000000,
    custom_budget      INTEGER NOT NULL DEFAULT 30000000000,
    unused_days        INTEGER NOT NULL DEFAULT 30 CHECK (unused_days BETWEEN 1 AND 3650),
    keep_old_days      INTEGER NOT NULL DEFAULT 7 CHECK (keep_old_days BETWEEN 0 AND 365),
    variant_gone_days  INTEGER NOT NULL DEFAULT 7 CHECK (variant_gone_days BETWEEN 0 AND 365),
    variant_unused_days INTEGER NOT NULL DEFAULT 14 CHECK (variant_unused_days BETWEEN 1 AND 365),
    cleanup_weekday    INTEGER NOT NULL DEFAULT 0 CHECK (cleanup_weekday BETWEEN 0 AND 6),
    cleanup_hour       INTEGER NOT NULL DEFAULT 3 CHECK (cleanup_hour BETWEEN 0 AND 23),
    read_only          INTEGER NOT NULL DEFAULT 0 CHECK (read_only IN (0, 1)),
    updated_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_by         TEXT NOT NULL DEFAULT ''
) STRICT;

INSERT INTO registry_settings (id) VALUES (1);

-- REG6: each cleanup run (weekly or Clean up now).
CREATE TABLE registry_cleanups (
    id          INTEGER PRIMARY KEY,
    trigger     TEXT NOT NULL CHECK (trigger IN ('weekly', 'now')),
    state       TEXT NOT NULL DEFAULT 'running' CHECK (state IN ('running', 'done', 'failed')),
    freed_bytes INTEGER NOT NULL DEFAULT 0,
    error       TEXT NOT NULL DEFAULT '',
    started_by  TEXT NOT NULL DEFAULT 'crsp',
    started_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    finished_at TEXT NOT NULL DEFAULT ''
) STRICT;

-- REG9. One row.
CREATE TABLE registry_mirror_settings (
    id                  INTEGER PRIMARY KEY CHECK (id = 1),
    enabled             INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    upstreams           TEXT NOT NULL DEFAULT '["docker.io","ghcr.io","mcr.microsoft.com","quay.io"]',
    folder              TEXT NOT NULL DEFAULT '',             -- empty: <data_dir>/registry-mirror
    budget_bytes        INTEGER NOT NULL DEFAULT 20000000000,
    tag_recheck_minutes INTEGER NOT NULL DEFAULT 60 CHECK (tag_recheck_minutes BETWEEN 1 AND 10080),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_by          TEXT NOT NULL DEFAULT ''
) STRICT;

INSERT INTO registry_mirror_settings (id) VALUES (1);

-- REG1: tokens not yet ended that name a manifest; GC and delete wait for them (RES20 8.4). No token value.
CREATE TABLE registry_token_leases (
    jti        TEXT PRIMARY KEY,
    machine_id TEXT NOT NULL,
    names      TEXT NOT NULL DEFAULT '[]',                    -- JSON array of repository names it may pull
    expires_at TEXT NOT NULL
) STRICT;

CREATE INDEX registry_token_leases_expiry ON registry_token_leases (expires_at);

-- +goose Down
DROP TABLE registry_token_leases;
DROP TABLE registry_mirror_settings;
DROP TABLE registry_cleanups;
DROP TABLE registry_settings;
DROP TABLE registry_scans;
DROP TABLE registry_replace_plans;
DROP TABLE registry_recommendations;
DROP TABLE image_builds;
DROP TABLE registry_pushes;
DROP TABLE registry_pull_daily;
DROP TABLE registry_pulls;
DROP TABLE registry_pointers;
DROP TABLE registry_image_layers;
DROP TABLE registry_layers;
DROP TRIGGER registry_image_tags_immutable;
DROP TABLE registry_image_tags;
DROP TABLE registry_images;
