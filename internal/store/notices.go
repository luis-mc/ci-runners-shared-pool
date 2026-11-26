package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/store/sqlcgen"
)

// This file is the store side of notices (notifications R5.2, R5.3, R5.11 to
// R5.14, R5.23, R5.32, R6.4, R6.5). Times are strings in TimeFormat (see
// FormatTime and ParseTime). The rows hold no secret and no webhook address.

// Rows of the three notice tables.
type (
	NotifyEvent    = sqlcgen.NotifyEvent
	Notice         = sqlcgen.Notice
	NoticeDelivery = sqlcgen.NoticeDelivery
	// HeldChannel is a channel with notices held by its hourly cap (R5.12).
	HeldChannel struct {
		ChannelID, ChannelKind, ChannelName, OwnerLogin string
		Oldest                                          time.Time
		Count                                           int
	}
)

// Delivery statuses.
const (
	DeliveryQueued      = "queued"
	DeliveryWaitingTray = "waiting_tray"
	DeliveryDelivered   = "delivered"
	DeliveryFailed      = "failed"
	DeliveryHeld        = "held"
	DeliveryGrouped     = "grouped"
	DeliveryDropped     = "dropped"
)

// FormatTime writes a time the way the database keeps it.
func FormatTime(t time.Time) string { return t.UTC().Format(TimeFormat) }

// GetNotifyEvent returns the state of one key inside the transaction; ok is false when the key is new.
func (t *Tx) GetNotifyEvent(key string) (NotifyEvent, bool, error) {
	e, err := t.q.GetNotifyEvent(t.ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return NotifyEvent{}, false, nil
	}
	if err != nil {
		return NotifyEvent{}, false, fmt.Errorf("get notify event: %w", err)
	}
	return e, true, nil
}

// PutNotifyEvent writes the state of one key.
func (t *Tx) PutNotifyEvent(e NotifyEvent) error {
	err := t.q.UpsertNotifyEvent(t.ctx, sqlcgen.UpsertNotifyEventParams(e))
	if err != nil {
		return fmt.Errorf("put notify event: %w", err)
	}
	return nil
}

// InsertNotice adds a notice.
func (t *Tx) InsertNotice(n Notice) error {
	err := t.q.InsertNotice(t.ctx, sqlcgen.InsertNoticeParams(n))
	if err != nil {
		return fmt.Errorf("insert notice: %w", err)
	}
	return nil
}

// SetNoticeFlaps sets the flap count shown on a notice (R5.11).
func (t *Tx) SetNoticeFlaps(id string, flaps int64) error {
	if err := t.q.SetNoticeFlaps(t.ctx, sqlcgen.SetNoticeFlapsParams{Flaps: flaps, ID: id}); err != nil {
		return fmt.Errorf("set notice flaps: %w", err)
	}
	return nil
}

// InsertDelivery adds a delivery with the given status, due at next.
func (t *Tx) InsertDelivery(d NoticeDelivery) error {
	err := t.q.InsertDelivery(t.ctx, sqlcgen.InsertDeliveryParams{
		ID: d.ID, NoticeID: d.NoticeID, ChannelID: d.ChannelID, ChannelKind: d.ChannelKind, ChannelName: d.ChannelName,
		OwnerLogin: d.OwnerLogin, Target: d.Target, Status: d.Status, NextTryAt: d.NextTryAt, GroupCount: d.GroupCount,
		BurstGroup: d.BurstGroup, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	})
	if err != nil {
		return fmt.Errorf("insert delivery: %w", err)
	}
	return nil
}

// CountChannelDeliveriesSince counts the deliveries made for a channel since a time, held ones not counted (R5.12).
func (t *Tx) CountChannelDeliveriesSince(channelID string, since time.Time) (int, error) {
	n, err := t.q.CountChannelDeliveriesSince(t.ctx, sqlcgen.CountChannelDeliveriesSinceParams{ChannelID: channelID, CreatedAt: FormatTime(since)})
	return int(n), err
}

