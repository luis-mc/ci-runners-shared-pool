-- SBOM and vulnerability scan (REG8 owns this file; docs/features/registry R5.59 to R5.62).

-- name: QueueRegistryScan :exec
INSERT INTO registry_scans (image_id) VALUES (?) ON CONFLICT (image_id) DO NOTHING;

-- name: ListQueuedRegistryScans :many
-- Oldest first; REG8 offers the pusher's own images to it first (in Go).
SELECT s.image_id, s.queued_at, i.pushed_by FROM registry_scans s JOIN registry_images i ON i.id = s.image_id
WHERE s.state = 'queued' AND i.state <> 'deleted'
ORDER BY s.queued_at, s.image_id
LIMIT 50;

-- name: ClaimRegistryScan :execrows
UPDATE registry_scans SET state = 'scanning', machine_id = ?, claimed_at = ?, attempts = attempts + 1
WHERE image_id = ? AND state = 'queued';

-- name: FinishRegistryScan :exec
UPDATE registry_scans SET state = ?, packages = ?, critical = ?, high = ?, medium = ?, low = ?, sbom_digest = ?,
    report_digest = ?, error = ?, finished_at = ?
WHERE image_id = ?;

-- name: GetRegistryScan :one
SELECT * FROM registry_scans WHERE image_id = ?;

-- name: SetRegistryImageScanState :exec
UPDATE registry_images SET scan_state = ? WHERE id = ?;
