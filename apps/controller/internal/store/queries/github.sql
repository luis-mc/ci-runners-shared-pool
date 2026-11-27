-- Installations, repositories, policies, variables, runs-on findings,
-- webhook deliveries and plans (github-integration R5.17 to R5.30, R5.36).

-- name: UpsertInstallation :exec
INSERT INTO installations (id, account_login, account_type, repository_selection, permissions, suspended_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET account_login = excluded.account_login,
    account_type = excluded.account_type, repository_selection = excluded.repository_selection,
    permissions = excluded.permissions, suspended_at = excluded.suspended_at,
    removed_at = NULL, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now');

-- name: MarkInstallationRemoved :exec
UPDATE installations SET removed_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?;

-- name: ListInstallations :many
SELECT * FROM installations WHERE removed_at IS NULL ORDER BY account_login;

-- name: UpsertRepository :exec
INSERT INTO repositories (id, installation_id, full_name, owner, name, visibility, default_branch, archived)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET installation_id = excluded.installation_id,
    full_name = excluded.full_name, owner = excluded.owner, name = excluded.name,
    visibility = excluded.visibility, default_branch = excluded.default_branch,
    archived = excluded.archived, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now');

-- name: GetRepository :one
SELECT * FROM repositories WHERE id = ?;

-- name: GetRepositoryByName :one
SELECT * FROM repositories WHERE full_name = ?;

-- name: ListRepositories :many
SELECT * FROM repositories
WHERE (sqlc.narg(installation_id) IS NULL OR installation_id = sqlc.narg(installation_id))
  AND (sqlc.narg(pool_on) IS NULL OR pool_on = sqlc.narg(pool_on))
  AND full_name > sqlc.arg(after_name)
ORDER BY full_name
LIMIT sqlc.arg(page_size);

-- name: ListPoolRepositories :many
SELECT * FROM repositories WHERE pool_on = 1 ORDER BY full_name;

-- name: SetRepositoryPool :exec
UPDATE repositories SET pool_on = ?, pool_changed_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), pool_changed_by = ? WHERE id = ?;

-- name: GetRepoPolicy :one
SELECT * FROM repo_policies WHERE repository_id = ?;

-- name: UpsertRepoPolicy :exec
INSERT INTO repo_policies (repository_id, priority, max_parallel_jobs, allow_fork_prs, labels,
    runner_groups, budget_memory_pct, budget_cpu_pct, updated_by)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (repository_id) DO UPDATE SET priority = excluded.priority,
    max_parallel_jobs = excluded.max_parallel_jobs, allow_fork_prs = excluded.allow_fork_prs,
    labels = excluded.labels, runner_groups = excluded.runner_groups,
    budget_memory_pct = excluded.budget_memory_pct, budget_cpu_pct = excluded.budget_cpu_pct,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), updated_by = excluded.updated_by;

-- name: ListManagedVariables :many
SELECT * FROM managed_variables WHERE repository_id = ? ORDER BY name;

-- name: UpsertManagedVariable :exec
INSERT INTO managed_variables (repository_id, name, value, status) VALUES (?, ?, ?, ?)
ON CONFLICT (repository_id, name) DO UPDATE SET value = excluded.value,
    status = excluded.status, written_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now');

-- name: SetManagedVariableStatus :exec
UPDATE managed_variables SET status = ?, checked_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE repository_id = ? AND name = ?;

-- name: DeleteManagedVariable :exec
DELETE FROM managed_variables WHERE repository_id = ? AND name = ?;

-- name: DeleteRunsOnFindings :exec
DELETE FROM runs_on_findings WHERE repository_id = ?;

-- name: InsertRunsOnFinding :exec
INSERT INTO runs_on_findings (repository_id, path, line, labels, kind, text) VALUES (?, ?, ?, ?, ?, ?);

-- name: ListRunsOnFindings :many
SELECT * FROM runs_on_findings WHERE repository_id = ? ORDER BY path, line;

-- name: InsertWebhookDelivery :execrows
INSERT INTO webhook_deliveries (delivery_id, event) VALUES (?, ?) ON CONFLICT (delivery_id) DO NOTHING;

-- name: DeleteWebhookDeliveriesBefore :exec
DELETE FROM webhook_deliveries WHERE received_at < ?;

-- name: InsertPlan :exec
INSERT INTO plans (id, area, state_hash, steps, created_by) VALUES (?, ?, ?, ?, ?);

-- name: GetPlan :one
SELECT * FROM plans WHERE id = ? AND area = ?;

-- name: UpdatePlan :exec
UPDATE plans SET state = ?, steps = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?;
