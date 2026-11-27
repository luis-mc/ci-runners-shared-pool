-- +goose Up
-- Image profiles (IMG3, docs/features/registry R5.77 to R5.80): the named sets of layers a standard or custom
-- image is built from. A profile is base, toolchains, pins, services and the labels that pick it. IMG3 writes
-- these tables. JSON columns hold canonical JSON text. Times are UTC RFC3339 text.

CREATE TABLE image_profiles (
    id           TEXT NOT NULL PRIMARY KEY,                   -- the profile id in the image key, such as node-web
    kind         TEXT NOT NULL CHECK (kind IN ('standard', 'custom')),
    repository   TEXT NOT NULL DEFAULT '',                    -- owner/repo for a custom profile, empty for a standard one
    title        TEXT NOT NULL DEFAULT '',
    base_name    TEXT NOT NULL DEFAULT '',
    base_digest  TEXT NOT NULL DEFAULT '',                    -- empty until the base is resolved
    pins         TEXT NOT NULL DEFAULT '{}',                  -- JSON object tool -> version
    toolchains   TEXT NOT NULL DEFAULT '[]',                  -- JSON array of {tool, version}
    services     TEXT NOT NULL DEFAULT '[]',                  -- JSON array of {name, image}
    labels       TEXT NOT NULL DEFAULT '[]',                  -- JSON array of runs-on labels that pick it
    stacks       TEXT NOT NULL DEFAULT '[]',                  -- JSON array of stack names that map to it
    built_in     INTEGER NOT NULL DEFAULT 0,                  -- 1: a default standard profile seeded by crsp
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

CREATE INDEX image_profiles_repository ON image_profiles (repository);

-- The profile the owner chose for a repository; without a row the stack decides.
CREATE TABLE repository_profiles (
    repository  TEXT NOT NULL PRIMARY KEY,                    -- owner/repo
    profile_id  TEXT NOT NULL REFERENCES image_profiles (id) ON DELETE CASCADE,
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

-- +goose Down
DROP TABLE repository_profiles;
DROP TABLE image_profiles;
