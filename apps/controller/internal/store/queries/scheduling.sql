-- GitHub jobs, slot tickets, slots and the slot log (scheduling R5.6 to R5.39).
-- A take is one transaction in the scheduler: first in line, count free slots,
-- insert the slot (the key refuses a double take), delete the ticket, log it.

-- name: UpsertGitHubJob :exec
INSERT INTO gh_jobs (id, repository_id, run_id, run_attempt, workflow, job_name, head_sha, head_branch,
    event, from_fork, labels, status, conclusion, runner_name, created_at_github, started_at_github,
    completed_at_github)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET status = excluded.status, conclusion = excluded.conclusion,
    runner_name = excluded.runner_name, started_at_github = excluded.started_at_github,
    completed_at_github = excluded.completed_at_github, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now');

-- name: GetGitHubJob :one
SELECT * FROM gh_jobs WHERE id = ?;

-- name: ListOpenGitHubJobs :many
SELECT * FROM gh_jobs WHERE status <> 'completed' ORDER BY created_at_github;

-- name: SetGitHubJobWait :exec
UPDATE gh_jobs SET wait_reason = ?, wait_text = ?, placed_machine_id = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?;

-- name: InsertTicket :exec
INSERT INTO slot_tickets (id, machine_id, repository, repository_id, run_id, run_attempt, job,
    github_job_id, shard, runner_name, kind, repo_priority, event_class, queued_at_ns, random_suffix,
    expires_at, holder_machine, holder_pid, gate_pid)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetTicket :one
SELECT * FROM slot_tickets WHERE id = ?;

-- name: ListMachineTickets :many
SELECT * FROM slot_tickets WHERE machine_id = ?
ORDER BY repo_priority DESC, event_class, queued_at_ns, random_suffix;

-- name: BeatTicket :execrows
UPDATE slot_tickets SET beat_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), expires_at = ? WHERE id = ?;

-- name: SetTicketWaitReason :exec
UPDATE slot_tickets SET wait_reason = ? WHERE id = ?;

-- name: DeleteTicket :execrows
DELETE FROM slot_tickets WHERE id = ?;

-- name: ListExpiredTickets :many
SELECT * FROM slot_tickets WHERE expires_at <= ?;

-- name: DeleteJobTickets :many
DELETE FROM slot_tickets WHERE repository = ? AND run_id = ? AND run_attempt = ? AND job = ?
RETURNING id, machine_id, kind;

-- name: InsertSlot :exec
INSERT INTO slots (machine_id, kind, number, claim, repository, repository_id, run_id, run_attempt, job,
    github_job_id, shard, runner_name, holder_machine, holder_pid, gate_pid, queued_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListMachineSlots :many
SELECT * FROM slots WHERE machine_id = ? ORDER BY kind, number;

-- name: CountMachineSlots :one
SELECT count(*) FROM slots WHERE machine_id = ? AND kind = ?;

-- name: CountRepositoryRunning :one
SELECT count(*) FROM slots WHERE repository = ? AND kind IN ('ci', 'heavy');

-- name: FreeSlotByClaim :execrows
DELETE FROM slots WHERE machine_id = ? AND kind = ? AND number = ? AND claim = ?;

-- name: FreeJobSlots :many
DELETE FROM slots WHERE repository = ? AND run_id = ? AND run_attempt = ? AND job = ?
RETURNING machine_id, kind, number, queued_at, taken_at;

-- name: ListExpiredSlots :many
SELECT * FROM slots WHERE expires_at <= ?;

-- name: FreeSlot :execrows
DELETE FROM slots WHERE machine_id = ? AND kind = ? AND number = ?;

-- name: InsertSlotLog :exec
INSERT INTO slot_log (machine_id, event, kind, number, repository, run_id, run_attempt, job, shard,
    runner_name, queued_at, slot_at, released_at, reason)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: SlotLogCutoff :one
-- The id of the oldest row to keep when keeping the newest N rows (R5.39).
SELECT id FROM slot_log WHERE machine_id = ? ORDER BY id DESC LIMIT 1 OFFSET ?;

-- name: TrimSlotLog :exec
DELETE FROM slot_log WHERE machine_id = ? AND id < ?;
