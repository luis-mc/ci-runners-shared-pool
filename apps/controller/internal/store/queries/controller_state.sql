-- Controller state kept as JSON documents (OPS59): machines, settings, keys,
-- join codes, changes, runner records and plans.

-- name: UpsertCtlMachine :exec
INSERT INTO ctl_machines (id, name, removed, doc) VALUES (?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET name = excluded.name, removed = excluded.removed, doc = excluded.doc;

-- name: GetCtlMachine :one
SELECT doc FROM ctl_machines WHERE id = ?;

-- name: ListCtlMachines :many
SELECT id, name, removed, doc FROM ctl_machines;

-- name: CountCtlMachines :one
SELECT count(*) FROM ctl_machines;

-- name: UpsertCtlMachineSettings :exec
INSERT INTO ctl_machine_settings (machine_id, doc) VALUES (?, ?)
ON CONFLICT (machine_id) DO UPDATE SET doc = excluded.doc;

-- name: GetCtlMachineSettings :one
SELECT doc FROM ctl_machine_settings WHERE machine_id = ?;

-- name: UpsertCtlMachineKey :exec
INSERT INTO ctl_machine_keys (hash, machine_id, revoked) VALUES (?, ?, ?)
ON CONFLICT (hash) DO UPDATE SET machine_id = excluded.machine_id, revoked = excluded.revoked;

-- name: GetCtlMachineByKey :one
SELECT m.doc FROM ctl_machine_keys k JOIN ctl_machines m ON m.id = k.machine_id
WHERE k.hash = ? AND k.revoked = 0 AND m.removed = 0;

-- name: RevokeCtlMachineKeys :exec
UPDATE ctl_machine_keys SET revoked = 1 WHERE machine_id = ?;

-- name: ListCtlKeys :many
SELECT hash, machine_id, revoked FROM ctl_machine_keys;

-- name: UpsertCtlJoinCode :exec
INSERT INTO ctl_join_codes (id, code_hash, doc) VALUES (?, ?, ?)
ON CONFLICT (id) DO UPDATE SET doc = excluded.doc;

-- name: GetCtlJoinCode :one
SELECT doc FROM ctl_join_codes WHERE id = ?;

-- name: FindCtlJoinCodeByHash :one
SELECT doc FROM ctl_join_codes WHERE code_hash = ?;

-- name: ListCtlJoinCodes :many
SELECT doc FROM ctl_join_codes;

-- name: UpsertCtlMachineChange :exec
INSERT INTO ctl_machine_changes (id, machine_id, open, doc) VALUES (?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET open = excluded.open, doc = excluded.doc;

-- name: ListCtlMachineChanges :many
SELECT doc FROM ctl_machine_changes;

-- name: UpsertCtlRunner :exec
INSERT INTO ctl_runners (id, machine_id, name, doc) VALUES (?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET machine_id = excluded.machine_id, name = excluded.name, doc = excluded.doc;

-- name: GetCtlRunner :one
SELECT doc FROM ctl_runners WHERE id = ?;

-- name: ListCtlRunners :many
SELECT doc FROM ctl_runners ORDER BY name;

-- name: DeleteCtlRunner :exec
DELETE FROM ctl_runners WHERE id = ?;

-- name: UpsertCtlRunnerPlan :exec
INSERT INTO ctl_runner_plans (id, doc) VALUES (?, ?)
ON CONFLICT (id) DO UPDATE SET doc = excluded.doc;

-- name: GetCtlRunnerPlan :one
SELECT doc FROM ctl_runner_plans WHERE id = ?;
