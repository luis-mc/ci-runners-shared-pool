package github

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func healthOf(e *env) *AppHealth {
	return &AppHealth{Client: e.client, AppID: e.client.AppID, Now: e.clock}
}

func TestAppHealthWellAndGone(t *testing.T) { // R5.6
	e := newEnv(t)
	h := healthOf(e)
	ctx := context.Background()
	if st, err := h.Check(ctx); err != nil || st.Gone || h.Gone() {
		t.Fatalf("a live App is not gone: %+v %v", st, err)
	}
	e.fake.FailMatching("/app", http.StatusNotFound, 1)
	st, err := h.Check(ctx)
	if err != nil || !st.Gone || st.Reason != ReasonAppDeleted || !h.Gone() {
		t.Fatalf("a deleted App is gone: %+v %v", st, err)
	}
	if st, _ := h.Check(ctx); st.Gone {
		t.Fatal("the verdict follows the next answer: the App is back")
	}
}

func TestAppHealthKeyRefusedAndMissing(t *testing.T) { // R5.6
	e := newEnv(t)
	h := healthOf(e)
	ctx := context.Background()
	e.fake.FailMatching("/app", http.StatusUnauthorized, 1)
	if st, _ := h.Check(ctx); !st.Gone || st.Reason != ReasonKeyRefused {
		t.Fatalf("a refused key is gone: %+v", st)
	}
	if err := e.secrets.Delete(ctx, SecretKey(e.fake.AppID, SecretPrivateKey)); err != nil {
		t.Fatal(err)
	}
	e.client.UseApp(e.fake.AppID, "Iv23liFAKECLIENTID00") // forget the cached key
	if st, _ := h.Check(ctx); !st.Gone || st.Reason != ReasonKeyMissing {
		t.Fatalf("a missing key is gone: %+v", st)
	}
}

func TestAppHealthTransientKeepsTheVerdict(t *testing.T) { // R5.6: a bad hour of GitHub stops nothing
	e := newEnv(t)
	h := healthOf(e)
	ctx := context.Background()
	e.fake.FailMatching("/app", http.StatusNotFound, 1)
	if st, _ := h.Check(ctx); !st.Gone {
		t.Fatal("setup: gone")
	}
	e.fake.FailMatching("/app", http.StatusServiceUnavailable, 10)
	if _, err := h.Check(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("GitHub down is an error, not a verdict: %v", err)
	}
	if !h.Gone() {
		t.Fatal("the last verdict stays")
	}
	h2 := healthOf(e)
	if _, err := h2.Check(ctx); err == nil || h2.Gone() {
		t.Fatalf("a first check that fails settles nothing: %v", err)
	}
}

func TestAppHealthNewAppClearsTheVerdict(t *testing.T) { // the wizard ran again
	e := newEnv(t)
	h := healthOf(e)
	e.fake.FailMatching("/app", http.StatusNotFound, 1)
	if st, _ := h.Check(context.Background()); !st.Gone {
		t.Fatal("setup: gone")
	}
	e.client.UseApp(e.fake.AppID+1, "Iv23liOTHERCLIENT0000")
	if h.Gone() {
		t.Fatal("a verdict about the old App must not stop the new one")
	}
}

func TestAppHealthRunChecksAtStartAndHourly(t *testing.T) { // R5.6
	e := newEnv(t)
	h := healthOf(e)
	ticks := make(chan time.Time)
	var asked time.Duration
	changes := make(chan AppState, 4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.Run(ctx, func(d time.Duration) (<-chan time.Time, func()) { asked = d; return ticks, func() {} },
			func(st AppState) { changes <- st }, nil)
	}()
	waitFor(t, func() bool { return e.fake.Count("GET", "/app") == 1 })
	e.fake.FailMatching("/app", http.StatusNotFound, 1)
	ticks <- time.Now()
	if st := <-changes; !st.Gone {
		t.Fatalf("the tick found the App gone: %+v", st)
	}
	ticks <- time.Now()
	if st := <-changes; st.Gone {
		t.Fatalf("and back: %+v", st)
	}
	cancel()
	<-done
	if asked != time.Hour {
		t.Fatalf("every hour: %v", asked)
	}
}

func TestAppHealthNoAppNoCall(t *testing.T) {
	e := newEnv(t)
	h := &AppHealth{Client: e.client, AppID: func() int64 { return 0 }}
	if st, err := h.Check(context.Background()); err != nil || st.Gone || e.fake.Count("GET", "/app") != 0 {
		t.Fatalf("no App: no call, no verdict: %+v %v", st, err)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	giveUp := time.After(5 * time.Second)
	for !ok() {
		select {
		case <-tick.C:
		case <-giveUp:
			t.Fatal("timed out")
		}
	}
}
