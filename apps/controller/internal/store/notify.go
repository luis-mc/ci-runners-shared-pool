package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// This file holds the store side of the notification channels (notifications
// R5.7 to R5.9; web-console 4.9). A channel holds no secret: HasSigning and
// SMTPHasPassword only say whether the OS secret store has one (R6.2).

// NotificationChannel is one stored channel.
type NotificationChannel struct {
	ID          string
	Kind        string // email, webhook, desktop
	Name        string
	Scope       string // personal, pool
	OwnerLogin  string
	Enabled     bool
	Events      []string
	MinSeverity string
	Address     string
	URL         string
	HasSigning  bool

	SMTPHost        string
	SMTPPort        int
	SMTPUsername    string
	SMTPFrom        string
	SMTPHasPassword bool

	LastTestAt *time.Time
	LastTestOK *bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func channelFromRow(r sqlcgen.NotificationChannel) NotificationChannel {
	c := NotificationChannel{
		ID: r.ID, Kind: r.Kind, Name: r.Name, Scope: r.Scope, OwnerLogin: r.OwnerLogin,
		Enabled: r.Enabled == 1, MinSeverity: r.MinSeverity, Address: r.Address, URL: r.Url,
		HasSigning: r.HasSigning == 1,
		SMTPHost:   r.SmtpHost, SMTPPort: int(r.SmtpPort), SMTPUsername: r.SmtpUsername, SMTPFrom: r.SmtpFrom,
		SMTPHasPassword: r.SmtpHasPassword == 1,
		CreatedAt:       ParseTime(r.CreatedAt), UpdatedAt: ParseTime(r.UpdatedAt),
	}
	if err := json.Unmarshal([]byte(r.Events), &c.Events); err != nil || c.Events == nil {
		c.Events = []string{}
	}
	if r.LastTestAt.Valid {
		t := ParseTime(r.LastTestAt.String)
		c.LastTestAt = &t
	}
	if r.LastTestOk.Valid {
		ok := r.LastTestOk.Int64 == 1
		c.LastTestOK = &ok
	}
	return c
}

func eventsJSON(events []string) string {
	if events == nil {
		events = []string{}
	}
	b, _ := json.Marshal(events)
	return string(b)
}

// ListNotificationChannels returns every channel, oldest first.
func (s *Store) ListNotificationChannels(ctx context.Context) ([]NotificationChannel, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	rows, err := s.q.ListNotificationChannels(ctx)
	if err != nil {
		return nil, fmt.Errorf("list notification channels: %w", err)
	}
	out := make([]NotificationChannel, 0, len(rows))
	for _, r := range rows {
		out = append(out, channelFromRow(r))
	}
	return out, nil
}

// GetNotificationChannel returns one channel, or ErrNotFound.
func (s *Store) GetNotificationChannel(ctx context.Context, id string) (NotificationChannel, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	r, err := s.q.GetNotificationChannel(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return NotificationChannel{}, ErrNotFound
	}
	if err != nil {
		return NotificationChannel{}, fmt.Errorf("get notification channel: %w", err)
	}
	return channelFromRow(r), nil
}

// SetNotificationChannelTest records the result of the last test send.
func (s *Store) SetNotificationChannelTest(ctx context.Context, id string, at time.Time, ok bool) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	err := s.q.SetNotificationChannelTest(ctx, sqlcgen.SetNotificationChannelTestParams{
		LastTestAt: sql.NullString{String: at.UTC().Format(TimeFormat), Valid: true},
		LastTestOk: sql.NullInt64{Int64: boolInt(ok), Valid: true},
		ID:         id,
	})
	if err != nil {
		return fmt.Errorf("record notification test: %w", err)
	}
	return nil
}

// InsertNotificationChannel adds a channel inside the transaction.
func (t *Tx) InsertNotificationChannel(c NotificationChannel) error {
	err := t.q.InsertNotificationChannel(t.ctx, sqlcgen.InsertNotificationChannelParams{
		ID: c.ID, Kind: c.Kind, Name: c.Name, Scope: c.Scope, OwnerLogin: c.OwnerLogin,
		Enabled: boolInt(c.Enabled), Events: eventsJSON(c.Events), MinSeverity: c.MinSeverity,
		Address: c.Address, Url: c.URL, HasSigning: boolInt(c.HasSigning),
		SmtpHost: c.SMTPHost, SmtpPort: int64(c.SMTPPort), SmtpUsername: c.SMTPUsername, SmtpFrom: c.SMTPFrom,
		SmtpHasPassword: boolInt(c.SMTPHasPassword),
	})
	if err != nil {
		return fmt.Errorf("insert notification channel: %w", err)
	}
	return nil
}

// UpdateNotificationChannel rewrites a channel's fields inside the
// transaction. The kind, scope and owner never change. It returns ErrNotFound
// when the channel does not exist.
func (t *Tx) UpdateNotificationChannel(c NotificationChannel) error {
	n, err := t.q.UpdateNotificationChannel(t.ctx, sqlcgen.UpdateNotificationChannelParams{
		Name: c.Name, Enabled: boolInt(c.Enabled), Events: eventsJSON(c.Events), MinSeverity: c.MinSeverity,
		Address: c.Address, Url: c.URL, HasSigning: boolInt(c.HasSigning),
		SmtpHost: c.SMTPHost, SmtpPort: int64(c.SMTPPort), SmtpUsername: c.SMTPUsername, SmtpFrom: c.SMTPFrom,
		SmtpHasPassword: boolInt(c.SMTPHasPassword), ID: c.ID,
	})
	if err != nil {
		return fmt.Errorf("update notification channel: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteNotificationChannel removes a channel inside the transaction. It
// returns ErrNotFound when the channel does not exist.
func (t *Tx) DeleteNotificationChannel(id string) error {
	n, err := t.q.DeleteNotificationChannel(t.ctx, id)
	if err != nil {
		return fmt.Errorf("delete notification channel: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
