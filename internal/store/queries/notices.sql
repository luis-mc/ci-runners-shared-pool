-- Notices, event state and deliveries (notifications R5.2, R5.3, R5.11 to R5.14, R5.23, R5.32, R6.4, R6.5).

-- name: GetNotifyEvent :one
SELECT * FROM notify_event WHERE key = ?;

-- name: ListNotifyEventsByType :many
SELECT * FROM notify_event WHERE type = ? ORDER BY key;

-- name: UpsertNotifyEvent :exec
INSERT INTO notify_event (key, type, subject_kind, subject_id, subject_name, state, tone, severity, summary, link,
    first_fired_at, last_change_at, flaps, open_notice_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (key) DO UPDATE SET
    subject_name = excluded.subject_name, state = excluded.state, tone = excluded.tone, severity = excluded.severity,
    summary = excluded.summary, link = excluded.link, first_fired_at = excluded.first_fired_at,
    last_change_at = excluded.last_change_at, flaps = excluded.flaps, open_notice_id = excluded.open_notice_id;

-- name: InsertNotice :exec
INSERT INTO notices (id, event_key, type, subject_kind, subject_id, subject_name, state, tone, severity, summary, link, flaps, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: SetNoticeFlaps :exec
UPDATE notices SET flaps = ? WHERE id = ?;

-- name: ListNotices :many
SELECT * FROM notices WHERE created_at >= ? ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: ListDeliveriesForNotices :many
SELECT * FROM notice_deliveries WHERE notice_id IN (sqlc.slice('ids')) ORDER BY created_at, id;

-- name: InsertDelivery :exec
INSERT INTO notice_deliveries (id, notice_id, channel_id, channel_kind, channel_name, owner_login, target, status,
    attempt, next_try_at, last_error, group_count, burst_group, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, '', ?, ?, ?, ?);

-- name: CountChannelDeliveriesSince :one
SELECT COUNT(*) FROM notice_deliveries
WHERE channel_id = ? AND created_at >= ? AND status NOT IN ('held', 'grouped', 'dropped');

-- name: FindOpenGroup :one
SELECT * FROM notice_deliveries
WHERE channel_id = ? AND burst_group = ? AND status = 'queued' AND group_count > 0 AND created_at >= ?
ORDER BY created_at DESC LIMIT 1;

-- name: CountBurstGroupSince :one
SELECT COUNT(*) FROM notice_deliveries WHERE channel_id = ? AND burst_group = ? AND created_at >= ?;

-- name: BumpGroup :exec
UPDATE notice_deliveries SET group_count = group_count + 1, updated_at = ? WHERE id = ?;

-- name: DueDeliveries :many
SELECT * FROM notice_deliveries WHERE status = 'queued' AND next_try_at <= ? ORDER BY next_try_at, id LIMIT ?;

-- name: GetDelivery :one
SELECT * FROM notice_deliveries WHERE id = ?;

-- name: GetNotice :one
SELECT * FROM notices WHERE id = ?;

-- name: SetDeliveryResult :exec
UPDATE notice_deliveries SET status = ?, attempt = ?, next_try_at = ?, last_error = ?, updated_at = ?, delivered_at = ?
WHERE id = ?;

-- name: CountDeliveredSince :one
SELECT COUNT(*) FROM notice_deliveries WHERE channel_id = ? AND status = 'delivered' AND delivered_at >= ?;

-- name: RecentChannelOutcomes :many
SELECT status FROM notice_deliveries
WHERE channel_id = ? AND status IN ('delivered', 'failed')
ORDER BY updated_at DESC, id DESC LIMIT ?;

-- name: ListHeldChannels :many
SELECT channel_id, channel_kind, channel_name, owner_login, MIN(created_at) AS oldest, COUNT(*) AS n
FROM notice_deliveries WHERE status = 'held' GROUP BY channel_id, channel_kind, channel_name, owner_login;

-- name: MarkHeldGrouped :exec
UPDATE notice_deliveries SET status = 'grouped', updated_at = ? WHERE channel_id = ? AND status = 'held';

-- name: CountQueuedDeliveries :one
SELECT COUNT(*) FROM notice_deliveries WHERE status IN ('queued', 'waiting_tray');

-- name: DropOldestNoteDeliveries :execrows
UPDATE notice_deliveries SET status = 'dropped', last_error = 'dropped: the queue was full', updated_at = ?
WHERE id IN (
    SELECT d.id FROM notice_deliveries d JOIN notices n ON n.id = d.notice_id
    WHERE d.status = 'queued' AND n.tone = 'note' ORDER BY d.created_at LIMIT ?
);

-- name: ListWaitingTray :many
SELECT * FROM notice_deliveries WHERE status = 'waiting_tray' AND owner_login = ? ORDER BY created_at, id LIMIT ?;

-- name: PruneNoticeDeliveries :execrows
DELETE FROM notice_deliveries WHERE created_at < ?;

-- name: PruneNotices :execrows
DELETE FROM notices WHERE created_at < ?;

-- name: PruneResolvedEvents :execrows
DELETE FROM notify_event WHERE state = 'resolved' AND last_change_at < ?;
