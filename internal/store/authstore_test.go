package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
)

func authDB(t *testing.T) (*Store, *AuthStore) {
	t.Helper()
	db, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a := db.Auth()
	if err := a.InsertUser(context.Background(), auth.User{GitHubID: 1, Login: "alice", Role: auth.RoleOwner, AddedBy: "setup"}); err != nil {
		t.Fatal(err)
	}
	return db, a
}

func TestLinkCodeUsedOnceUnderRace(t *testing.T) { // tray R6.1: single use
	_, a := authDB(t)
	ctx := context.Background()
	if err := a.InsertLinkCode(ctx, auth.LinkCode{Hash: "h1", UserID: 1, Purpose: auth.LinkSignIn, TargetPath: "/x", ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	var won atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if lc, err := a.UseLinkCode(ctx, "h1", time.Now()); err == nil {
				if lc.UserID != 1 || lc.TargetPath != "/x" {
					t.Errorf("%+v", lc)
				}
				won.Add(1)
			} else if !errors.Is(err, auth.ErrNotFound) {
				t.Errorf("%v", err)
			}
		}()
	}
	wg.Wait()
	if won.Load() != 1 {
		t.Fatalf("a link code worked %d times", won.Load())
	}
}

func TestLinkCodeExpiredAndUnknownAreRefused(t *testing.T) { // tray R5.7, R6.1
	_, a := authDB(t)
	ctx := context.Background()
	if err := a.InsertLinkCode(ctx, auth.LinkCode{Hash: "old", UserID: 1, Purpose: auth.LinkSignIn, TargetPath: "/", ExpiresAt: time.Now().Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"old", "never-issued"} {
		if _, err := a.UseLinkCode(ctx, h, time.Now()); !errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("%s: %v", h, err)
		}
	}
}

func TestLinkCodeStoredAsHashOnly(t *testing.T) { // tray R6.1: stored only as a hash
	db, _ := authDB(t)
	ctx := context.Background()
	svc := auth.New(db.Auth(), auth.Config{})
	code, err := svc.IssueLink(ctx, 1, auth.LinkSignIn, "/")
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := db.db.QueryRowContext(ctx, `SELECT code_hash FROM link_codes`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == code || strings.Contains(stored, code) || stored != auth.Hash(code) {
		t.Fatalf("stored %q for code %q", stored, code)
	}
	rows, err := db.ListAudit(ctx, AuditFilter{})
	if err != nil || len(rows) == 0 {
		t.Fatal(rows, err)
	}
	for _, r := range rows {
		if strings.Contains(r.Detail+r.TargetID+r.Actor, code) {
			t.Fatalf("code in audit row %+v", r)
		}
	}
}

func TestTrayBindingIsPerOSUserAndGoesWithTheUser(t *testing.T) { // tray R5.9
	_, a := authDB(t)
	ctx := context.Background()
	if err := a.InsertUser(ctx, auth.User{GitHubID: 2, Login: "bob", Role: auth.RoleViewer, AddedBy: "alice"}); err != nil {
		t.Fatal(err)
	}
	if err := a.UpsertTrayBinding(ctx, auth.TrayBinding{MachineID: "controller", OSUserID: "1000", UserID: 2}); err != nil {
		t.Fatal(err)
	}
	if b, err := a.GetTrayBinding(ctx, "controller", "1000"); err != nil || b.UserID != 2 {
		t.Fatal(b, err)
	}
	if _, err := a.GetTrayBinding(ctx, "controller", "1001"); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("another OS user: %v", err)
	}
	if err := a.DeleteUser(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := a.GetTrayBinding(ctx, "controller", "1000"); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("a removed user keeps no binding: %v", err)
	}
}

func TestSessionsAndAuditRoundTrip(t *testing.T) {
	_, a := authDB(t)
	ctx := context.Background()
	if err := a.InsertSession(ctx, auth.Session{IDHash: "s", CSRFHash: "c", UserID: 1, Source: "tray"}); err != nil {
		t.Fatal(err)
	}
	if _, u, err := a.GetSession(ctx, "s"); err != nil || u.Login != "alice" || u.Role != auth.RoleOwner {
		t.Fatal(u, err)
	}
	if err := a.EndSession(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.GetSession(ctx, "s"); !errors.Is(err, auth.ErrNotFound) {
		t.Fatal(err)
	}
	if err := a.InsertUser(ctx, auth.User{GitHubID: 3, Login: "eve", Role: auth.RoleOwner}); !errors.Is(err, auth.ErrConflict) {
		t.Fatalf("a second owner: %v", err)
	}
}
