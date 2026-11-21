package audit

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

func open(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestRowFields(t *testing.T) {
	ctx := context.Background()
	l := New(open(t))
	actor := Actor{Login: "octo", ID: "42", Source: SourceWeb}
	if err := l.Record(ctx, actor, "repo.on", Target{Type: "repository", ID: "r1"}, map[string]any{"summary": "pool on"}); err != nil {
		t.Fatal(err)
	}
	if err := l.RecordRefused(ctx, actor, "user.add", Target{Type: "user", ID: "u9"}, map[string]any{"why": "not owner"}); err != nil {
		t.Fatal(err)
	}
	p, err := l.List(ctx, Filter{})
	if err != nil || len(p.Items) != 2 {
		t.Fatalf("items=%v err=%v", p.Items, err)
	}
	refused, done := p.Items[0], p.Items[1]
	if done.Actor != "octo" || done.ActorID != "42" || done.Source != "web" || done.Action != "repo.on" ||
		done.TargetType != "repository" || done.TargetID != "r1" || done.Result != "done" || done.Summary != "pool on" {
		t.Errorf("done row: %+v", done)
	}
	if refused.Result != "refused" || refused.Summary != "why=not owner" {
		t.Errorf("refused row: %+v", refused)
	}
	if time.Since(done.At) > time.Minute || done.At.Location() != time.UTC {
		t.Errorf("time not UTC now: %v", done.At)
	}
}

func TestDefaultsToSystemActor(t *testing.T) {
	ctx := context.Background()
	l := New(open(t))
	if err := l.Record(ctx, Actor{}, "x.y", Target{}, nil); err != nil {
		t.Fatal(err)
	}
	p, _ := l.List(ctx, Filter{})
	if p.Items[0].Actor != "system" || p.Items[0].Source != "system" {
		t.Errorf("%+v", p.Items[0])
	}
	if ActorFrom(ctx) != System || ActorFrom(WithActor(ctx, Actor{Login: "a", Source: SourceCLI})).Login != "a" {
		t.Error("ActorFrom")
	}
}

func TestRedactsSecretsAndLongValues(t *testing.T) {
	ctx := context.Background()
	l := New(open(t))
	err := l.Record(ctx, System, "secret.rotate", Target{Type: "secret"}, map[string]any{
		"privatekey": "-----BEGIN-----", "nested": map[string]any{"webhooksecret": "hunter2"}, "note": strings.Repeat("a", 900),
	})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := l.List(ctx, Filter{})
	s := p.Items[0].Summary
	if strings.Contains(s, "BEGIN") || strings.Contains(s, "hunter2") || !strings.Contains(s, "[redacted]") {
		t.Errorf("secret leaked: %q", s)
	}
	if len(s) > 700 {
		t.Errorf("long value not cut: %d", len(s))
	}
}

func TestAuditFailureRollsBackChange(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	err := db.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.SetSetting("backup_time_local", `"04:00"`, "octo"); err != nil {
			return err
		}
		return RecordTx(tx, System, "", Target{}, nil) // an empty action cannot be written
	})
	if err == nil {
		t.Fatal("want the audit failure to fail the change")
	}
	rows, _ := db.ListSettings(ctx)
	if len(rows) != 0 {
		t.Fatalf("change survived a failed audit write: %v", rows)
	}
	// And the success path commits both together.
	err = db.WithTx(ctx, func(tx *store.Tx) error {
		if err := tx.SetSetting("backup_time_local", `"04:00"`, "octo"); err != nil {
			return err
		}
		return RecordTx(tx, System, "settings.update", Target{Type: "settings"}, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, _ = db.ListSettings(ctx)
	p, _ := New(db).List(ctx, Filter{})
	if len(rows) != 1 || len(p.Items) != 1 {
		t.Fatalf("rows=%v audit=%v", rows, p.Items)
	}
}

func TestNoUpdateOrDeletePath(t *testing.T) {
	typ := reflect.TypeOf(&Log{})
	for i := 0; i < typ.NumMethod(); i++ {
		n := strings.ToLower(typ.Method(i).Name)
		if strings.Contains(n, "update") || strings.Contains(n, "edit") || strings.Contains(n, "delete") || strings.Contains(n, "remove") {
			t.Errorf("Log has a mutating method %s", typ.Method(i).Name)
		}
	}
	if _, err := New(open(t)).List(context.Background(), Filter{Cursor: "x"}); !errors.Is(err, ErrBadCursor) {
		t.Errorf("want ErrBadCursor, got %v", err)
	}
}

func TestRetentionWritesOwnRow(t *testing.T) {
	ctx := context.Background()
	l := New(open(t))
	for i := 0; i < 3; i++ {
		_ = l.Record(ctx, System, "a.b", Target{}, nil)
	}
	n, err := l.Prune(ctx, time.Now().Add(time.Hour))
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	p, _ := l.List(ctx, Filter{})
	if len(p.Items) != 1 || p.Items[0].Action != "audit.retention" || !strings.Contains(p.Items[0].Summary, "deleted 3") {
		t.Fatalf("%+v", p.Items)
	}
	n, _ = l.Prune(ctx, time.Now().Add(-time.Hour))
	if n != 0 {
		t.Fatalf("recent rows must stay: %d", n)
	}
}

// LOOK49: a note stored under "detail" shows as plain text, without "detail=".
func TestSummaryShowsDetailWithoutItsKey(t *testing.T) {
	if got := Summary(`{"detail":"not on the list"}`); got != "not on the list" {
		t.Fatalf("got %q", got)
	}
	if got := Summary(`{"a":1,"detail":"x"}`); got != "a=1, detail=x" {
		t.Fatalf("got %q", got)
	}
}
