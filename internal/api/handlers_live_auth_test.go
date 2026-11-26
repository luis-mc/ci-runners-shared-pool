package api

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/events"
	"github.com/luis-mc/ci-runners-shared-pool/internal/overview"
)

// switchAuthn answers with the role in role; 0 signs the session out, 1 is a viewer, 2 a user without a role.
type switchAuthn struct{ state atomic.Int32 }

func (a *switchAuthn) Authenticate(context.Context, *http.Request) (auth.Principal, error) {
	switch a.state.Load() {
	case 0:
		return auth.Principal{}, auth.ErrUnauthenticated
	case 1:
		return auth.Principal{UserID: 1, Login: "v", Role: auth.RoleViewer, Source: "cli", TokenID: "t"}, nil
	}
	return auth.Principal{UserID: 1, Login: "v", Role: "", Source: "cli", TokenID: "t"}, nil
}

func streamMux(authn Authenticator) (*http.ServeMux, *events.MemHub) {
	ov := &overview.Service{Source: ovSrc{}}
	hub := events.NewHub(events.Options{Snapshotter: ov, MaxStreams: 5})
	ov.Events = hub
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Live: Live{Overviews: ov, Hub: hub, KeepAlive: 20 * time.Millisecond, Recheck: 10 * time.Millisecond}, Authn: authn})
	return mux, hub
}

// R6.4: the stream sends nothing before the session is checked: no session or a user with no role gets an error
// envelope, no event-stream header, no event, and no subscription is opened.
func TestStreamNoDataBeforeAuth(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state int32
		want  int
	}{{"signed out", 0, http.StatusUnauthorized}, {"no role", 2, http.StatusForbidden}} {
		t.Run(tc.name, func(t *testing.T) {
			a := &switchAuthn{}
			a.state.Store(tc.state)
			mux, hub := streamMux(a)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/events", nil))
			if rec.Code != tc.want {
				t.Fatalf("status %d, want %d", rec.Code, tc.want)
			}
			if strings.Contains(rec.Header().Get("Content-Type"), "event-stream") || strings.Contains(rec.Body.String(), "event:") {
				t.Fatalf("data sent before auth: %q", rec.Body.String())
			}
			if hub.Streams() != 0 {
				t.Fatalf("%d streams opened for a refused request", hub.Streams())
			}
		})
	}
}

// R6.4: an open stream closes when the session ends or the user's role is removed.
func TestStreamClosesWhenRoleRemoved(t *testing.T) {
	for _, tc := range []struct {
		name string
		then int32
	}{{"session ended", 0}, {"role removed", 2}} {
		t.Run(tc.name, func(t *testing.T) {
			a := &switchAuthn{}
			a.state.Store(1)
			mux, hub := streamMux(a)
			srv := httptest.NewServer(mux)
			defer srv.Close()
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/api/v1/events", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d", resp.StatusCode)
			}
			sc := bufio.NewScanner(resp.Body)
			sawSnapshot := false
			for sc.Scan() && !sawSnapshot {
				sawSnapshot = sc.Text() == "event: snapshot"
			}
			if !sawSnapshot {
				t.Fatal("no snapshot before the change")
			}
			a.state.Store(tc.then)
			done := make(chan struct{})
			go func() {
				defer close(done)
				for sc.Scan() { // keep-alive lines until the server closes the stream
				}
			}()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("the stream stayed open after the session ended")
			}
			// Serve closes its subscription before the response ends, so the client's end of file comes after it.
			if hub.Streams() != 0 {
				t.Fatalf("%d subscriptions left", hub.Streams())
			}
		})
	}
}
