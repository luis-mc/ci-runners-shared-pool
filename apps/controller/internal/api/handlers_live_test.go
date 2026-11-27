package api

import (
	"bufio"
	"context"
	"encoding/json"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/events"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/overview"
)

type ovSrc struct{}

func (ovSrc) Machines(context.Context) ([]gen.Machine, error) {
	return []gen.Machine{{Id: "m1", Name: "alpha", Online: true}}, nil
}
func (ovSrc) QueuedJobs(context.Context) ([]gen.Job, error) {
	r, text := gen.WaitReasonLoadNotMeasured, "load not measured yet"
	return []gen.Job{{Id: 1, JobName: "build", Repository: "o/r", WaitReason: &r, WaitText: &text}}, nil
}
func (ovSrc) RunningJobs(context.Context) ([]gen.Job, error) { return nil, nil }
func (ovSrc) MinutesSavedSince(context.Context, time.Time) ([]overview.OSMinutes, error) {
	return []overview.OSMinutes{{OS: "linux", Minutes: 7}}, nil
}

func liveMux(maxStreams int) (*http.ServeMux, *events.MemHub) {
	ov := &overview.Service{Source: ovSrc{}}
	hub2 := events.NewHub(events.Options{Snapshotter: ov, MaxStreams: maxStreams})
	ov.Events = hub2
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Live: Live{Overviews: ov, Hub: hub2, KeepAlive: 20 * time.Millisecond}, Authn: asRole(auth.RoleOwner)})
	return mux, hub2
}

func TestGetOverview(t *testing.T) {
	mux, hub := liveMux(5)
	hub.Publish(events.JobQueued, []byte(`{}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/overview", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var o gen.Overview
	if err := json.Unmarshal(rec.Body.Bytes(), &o); err != nil {
		t.Fatal(err)
	}
	if len(o.Machines) != 1 || o.QueuedCount != 1 || o.LastEventId != "1" || (*o.MinutesSavedToday)[0].Minutes != 7 {
		t.Fatalf("overview = %+v", o)
	}
	// R5.48 contract: the waiting list carries the reason and its words.
	if o.Waiting == nil || len(*o.Waiting) != 1 {
		t.Fatalf("waiting = %+v", o.Waiting)
	}
	w := (*o.Waiting)[0]
	if w.JobId != 1 || w.Reason != gen.WaitReasonLoadNotMeasured || !strings.HasPrefix(w.Words, "Load not measured yet.") {
		t.Fatalf("waiting job = %+v", w)
	}
	if !strings.Contains(rec.Body.String(), `"reason":"load_not_measured"`) {
		t.Fatalf("body: %s", rec.Body)
	}
}

// readEvents reads SSE frames from a live server until n events arrived.
func readEvents(t *testing.T, url, lastID string, n int) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url+"/api/v1/events", nil)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status %d type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var got []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() && len(got) < n {
		if line := sc.Text(); strings.HasPrefix(line, "event: ") {
			got = append(got, strings.TrimPrefix(line, "event: "))
		}
	}
	return got
}

func TestStreamEventsSnapshotThenChanges(t *testing.T) {
	mux, hub := liveMux(5)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	go func() {
		// Publish once the stream is subscribed, so the change follows the snapshot.
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		giveUp := time.After(3 * time.Second)
		for hub.Streams() == 0 {
			select {
			case <-tick.C:
			case <-giveUp:
				return // readEvents fails with its own timeout
			}
		}
		hub.Publish(events.JobStarted, []byte(`{"id":1}`))
	}()
	got := readEvents(t, srv.URL, "", 2)
	if len(got) != 2 || got[0] != "snapshot" || got[1] != "job.started" {
		t.Fatalf("events = %v", got)
	}
}

func TestStreamEventsReplayByLastEventID(t *testing.T) {
	mux, hub := liveMux(5)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	hub.Publish(events.JobQueued, []byte(`{}`))
	hub.Publish(events.JobStarted, []byte(`{}`))
	hub.Publish(events.JobFinished, []byte(`{}`))
	got := readEvents(t, srv.URL, "1", 2)
	if len(got) != 2 || got[0] != "job.started" || got[1] != "job.finished" {
		t.Fatalf("replayed = %v", got)
	}
}

func TestStreamEventsTooManyStreams(t *testing.T) {
	mux, hub := liveMux(1)
	sub, err := hub.Subscribe(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/events", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d", rec.Code)
	}
	assertEnvelope(t, rec, CodeRateLimited)
}

func doctorMux() *http.ServeMux {
	reg := doctor.NewRegistry()
	reg.Register(doctor.NewCheck("machine.online", doctor.GroupMachine, 1, false, func(context.Context, doctor.Env) []doctor.Finding {
		return []doctor.Finding{{Subject: doctor.Subject{Kind: "machine", ID: "m1", Name: "alpha"}, Severity: doctor.Warn, Title: "x", Fix: doctor.Fix{Text: "do it"}}}
	}))
	eng := &doctor.Engine{Registry: reg, Store: doctor.NewMemStore(), Env: func(context.Context) (doctor.Env, error) { return doctor.Env{}, nil }}
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Doctor: &doctor.Service{Engine: eng}, Authn: asRole(auth.RoleOwner)})
	return mux
}

func TestDoctorRoutes(t *testing.T) {
	mux := doctorMux()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/doctor/runs", strings.NewReader(`{"checks":["machine"]}`)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var run gen.DoctorRun
	_ = json.Unmarshal(rec.Body.Bytes(), &run)
	if run.Id == "" || run.Trigger != "web" || run.Scope == nil {
		t.Fatalf("run = %+v", run)
	}
	var got gen.DoctorRun
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	giveUp := time.After(10 * time.Second)
	for got.State != "done" {
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/doctor/runs/"+run.Id, nil))
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		if got.State == "done" {
			break
		}
		select {
		case <-tick.C:
		case <-giveUp:
			t.Fatalf("the doctor run did not finish: %+v", got)
		}
	}
	if got.State != "done" || got.Worst == nil || *got.Worst != "warn" || len(got.Findings) != 1 {
		t.Fatalf("run = %+v", got)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/doctor/findings?severity=warn", nil))
	var list gen.DoctorFindingList
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if rec.Code != 200 || len(list.Items) != 1 || list.LastRunAt == nil {
		t.Fatalf("findings %d %+v", rec.Code, list)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/doctor/findings?severity=fail", nil))
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Items) != 0 {
		t.Fatalf("fail filter = %+v", list)
	}
}

func TestDoctorRunNotFoundAndNoBody(t *testing.T) {
	mux := doctorMux()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/doctor/runs/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
	assertEnvelope(t, rec, CodeNotFound)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/doctor/runs", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("run without body: %d %s", rec.Code, rec.Body)
	}
}
