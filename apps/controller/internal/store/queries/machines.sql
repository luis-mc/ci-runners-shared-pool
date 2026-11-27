-- Machines, keys, join codes, settings, load and drain changes
-- (machines R5.1 to R5.39, R6.2, R6.8).

-- name: InsertMachine :exec
INSERT INTO machines (id, name, os, arch, facts, backends, is_controller, agent_version)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetMachine :one
SELECT * FROM machines WHERE id = ?;

-- name: ListMachines :many
SELECT * FROM machines WHERE removed_at IS NULL ORDER BY is_controller DESC, name;

-- name: UpdateMachineFacts :exec
UPDATE machines SET facts = ?, backends = ?, agent_version = ? WHERE id = ?;

-- name: SetMachineState :exec
UPDATE machines SET state = ? WHERE id = ?;

-- name: RenameMachine :exec
UPDATE machines SET name = ? WHERE id = ?;

-- name: MarkMachineSeen :exec
UPDATE machines SET online = 1, last_seen_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?;

-- name: MarkMachinesOffline :many
UPDATE machines SET online = 0 WHERE online = 1 AND last_seen_at < ? RETURNING id;

-- name: MarkMachineRemoved :exec
UPDATE machines SET state = 'removing', removed_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), online = 0 WHERE id = ?;

-- name: InsertMachineKey :exec
INSERT INTO machine_keys (hash, machine_id) VALUES (?, ?);

-- name: GetMachineByKey :one
SELECT m.* FROM machine_keys k JOIN machines m ON m.id = k.machine_id
WHERE k.hash = ? AND k.revoked_at IS NULL AND m.removed_at IS NULL;

-- name: RevokeMachineKeys :exec
UPDATE machine_keys SET revoked_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE machine_id = ? AND revoked_at IS NULL;

-- name: InsertJoinCode :exec
INSERT INTO join_codes (id, code_hash, kind, created_by, expires_at) VALUES (?, ?, ?, ?, ?);

-- name: RevokeOpenJoinCodes :exec
UPDATE join_codes SET revoked_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE kind = 'code' AND used_at IS NULL AND revoked_at IS NULL;

-- name: GetJoinCode :one
SELECT * FROM join_codes WHERE id = ?;

-- name: UseJoinCode :one
UPDATE join_codes SET used_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), machine_id = sqlc.arg(machine_id)
WHERE code_hash = sqlc.arg(code_hash) AND used_at IS NULL AND revoked_at IS NULL AND expires_at > strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
RETURNING id;

-- name: FindJoinCodeByHash :one
SELECT * FROM join_codes WHERE code_hash = ?;

-- name: RevokeJoinCode :execrows
UPDATE join_codes SET revoked_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ? AND used_at IS NULL AND revoked_at IS NULL;

-- name: InsertMachineSettings :exec
INSERT INTO machine_settings (machine_id, budget_memory_pct, budget_cpu_pct, job_slots, drain_timeout_minutes)
VALUES (?, ?, ?, ?, ?);

-- name: GetMachineSettings :one
SELECT * FROM machine_settings WHERE machine_id = ?;

-- name: UpdateMachineSettings :exec
UPDATE machine_settings SET
    version = version + 1,
    budget_memory_pct = ?, budget_cpu_pct = ?, job_slots = ?, light_slots = ?, heavy_slots = ?,
    light_jobs = ?, heavy_jobs = ?, gate_on_timeout = ?, allowed_backends = ?, cache_cap_bytes = ?,
    drain_timeout_minutes = ?, windows_symlink_right = ?, pool_memory_gib = ?, pool_cpus = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), updated_by = ?
WHERE machine_id = ?;

-- name: InsertMachineLoad :exec
INSERT INTO machine_load (machine_id, sampled_at, memory_pct, cpu_pct, slots_used) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (machine_id, sampled_at) DO NOTHING;

-- name: ListMachineLoad :many
SELECT * FROM machine_load WHERE machine_id = ? AND sampled_at >= ? AND sampled_at <= ? ORDER BY sampled_at;

-- name: DeleteMachineLoadBefore :exec
DELETE FROM machine_load WHERE sampled_at < ?;

-- name: InsertMachineChange :exec
INSERT INTO machine_changes (id, machine_id, kind, detail, started_by, deadline_at) VALUES (?, ?, ?, ?, ?, ?);

-- name: GetMachineChange :one
SELECT * FROM machine_changes WHERE id = ? AND machine_id = ?;

-- name: GetOpenMachineChange :one
SELECT * FROM machine_changes WHERE machine_id = ? AND state IN ('waiting', 'running', 'needs_you')
ORDER BY started_at DESC LIMIT 1;

-- name: SetMachineChangeState :exec
-- finished_at is set by the caller when the change is done or cancelled.
UPDATE machine_changes SET state = ?, deadline_at = ?, finished_at = ? WHERE id = ?;
