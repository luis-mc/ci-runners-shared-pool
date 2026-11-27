-- The Jobs section (internal/jobsdata): runs, jobs, steps, daily summaries, workflow graphs. The filtered
-- lists and groupings are built in jobsdata_read.go; the merge rules (state never backward) are in Go.

-- name: GetJobsRow :one
SELECT * FROM jobs WHERE id = ?;

-- name: UpsertJobsRow :exec
INSERT INTO jobs (id, repository_id, repository, run_id, run_attempt, workflow, name, job_type, matrix, job_ident, branch, head_sha, event, labels, state, result, html_url, created_at, queued_at, slot_at, started_at, completed_at, cleanup_ms, machine_id, machine_name, runner_id, runner_name, runner_kind, backend, isolation, cache, wait_reason, wait_text, worktree, light, fc_lo_ms, fc_mid_ms, fc_hi_ms, fc_basis, fc_samples, updated_at, runner_origin, runner_group, runner_labels, steps_source, checked_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
    repository_id = excluded.repository_id,
    repository = excluded.repository,
    run_id = excluded.run_id,
    run_attempt = excluded.run_attempt,
    workflow = excluded.workflow,
    name = excluded.name,
    job_type = excluded.job_type,
    matrix = excluded.matrix,
    job_ident = excluded.job_ident,
    branch = excluded.branch,
    head_sha = excluded.head_sha,
    event = excluded.event,
    labels = excluded.labels,
    state = excluded.state,
    result = excluded.result,
    html_url = excluded.html_url,
    created_at = excluded.created_at,
    queued_at = excluded.queued_at,
    slot_at = excluded.slot_at,
    started_at = excluded.started_at,
    completed_at = excluded.completed_at,
    cleanup_ms = excluded.cleanup_ms,
    machine_id = excluded.machine_id,
    machine_name = excluded.machine_name,
    runner_id = excluded.runner_id,
    runner_name = excluded.runner_name,
    runner_kind = excluded.runner_kind,
    backend = excluded.backend,
    isolation = excluded.isolation,
    cache = excluded.cache,
    wait_reason = excluded.wait_reason,
    wait_text = excluded.wait_text,
    worktree = excluded.worktree,
    light = excluded.light,
    fc_lo_ms = excluded.fc_lo_ms,
    fc_mid_ms = excluded.fc_mid_ms,
    fc_hi_ms = excluded.fc_hi_ms,
    fc_basis = excluded.fc_basis,
    fc_samples = excluded.fc_samples,
    updated_at = excluded.updated_at,
    -- CIX8 (ci-run-insights R5.39 to R5.41): the reconcile sets these; an ingest upsert that does not know
    -- them (empty) keeps what is stored.
    runner_origin = CASE WHEN excluded.runner_origin != '' THEN excluded.runner_origin ELSE jobs.runner_origin END,
    runner_group = CASE WHEN excluded.runner_group != '' THEN excluded.runner_group ELSE jobs.runner_group END,
    runner_labels = CASE WHEN excluded.runner_labels NOT IN ('', '[]') THEN excluded.runner_labels ELSE jobs.runner_labels END,
    steps_source = CASE WHEN excluded.steps_source != '' THEN excluded.steps_source ELSE jobs.steps_source END,
    checked_at = CASE WHEN excluded.checked_at != '' THEN excluded.checked_at ELSE jobs.checked_at END;

-- name: GetRunsRow :one
SELECT * FROM runs WHERE repository_id = ? AND run_id = ? AND run_attempt = ?;

-- name: UpsertRunsRow :exec
INSERT INTO runs (repository_id, run_id, run_attempt, repository, workflow, workflow_name, event, head_branch, head_sha, pull_request, task_id, worktree, html_url, created_at, started_at, completed_at, conclusion, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (repository_id, run_id, run_attempt) DO UPDATE SET
    repository = excluded.repository,
    workflow = excluded.workflow,
    workflow_name = excluded.workflow_name,
    event = excluded.event,
    head_branch = excluded.head_branch,
    head_sha = excluded.head_sha,
    pull_request = excluded.pull_request,
    task_id = excluded.task_id,
    worktree = excluded.worktree,
    html_url = excluded.html_url,
    created_at = excluded.created_at,
    started_at = excluded.started_at,
    completed_at = excluded.completed_at,
    conclusion = excluded.conclusion,
    updated_at = excluded.updated_at;

-- name: DeleteJobSteps :exec
DELETE FROM job_steps WHERE job_id = ?;

-- name: InsertJobStep :exec
INSERT INTO job_steps (job_id, number, name, state, result, started_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListJobSteps :many
SELECT * FROM job_steps WHERE job_id = ? ORDER BY number;

-- name: GetPendingFacts :one
SELECT json FROM job_facts_pending WHERE job_id = ?;

-- name: PutPendingFacts :exec
INSERT INTO job_facts_pending (job_id, json, updated_at) VALUES (?, ?, ?)
ON CONFLICT (job_id) DO UPDATE SET json = excluded.json, updated_at = excluded.updated_at;

-- name: DeletePendingFacts :exec
DELETE FROM job_facts_pending WHERE job_id = ?;

-- name: SetJobForecast :exec
UPDATE jobs SET fc_lo_ms = ?, fc_mid_ms = ?, fc_hi_ms = ?, fc_basis = ?, fc_samples = ?, updated_at = ? WHERE id = ?;

-- name: GetWorkflowGraph :one
SELECT * FROM workflow_graphs WHERE repository_id = ? AND workflow = ? AND head_sha = ?;

-- name: PutWorkflowGraph :exec
INSERT INTO workflow_graphs (repository_id, workflow, head_sha, ok, needs, dynamic, fetched_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (repository_id, workflow, head_sha) DO UPDATE SET
    ok = excluded.ok, needs = excluded.needs, dynamic = excluded.dynamic, fetched_at = excluded.fetched_at;

-- name: ListJobAttempts :many
SELECT id, run_attempt, state, result FROM jobs
WHERE repository_id = ? AND run_id = ? AND name = ? ORDER BY run_attempt;

-- name: ListRunAttempts :many
SELECT run_attempt FROM runs WHERE run_id = ? ORDER BY run_attempt;

-- name: ListDailySummaries :many
SELECT * FROM job_daily WHERE day >= ? AND day <= ? ORDER BY day, repository, job_type;

-- name: UpsertDailySummary :exec
INSERT INTO job_daily (day, repository, job_type, jobs, failed, wait_p50_ms, wait_p90_ms, run_p50_ms, run_p90_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (day, repository, job_type) DO UPDATE SET
    jobs = excluded.jobs, failed = excluded.failed, wait_p50_ms = excluded.wait_p50_ms,
    wait_p90_ms = excluded.wait_p90_ms, run_p50_ms = excluded.run_p50_ms, run_p90_ms = excluded.run_p90_ms;

-- name: DailySummarisedDays :many
SELECT DISTINCT day FROM job_daily;

-- name: PruneJobSteps :execrows
DELETE FROM job_steps WHERE completed_at <> '' AND completed_at < ?;

-- name: PruneJobFacts :execrows
DELETE FROM job_facts_pending WHERE job_facts_pending.job_id IN (SELECT j.id FROM jobs j WHERE j.state = 'done' AND j.completed_at <> '' AND j.completed_at < ?)
   OR job_facts_pending.updated_at < ?;

-- name: PruneJobStepsOfJobs :execrows
DELETE FROM job_steps WHERE job_steps.job_id IN (SELECT j.id FROM jobs j WHERE j.state = 'done' AND j.completed_at <> '' AND j.completed_at < ?);

-- name: PruneJobs :execrows
DELETE FROM jobs WHERE state = 'done' AND completed_at <> '' AND completed_at < ?;

-- name: PruneRuns :execrows
DELETE FROM runs WHERE runs.created_at < ?
  AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.repository_id = runs.repository_id AND j.run_id = runs.run_id AND j.run_attempt = runs.run_attempt);

-- name: PruneDailySummaries :execrows
DELETE FROM job_daily WHERE day < ?;

-- name: PruneWorkflowGraphs :execrows
DELETE FROM workflow_graphs WHERE fetched_at < ?;

-- name: GetRunFollowUp :one
SELECT * FROM run_follow_ups WHERE repository_id = ? AND run_id = ?;

-- name: SetRunFollowUp :exec
INSERT INTO run_follow_ups (repository_id, run_id, parent_run_id, source, updated_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (repository_id, run_id) DO UPDATE SET parent_run_id = excluded.parent_run_id,
    source = excluded.source, updated_at = excluded.updated_at;

-- name: PruneRunFollowUps :execrows
DELETE FROM run_follow_ups WHERE NOT EXISTS (SELECT 1 FROM runs r WHERE r.repository_id = run_follow_ups.repository_id AND r.run_id = run_follow_ups.run_id);
