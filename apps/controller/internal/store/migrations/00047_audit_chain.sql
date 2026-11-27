-- +goose Up
-- CRSP257 (observability R5.28): each audit row stores the hash of the row
-- before it, and its own hash. Rows written before this change keep both
-- empty; the chain starts at the first row that has a hash.
ALTER TABLE audit_log ADD COLUMN prev_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN hash TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE audit_log DROP COLUMN hash;
ALTER TABLE audit_log DROP COLUMN prev_hash;
