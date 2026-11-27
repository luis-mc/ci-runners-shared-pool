//go:build !windows

package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tray"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/trayserver"
)

// flow is the whole path of Open: the tray client, the local channel, the
// auth service on SQLite, the API handler (tray R5.6 to R5.9, R6.1, R6.8).
type flow struct {
	t       *testing.T
	db      *store.Store
	client  *tray.Client
	mux     *http.ServeMux
	console string
	uid     string
}

func newFlow(t *testing.T) *flow {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	db, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	console := "https://127.0.0.1:8460"
	area, links := newAuthArea(db, secrets.NewMemory(), console, logger)
	if err := db.Auth().SaveApp(ctx, auth.AppRecord{ID: 7, Slug: "crsp", ClientID: "Iv1.x", OwnerLogin: "alice", OwnerType: "User"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Auth().InsertUser(ctx, auth.User{GitHubID: 1, Login: "alice", Role: auth.RoleOwner, AddedBy: "setup"}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	api.Register(mux, api.Options{Version: "test", DB: db, Logger: logger, GitHub: area})

	// A short path: macOS limits a socket path to 104 bytes, t.TempDir() is longer.
	dir, err := os.MkdirTemp("", "crsp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "t.sock")
	ln, err := trayserver.Listen(sock, logger)
	if err != nil {
		t.Fatal(err)
	}
	b := trayBackend{Console: console, Links: links}
	go func() { _ = (&trayserver.Server{Backend: b, Logger: logger}).Serve(ctx, ln) }()
	dial := func(ctx context.Context) (net.Conn, error) { return (&net.Dialer{}).DialContext(ctx, "unix", sock) }
	return &flow{t: t, db: db, mux: mux, console: console, uid: strconv.Itoa(os.Getuid()),
		client: &tray.Client{Dial: dial, Timeout: 5 * time.Second}}
}

func (f *flow) bind(osUser string, gh int64) {
	f.t.Helper()
	if err := f.db.Auth().UpsertTrayBinding(context.Background(), auth.TrayBinding{MachineID: trayMachineID, OSUserID: osUser, UserID: gh}); err != nil {
		f.t.Fatal(err)
	}
}

// get sends a GET to the handler as a browser on remote.
func (f *flow) get(rawURL, remote string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	u, err := url.Parse(rawURL)
	if err != nil {
		f.t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, u.RequestURI(), nil)
	r.RemoteAddr = remote
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	return w
}

func (f *flow) audit(action string) int {
	rows, err := f.db.ListAudit(context.Background(), store.AuditFilter{Action: action})
	if err != nil {
		f.t.Fatal(err)
	}
	return len(rows)
}

func TestOpenSignsInOnceFromTheBoundOSUser(t *testing.T) { // R5.6, R5.7, R5.8, R6.1
	f := newFlow(t)
	f.bind(f.uid, 1)
	link, err := f.client.LinkTo(context.Background(), "/settings/updates")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link, f.console+"/auth/link?code=") {
		t.Fatalf("link %q", link)
	}
	if f.audit("auth.link_issued") != 1 {
		t.Fatal("issuing a link is audited")
	}

	w := f.get(link, "127.0.0.1:50000")
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/settings/updates" {
		t.Fatalf("%d %q", w.Code, w.Header().Get("Location"))
	}
	if strings.Contains(w.Header().Get("Location"), "code") {
		t.Fatal("the redirect keeps no code")
	}
	var session *http.Cookie
	for _, c := range w.Result().Cookies() {
		if strings.Contains(c.Name, "session") || c.HttpOnly {
			session = c
		}
	}
	if session == nil || !session.HttpOnly {
		t.Fatalf("no HttpOnly session cookie: %v", w.Result().Cookies())
	}
	me := f.get(f.console+"/api/v1/me", "127.0.0.1:50000", session)
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), "alice") {
		t.Fatalf("the link made no working session: %d %s", me.Code, me.Body)
	}
	if f.audit("auth.signin") != 1 {
		t.Fatal("a use is audited")
	}

	// Replay: the same code again lands on sign-in with the expired message.
	again := f.get(link, "127.0.0.1:50001")
	if again.Code != http.StatusFound || !strings.HasPrefix(again.Header().Get("Location"), "/sign-in?error=link_expired") {
		t.Fatalf("replay: %d %q", again.Code, again.Header().Get("Location"))
	}
	if len(again.Result().Cookies()) != 0 {
		t.Fatal("a replay makes no session")
	}
	if f.audit("auth.link_failed") != 1 {
		t.Fatal("a refused use is audited")
	}
}

