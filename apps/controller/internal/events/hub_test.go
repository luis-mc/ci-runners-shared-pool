package events

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type snap struct{}

func (snap) Snapshot(context.Context) ([]byte, error) { return []byte(`{"queued_count":0}`), nil }

func recv(t *testing.T, s Subscription) Event {
	t.Helper()
	select {
	case e, ok := <-s.Events():
		if !ok {
			t.Fatal("stream closed")
		}
		return e
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
	return Event{}
}

func TestFirstEventIsSnapshotThenChangesInOrder(t *testing.T) {
	h := NewHub(Options{Snapshotter: snap{}})
	s, err := h.Subscribe(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if e := recv(t, s); e.Type != Snapshot || e.ReplayGap {
		t.Fatalf("first = %+v", e)
	}
	h.Publish(JobQueued, []byte(`{"id":1}`))
	h.Publish(JobStarted, []byte(`{"id":1}`))
	a, b := recv(t, s), recv(t, s)
	if a.Type != JobQueued || b.Type != JobStarted || b.ID != a.ID+1 {
		t.Fatalf("got %+v %+v", a, b)
	}
}

func TestReplayByLastEventID(t *testing.T) {
	h := NewHub(Options{Snapshotter: snap{}})
	for i := 0; i < 5; i++ {
		h.Publish(JobQueued, []byte(`{}`))
	}
	s, err := h.Subscribe(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	for want := uint64(3); want <= 5; want++ {
		if e := recv(t, s); e.ID != want || e.Type != JobQueued {
			t.Fatalf("want id %d got %+v", want, e)
		}
	}
	// Caught up: nothing is pending and a snapshot is not sent.
	select {
	case e := <-s.Events():
		t.Fatalf("unexpected %+v", e)
	default:
	}
}

func TestReplayGapSendsMarkedSnapshot(t *testing.T) {
	h := NewHub(Options{Snapshotter: snap{}, ReplayEvents: 3})
	for i := 0; i < 10; i++ {
		h.Publish(JobQueued, []byte(`{}`))
	}
	s, _ := h.Subscribe(context.Background(), 2)
	e := recv(t, s)
	if e.Type != Snapshot || !e.ReplayGap {
		t.Fatalf("got %+v", e)
	}
	// An id from the future (controller restarted) is a gap too.
	s2, _ := h.Subscribe(context.Background(), 999)
	if e := recv(t, s2); e.Type != Snapshot || !e.ReplayGap {
		t.Fatalf("future id: %+v", e)
	}
}

func TestReplayAgeLimit(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	h := NewHub(Options{Snapshotter: snap{}, Now: func() time.Time { return now }})
	h.Publish(JobQueued, []byte(`{}`))
	now = now.Add(11 * time.Minute)
	h.Publish(JobQueued, []byte(`{}`))
	s, _ := h.Subscribe(context.Background(), 0+1) // id 1 is older than 10 min and gone
	e := recv(t, s)
	if e.ID != 2 || e.Type != JobQueued {
		t.Fatalf("got %+v", e)
	}
	s2, _ := h.Subscribe(context.Background(), 0)
	if recv(t, s2).Type != Snapshot {
		t.Fatal("want snapshot")
	}
}

func TestSlowSubscriberIsClosedAndNeverBlocksPublisher(t *testing.T) {
	h := NewHub(Options{Snapshotter: snap{}, MaxBehind: 5})
	s, _ := h.Subscribe(context.Background(), 0)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			h.Publish(JobQueued, []byte(`{}`))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publisher blocked")
	}
	n := 0
	for range s.Events() { // drains, then sees the close
		n++
	}
	if n > 6 || h.Streams() != 0 {
		t.Fatalf("got %d events, %d streams", n, h.Streams())
	}
}

func TestStreamCap(t *testing.T) {
	h := NewHub(Options{Snapshotter: snap{}, MaxStreams: 2})
	for i := 0; i < 2; i++ {
		if _, err := h.Subscribe(context.Background(), 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.Subscribe(context.Background(), 0); !errors.Is(err, ErrTooManyStreams) {
		t.Fatalf("err = %v", err)
	}
	if got := NewHub(Options{Snapshotter: snap{}, MaxStreams: 9999}).opt.MaxStreams; got != CeilingMaxStreams {
		t.Fatalf("ceiling: %d", got)
	}
}

func TestLoadThrottlePerMachine(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	h := NewHub(Options{Snapshotter: snap{}, Now: func() time.Time { return now }})
	h.Publish(MachineLoad, []byte(`{"machine_id":"a"}`))
	h.Publish(MachineLoad, []byte(`{"machine_id":"a"}`)) // dropped
	h.Publish(MachineLoad, []byte(`{"machine_id":"b"}`)) // other machine
	now = now.Add(3 * time.Second)
	h.Publish(MachineLoad, []byte(`{"machine_id":"a"}`))
	if h.LastID() != 3 {
		t.Fatalf("last id %d, want 3", h.LastID())
	}
}

func TestRedactorRunsBeforeStore(t *testing.T) {
	h := NewHub(Options{Snapshotter: snap{}, Redact: func(b []byte) []byte {
		return []byte(strings.ReplaceAll(string(b), "ghs_secret", "[redacted:token]"))
	}})
	s, _ := h.Subscribe(context.Background(), 0)
	recv(t, s)
	h.Publish(JobQueued, []byte(`{"x":"ghs_secret"}`))
	e := recv(t, s)
	if strings.Contains(string(e.Data), "ghs_secret") {
		t.Fatalf("leaked: %s", e.Data)
	}
}

func TestStopClosesStreams(t *testing.T) {
	h := NewHub(Options{Snapshotter: snap{}})
	s, _ := h.Subscribe(context.Background(), 0)
	h.Stop()
	recv(t, s) // the snapshot
	if _, ok := <-s.Events(); ok {
		t.Fatal("still open")
	}
}

// leaveOnKeepAlive cancels the request once a keep-alive comment was written.
type leaveOnKeepAlive struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (l *leaveOnKeepAlive) Write(p []byte) (int, error) {
	n, err := l.ResponseRecorder.Write(p)
	if strings.Contains(l.Body.String(), ": keep-alive") {
		l.cancel()
	}
	return n, err
}

// WriteString is the path io.WriteString takes; the recorder has its own.
func (l *leaveOnKeepAlive) WriteString(str string) (int, error) { return l.Write([]byte(str)) }

func TestSSEFormatAndServe(t *testing.T) {
	h := NewHub(Options{Snapshotter: snap{}})
	h.Publish(JobQueued, []byte(`{"id":7}`))
	s, _ := h.Subscribe(context.Background(), 0)
	ctx, cancel := context.WithCancel(context.Background())
	// The client leaves as soon as it has seen the first keep-alive.
	rec := &leaveOnKeepAlive{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	req := httptest.NewRequestWithContext(ctx, "GET", "/api/v1/events", nil)
	Serve(rec, req, s, 20*time.Millisecond)
	body := rec.Body.String()
	for _, want := range []string{"event: snapshot", `"snapshot":{"queued_count":0}`, ": keep-alive", "\n\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	if rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Error("content type")
	}
	if ParseLastEventID("12") != 12 || ParseLastEventID("x") != 0 || ParseLastEventID("") != 0 {
		t.Error("ParseLastEventID")
	}
	got := string(LiveEventJSON(Event{ID: 9, Type: JobQueued, At: time.Unix(0, 0), Data: []byte(`{"id":7}`), ReplayGap: true}))
	if !strings.Contains(got, `"id":"9"`) || !strings.Contains(got, `"job":{"id":7}`) || !strings.Contains(got, `"replay_gap":true`) {
		t.Errorf("LiveEventJSON = %s", got)
	}
}
