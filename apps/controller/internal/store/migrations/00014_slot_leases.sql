-- +goose Up
-- HND11: a slot is held by a lease, not by a process id (scheduling R5.70). The
-- holder id names the gate that took the slot; lease_expires_at moves forward
-- with each heartbeat. Rows that exist now get the claim as holder id and their
-- age limit as lease end, so no slot is freed early by this migration. The pid
-- columns stay: they are only shown, never judged.
ALTER TABLE slots ADD COLUMN holder_id TEXT NOT NULL DEFAULT '';
ALTER TABLE slots ADD COLUMN lease_expires_at TEXT NOT NULL DEFAULT '';
UPDATE slots SET holder_id = claim, lease_expires_at = expires_at;

-- +goose Down
ALTER TABLE slots DROP COLUMN lease_expires_at;
ALTER TABLE slots DROP COLUMN holder_id;