// CountBurstGroupSince counts the deliveries of one group key (channel, type, state) since a time (R5.13).
func (t *Tx) CountBurstGroupSince(channelID, burstGroup string, since time.Time) (int, error) {
	n, err := t.q.CountBurstGroupSince(t.ctx, sqlcgen.CountBurstGroupSinceParams{ChannelID: channelID, BurstGroup: burstGroup, CreatedAt: FormatTime(since)})
	return int(n), err
}

// FindOpenGroup returns the queued group delivery of a key made since a time; ok is false when there is none.
func (t *Tx) FindOpenGroup(channelID, burstGroup string, since time.Time) (NoticeDelivery, bool, error) {
	d, err := t.q.FindOpenGroup(t.ctx, sqlcgen.FindOpenGroupParams{ChannelID: channelID, BurstGroup: burstGroup, CreatedAt: FormatTime(since)})
	if errors.Is(err, sql.ErrNoRows) {
		return NoticeDelivery{}, false, nil
	}
	return d, err == nil, err
}

// BumpGroup counts one more notice into a group delivery.
func (t *Tx) BumpGroup(id string, now time.Time) error {
	return t.q.BumpGroup(t.ctx, sqlcgen.BumpGroupParams{UpdatedAt: FormatTime(now), ID: id})
}

// ListNotifyEventsByType returns the state of every key of one event type.
func (s *Store) ListNotifyEventsByType(ctx context.Context, typ string) ([]NotifyEvent, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.q.ListNotifyEventsByType(ctx, typ)
	if err != nil {
		return nil, fmt.Errorf("list notify events: %w", err)
	}
	return rows, nil
}

