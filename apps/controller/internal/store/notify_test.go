package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNotificationChannelRoundTrip(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	c := NotificationChannel{
		ID: "nch_1", Kind: "email", Name: "Mail", Scope: "personal", OwnerLogin: "alice", Enabled: true,
		Events: []string{"machine.offline", "disk.low"}, MinSeverity: "warning", Address: "alice@example.com",
		SMTPHost: "smtp.example.com", SMTPPort: 587, SMTPUsername: "mailer", SMTPFrom: "crsp@example.com", SMTPHasPassword: true,
	}
	if err := s.WithTx(ctx, func(tx *Tx) error { return tx.InsertNotificationChannel(c) }); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetNotificationChannel(ctx, "nch_1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Events, c.Events) || got.SMTPHost != "smtp.example.com" || !got.SMTPHasPassword || !got.Enabled || got.LastTestAt != nil || got.LastTestOK != nil {
		t.Fatalf("%+v", got)
	}

	c.Name, c.Enabled, c.Events, c.SMTPHasPassword = "Renamed", false, []string{"update.available"}, false
	if err := s.WithTx(ctx, func(tx *Tx) error { return tx.UpdateNotificationChannel(c) }); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	if err := s.SetNotificationChannelTest(ctx, "nch_1", at, true); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetNotificationChannel(ctx, "nch_1")
	if got.Name != "Renamed" || got.Enabled || got.SMTPHasPassword || len(got.Events) != 1 || got.LastTestOK == nil || !*got.LastTestOK || !got.LastTestAt.Equal(at) {
		t.Fatalf("%+v", got)
	}
	list, _ := s.ListNotificationChannels(ctx)
	if len(list) != 1 {
		t.Fatal(len(list))
	}
}

func TestNotificationChannelMissingAndDelete(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	if _, err := s.GetNotificationChannel(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.WithTx(ctx, func(tx *Tx) error { return tx.UpdateNotificationChannel(NotificationChannel{ID: "nope"}) }); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.WithTx(ctx, func(tx *Tx) error { return tx.DeleteNotificationChannel("nope") }); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	c := NotificationChannel{ID: "nch_2", Kind: "webhook", Name: "Hook", Scope: "pool", Enabled: true, MinSeverity: "info", URL: "https://x.example/h", HasSigning: true}
	_ = s.WithTx(ctx, func(tx *Tx) error { return tx.InsertNotificationChannel(c) })
	if err := s.WithTx(ctx, func(tx *Tx) error { return tx.DeleteNotificationChannel("nch_2") }); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListNotificationChannels(ctx); len(list) != 0 {
		t.Fatal("deleted")
	}
}

// The table has no column that could hold a secret: only the two flags.
func TestNotificationChannelsHoldNoSecretColumn(t *testing.T) {
	s := openTemp(t)
	rows, err := s.db.QueryContext(context.Background(), "SELECT name FROM pragma_table_info('notification_channels')")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != "smtp_has_password" && (strings.Contains(n, "secret") || strings.Contains(n, "password") || strings.Contains(n, "token")) {
			t.Errorf("column %q could hold a secret", n)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationChannelRejectsBadKind(t *testing.T) {
	s := openTemp(t)
	err := s.WithTx(context.Background(), func(tx *Tx) error {
		return tx.InsertNotificationChannel(NotificationChannel{ID: "x", Kind: "sms", Name: "n", Scope: "pool", MinSeverity: "info"})
	})
	if err == nil {
		t.Fatal("the kind is checked by the database")
	}
}
