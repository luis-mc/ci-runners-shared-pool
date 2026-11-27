-- +goose Up
-- CRSP50: the owner's pool size per machine (machines R5.58). 0 means not set.
ALTER TABLE machine_settings ADD COLUMN pool_memory_gib REAL NOT NULL DEFAULT 0 CHECK (pool_memory_gib >= 0);
ALTER TABLE machine_settings ADD COLUMN pool_cpus INTEGER NOT NULL DEFAULT 0 CHECK (pool_cpus >= 0);

-- +goose Down
ALTER TABLE machine_settings DROP COLUMN pool_cpus;
ALTER TABLE machine_settings DROP COLUMN pool_memory_gib;
