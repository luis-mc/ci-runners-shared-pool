-- Doctor runs, findings and state (doctor 7.5).

-- name: InsertDoctorRun :exec
INSERT INTO doctor_runs (id, trigger, scope, started_by) VALUES (?, ?, ?, ?);

-- name: FinishDoctorRun :exec
UPDATE doctor_runs SET state = ?, finished_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), worst = ?, counts = ? WHERE id = ?;

-- name: GetDoctorRun :one
SELECT * FROM doctor_runs WHERE id = ?;

-- name: LastDoctorRun :one
SELECT * FROM doctor_runs WHERE state = 'done' ORDER BY started_at DESC LIMIT 1;

-- name: InsertDoctorFinding :one
INSERT INTO doctor_findings (run_id, check_id, subject_kind, subject_id, subject_name, severity, title,
    detail, fix, needs_admin, milestone)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: ListRunFindings :many
SELECT * FROM doctor_findings WHERE run_id = ? ORDER BY severity, check_id, subject_name;

-- name: ListLatestFindings :many
SELECT f.* FROM doctor_state s JOIN doctor_findings f ON f.id = s.finding_id
WHERE (sqlc.narg(severity) IS NULL OR f.severity = sqlc.narg(severity))
ORDER BY f.severity, f.check_id, f.subject_name;

-- name: UpsertDoctorState :exec
INSERT INTO doctor_state (check_id, subject_kind, subject_id, severity, finding_id) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (check_id, subject_kind, subject_id) DO UPDATE SET finding_id = excluded.finding_id,
    since = CASE WHEN doctor_state.severity = excluded.severity THEN doctor_state.since ELSE strftime('%Y-%m-%dT%H:%M:%fZ', 'now') END,
    severity = excluded.severity;