func TestOpenLinkFromAnotherMachineIsRefused(t *testing.T) { // R5.8, R6.1
	f := newFlow(t)
	f.bind(f.uid, 1)
	link, err := f.client.Link(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	w := f.get(link, "192.168.1.20:4000")
	if !strings.HasPrefix(w.Header().Get("Location"), "/sign-in?error=link_local_only") || len(w.Result().Cookies()) != 0 {
		t.Fatalf("%d %q", w.Code, w.Header().Get("Location"))
	}
	// The refused attempt did not burn the code: this computer can still use it.
	if w := f.get(link, "[::1]:4000"); len(w.Result().Cookies()) == 0 {
		t.Fatalf("loopback use failed: %q", w.Header().Get("Location"))
	}
}

func TestOpenLinkExpiresAfterSixtySeconds(t *testing.T) { // R6.1
	f := newFlow(t)
	ctx := context.Background()
	// A code made 61 seconds ago: expiry is judged by the database clock.
	if err := f.db.Auth().InsertLinkCode(ctx, auth.LinkCode{Hash: auth.Hash("late"), UserID: 1, Purpose: auth.LinkSignIn, TargetPath: "/",
		ExpiresAt: time.Now().Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	w := f.get(f.console+"/auth/link?code=late", "127.0.0.1:1")
	if !strings.HasPrefix(w.Header().Get("Location"), "/sign-in?error=link_expired") || len(w.Result().Cookies()) != 0 {
		t.Fatalf("%q", w.Header().Get("Location"))
	}
	if w := f.get(f.console+"/auth/link?code=nonsense", "127.0.0.1:1"); !strings.HasPrefix(w.Header().Get("Location"), "/sign-in?error=link_expired") {
		t.Fatalf("unknown code: %q", w.Header().Get("Location"))
	}
	if auth.LinkTTL != 60*time.Second {
		t.Fatal("60 seconds")
	}
}

func TestOpenFromAnUnboundOSUserGetsGitHubSignInNotASession(t *testing.T) { // R5.9
	f := newFlow(t)
	f.bind("99999", 1) // alice is bound to another OS user, not to the caller
	link, err := f.client.LinkTo(context.Background(), "/machines")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link, f.console+"/auth/github/login?") || !strings.Contains(link, "bind_tray=") || strings.Contains(link, "/auth/link") {
		t.Fatalf("an unbound OS user must not get a sign-in link: %q", link)
	}
	if !strings.Contains(link, "return_to=%2Fmachines") {
		t.Fatalf("the target is kept: %q", link)
	}
	if f.audit("auth.link_issued") != 0 || f.audit("auth.bind_started") != 1 {
		t.Fatal("no link was issued; the start of binding is audited")
	}
}

func TestOpenAfterTheBoundUserWasRemovedAsksForGitHubAgain(t *testing.T) { // R5.9
	f := newFlow(t)
	ctx := context.Background()
	if err := f.db.Auth().InsertUser(ctx, auth.User{GitHubID: 2, Login: "bob", Role: auth.RoleViewer, AddedBy: "alice"}); err != nil {
		t.Fatal(err)
	}
	f.bind(f.uid, 2)
	if err := f.db.Auth().DeleteUser(ctx, 2); err != nil {
		t.Fatal(err)
	}
	link, err := f.client.Link(ctx)
	if err != nil || !strings.Contains(link, "/auth/github/login?") {
		t.Fatalf("%q %v", link, err)
	}
}

func TestOpenBeforeSetupGivesThePlainConsole(t *testing.T) {
	f := newFlow(t)
	// A fresh controller: no owner yet, so there is nobody to sign in as.
	ctx := context.Background()
	db, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, links := newAuthArea(db, secrets.NewMemory(), f.console, slog.New(slog.NewTextHandler(io.Discard, nil)))
	u, err := links.LinkFor(ctx, f.uid, "/x")
	if err != nil || u != f.console {
		t.Fatalf("%q %v", u, err)
	}
}
