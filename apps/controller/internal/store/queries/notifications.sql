-- Notification channels (notifications R5.7 to R5.9; web-console 4.9).

-- name: ListNotificationChannels :many
SELECT * FROM notification_channels ORDER BY created_at, id;

-- name: GetNotificationChannel :one
SELECT * FROM notification_channels WHERE id = ?;

-- name: InsertNotificationChannel :exec
INSERT INTO notification_channels (
    id, kind, name, scope, owner_login, enabled, events, min_severity, address, url, has_signing,
    smtp_host, smtp_port, smtp_username, smtp_from, smtp_has_password
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateNotificationChannel :execrows
UPDATE notification_channels SET
    name = ?, enabled = ?, events = ?, min_severity = ?, address = ?, url = ?, has_signing = ?,
    smtp_host = ?, smtp_port = ?, smtp_username = ?, smtp_from = ?, smtp_has_password = ?,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ?;

-- name: DeleteNotificationChannel :execrows
DELETE FROM notification_channels WHERE id = ?;

-- name: SetNotificationChannelTest :exec
UPDATE notification_channels SET last_test_at = ?, last_test_ok = ? WHERE id = ?;