// ListNotices returns the notices made since a time, newest first.
func (s *Store) ListNotices(ctx context.Context, since time.Time, limit int) ([]Notice, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.q.ListNotices(ctx, sqlcgen.ListNoticesParams{CreatedAt: FormatTime(since), Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("list notices: %w", err)
	}
	return rows, nil
}

// ListDeliveriesForNotices returns the deliveries of the given notices.
func (s *Store) ListDeliveriesForNotices(ctx context.Context, ids []string) ([]NoticeDelivery, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.q.ListDeliveriesForNotices(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list deliveries: %w", err)
	}
	return rows, nil
}

// DueDeliveries returns queued deliveries due at or before now, oldest first.
func (s *Store) DueDeliveries(ctx context.Context, now time.Time, limit int) ([]NoticeDelivery, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.q.DueDeliveries(ctx, sqlcgen.DueDeliveriesParams{NextTryAt: FormatTime(now), Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("due deliveries: %w", err)
	}
	return rows, nil
}

// GetDelivery returns one delivery, or ErrNotFound.
func (s *Store) GetDelivery(ctx context.Context, id string) (NoticeDelivery, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	d, err := s.q.GetDelivery(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

// GetNotice returns one notice, or ErrNotFound.
func (s *Store) GetNotice(ctx context.Context, id string) (Notice, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	n, err := s.q.GetNotice(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return n, ErrNotFound
	}
	return n, err
}

// SetDeliveryResult records the outcome of one try.
func (s *Store) SetDeliveryResult(ctx context.Context, id, status string, attempt int64, next time.Time, lastErr string, now time.Time, delivered bool) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	var at sql.NullString
	if delivered {
		at = sql.NullString{String: FormatTime(now), Valid: true}
	}
	err := s.q.SetDeliveryResult(ctx, sqlcgen.SetDeliveryResultParams{
		Status: status, Attempt: attempt, NextTryAt: FormatTime(next), LastError: lastErr,
		UpdatedAt: FormatTime(now), DeliveredAt: at, ID: id,
	})
	if err != nil {
		return fmt.Errorf("set delivery result: %w", err)
	}
	return nil
}

// CountDeliveredSince counts what a channel delivered since a time (R5.14).
func (s *Store) CountDeliveredSince(ctx context.Context, channelID string, since time.Time) (int, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	n, err := s.q.CountDeliveredSince(ctx, sqlcgen.CountDeliveredSinceParams{ChannelID: channelID, DeliveredAt: sql.NullString{String: FormatTime(since), Valid: true}})
	return int(n), err
}

// RecentChannelOutcomes returns the last statuses (delivered or failed) of a channel, newest first (R5.24).
func (s *Store) RecentChannelOutcomes(ctx context.Context, channelID string, n int) ([]string, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return s.q.RecentChannelOutcomes(ctx, sqlcgen.RecentChannelOutcomesParams{ChannelID: channelID, Limit: int64(n)})
}

// ListHeldChannels returns the channels that have notices held by the hourly cap.
func (s *Store) ListHeldChannels(ctx context.Context) ([]HeldChannel, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.q.ListHeldChannels(ctx)
	if err != nil {
		return nil, fmt.Errorf("list held: %w", err)
	}
	out := make([]HeldChannel, 0, len(rows))
	for _, r := range rows {
		oldest, _ := r.Oldest.(string)
		out = append(out, HeldChannel{ChannelID: r.ChannelID, ChannelKind: r.ChannelKind, ChannelName: r.ChannelName,
			OwnerLogin: r.OwnerLogin, Oldest: ParseTime(oldest), Count: int(r.N)})
	}
	return out, nil
}

// MarkHeldGrouped marks a channel's held deliveries as folded into a summary (R5.12).
func (s *Store) MarkHeldGrouped(ctx context.Context, channelID string, now time.Time) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return s.q.MarkHeldGrouped(ctx, sqlcgen.MarkHeldGroupedParams{UpdatedAt: FormatTime(now), ChannelID: channelID})
}

// CountQueuedDeliveries counts deliveries that wait to be sent (R6.9).
func (s *Store) CountQueuedDeliveries(ctx context.Context) (int, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	n, err := s.q.CountQueuedDeliveries(ctx)
	return int(n), err
}

// DropOldestNoteDeliveries drops up to n of the oldest queued Note-tone deliveries and returns how many (R6.9).
func (s *Store) DropOldestNoteDeliveries(ctx context.Context, n int, now time.Time) (int, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	c, err := s.q.DropOldestNoteDeliveries(ctx, sqlcgen.DropOldestNoteDeliveriesParams{UpdatedAt: FormatTime(now), Limit: int64(n)})
	return int(c), err
}

// ListWaitingTray returns the desktop deliveries a tray has not taken yet (R5.15).
func (s *Store) ListWaitingTray(ctx context.Context, login string, limit int) ([]NoticeDelivery, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return s.q.ListWaitingTray(ctx, sqlcgen.ListWaitingTrayParams{OwnerLogin: login, Limit: int64(limit)})
}

// PruneNotices deletes notices, deliveries and resolved event states older than before (R6.5).
func (s *Store) PruneNotices(ctx context.Context, before time.Time) (int64, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	b := FormatTime(before)
	d, err := s.q.PruneNoticeDeliveries(ctx, b)
	if err != nil {
		return 0, err
	}
	n, err := s.q.PruneNotices(ctx, b)
	if err != nil {
		return 0, err
	}
	if _, err := s.q.PruneResolvedEvents(ctx, b); err != nil {
		return 0, err
	}
	return d + n, nil
}

// QueuedJob is a job that waits for a runner, for the job.waiting source (R5.1).
type QueuedJob struct {
	ID         int64
	Repository string
	Workflow   string
	Name       string
	QueuedAt   time.Time
}

// QueuedJobs returns the jobs GitHub has queued and crsp has not started.
func (s *Store) QueuedJobs(ctx context.Context) ([]QueuedJob, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `
SELECT j.id, r.full_name, j.workflow, j.job_name, COALESCE(j.created_at_github, j.updated_at)
FROM gh_jobs j JOIN repositories r ON r.id = j.repository_id
WHERE j.status = 'queued' ORDER BY j.id`)
	if err != nil {
		return nil, fmt.Errorf("list queued jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []QueuedJob
	for rows.Next() {
		var q QueuedJob
		var at string
		if err := rows.Scan(&q.ID, &q.Repository, &q.Workflow, &q.Name, &at); err != nil {
			return nil, fmt.Errorf("list queued jobs: %w", err)
		}
		q.QueuedAt = ParseTime(at)
		out = append(out, q)
	}
	return out, rows.Err()
}
