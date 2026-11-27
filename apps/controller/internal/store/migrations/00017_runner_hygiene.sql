-- +goose Up
-- OPS28: a persistent runner keeps its downloaded dependencies between jobs unless
-- told otherwise (default on, also for rows made before), the size of its own
-- dependency cache, and the reason it is unclean after a failed clean-up
-- (runners R5.90, R5.91).
ALTER TABLE runners ADD COLUMN keep_dependencies INTEGER NOT NULL DEFAULT 1 CHECK (keep_dependencies IN (0, 1));
ALTER TABLE runners ADD COLUMN dependency_cache_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runners ADD COLUMN unclean_reason TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE runners DROP COLUMN unclean_reason;
ALTER TABLE runners DROP COLUMN dependency_cache_bytes;
ALTER TABLE runners DROP COLUMN keep_dependencies;
