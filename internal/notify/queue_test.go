package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/secrets"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

// recorder is a webhook receiver whose answer the test sets.
type recorder struct {
	srv    *httptest.Server
	mu     sync.Mutex
	hits   []hit
	status atomic.Int32
	retry  atomic.Value // string
}

func newRecorder(t *testing.T, status int) *recorder {
	t.Helper()
	r := &recorder{}
	r.status.Store(int32(status))
	r.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var b [8192]byte
		n, _ := req.Body.Read(b[:])
		r.mu.Lock()
		r.hits = append(r.hits, hit{header: req.Header.Clone(), body: append([]byte(nil), b[:n]...), path: req.URL.Path})
		r.mu.Unlock()
		if v, _ := r.retry.Load().(string); v != "" {
			w.Header().Set("Retry-After", v)
		}
		w.WriteHeader(int(r.status.Load()))
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *recorder) count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.hits) }

func (e *env) hook(t *testing.T, r *recorder, events ...string) Channel {
	t.Helper()
	if len(events) == 0 {
		events = allEvents
	}
	in := webhookInput(r.srv.URL + "/crsp/T000/token-xyz")
	in.Events = events
	c, err := e.svc.Create(context.Background(), owner, in)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func firstDelivery(t *testing.T, e *env, channel string) NoticeRow {
	t.Helper()
	for _, r := range e.notices(t) {
		for _, d := range r.Deliveries {
			if d.Channel == channel {
				return r
			}
		}
	}
	t.Fatalf("no notice with a delivery on %s", channel)
	return NoticeRow{}
}

func newHookEnv(t *testing.T, r *recorder) *env {
	return newEnv(t, func(c *Config) { c.Trust = trustOf(r.srv); c.ConsoleURL = "https://crsp.lan" })
}

func TestRealDeliveryIsSignedAndComplete(t *testing.T) { // R5.20, R5.21, R5.19
	r := newRecorder(t, 200)
	e := newHookEnv(t, r)
	ctx := context.Background()
	e.hook(t, r)
	if err := e.svc.Observe(ctx, scopeOf("machine.offline"), []Fact{machineFact("build-02")}); err != nil {
		t.Fatal(err)
	}
	e.svc.Tick(ctx)
	if r.count() != 1 {
		t.Fatalf("%d posts", r.count())
	}
	h := r.hits[0]
	ts := h.header.Get(HeaderTimestamp)
	if h.header.Get(HeaderSignature) != Sign([]byte(webhookSecret), ts, h.body) || h.header.Get(HeaderTest) != "" {
		t.Fatalf("%v", h.header)
	}
	var b map[string]any
	_ = json.Unmarshal(h.body, &b)
	for _, k := range []string{"schema_version", "event_id", "delivery_id", "type", "subject", "state", "tone", "time", "summary", "link"} {
		if _, ok := b[k]; !ok {
			t.Errorf("missing %s: %s", k, h.body)
		}
	}
	if b["type"] != "machine.offline" || b["state"] != "firing" || b["tone"] != "problem" || b["link"] != "https://crsp.lan/machines/build-02" ||
		b["delivery_id"] != h.header.Get(HeaderDelivery) || b["test"] != nil {
		t.Fatalf("%v", b)
	}
	if strings.Contains(string(h.body), webhookSecret) || strings.Contains(string(h.body), "token-xyz") {
		t.Fatal("no secret and no webhook path in a body")
	}
	row := firstDelivery(t, e, "Team chat")
	if d := row.Deliveries[0]; d.Status != store.DeliveryDelivered || d.Target != strings.TrimPrefix(r.srv.URL, "https://") || strings.Contains(d.Target, "token") {
		t.Fatalf("%+v", d)
	}
}

func TestRetryScheduleAndRetryAfter(t *testing.T) { // R5.23
	r := newRecorder(t, 503)
	e := newHookEnv(t, r)
	ctx := context.Background()
	e.hook(t, r, "machine.offline")
	_ = e.svc.Observe(ctx, scopeOf("machine.offline"), []Fact{machineFact("m1")})
	var ids []string
	var waits []time.Duration
	for try := 0; try < len(Backoff); try++ {
		e.svc.Tick(ctx)
		d := e.notices(t)[0].Deliveries[0]
		if d.Status != store.DeliveryQueued || d.Attempt != try+1 || d.NextTry == nil {
			t.Fatalf("try %d: %+v", try, d)
		}
		waits = append(waits, d.NextTry.Sub(*e.now))
		ids = append(ids, r.hits[len(r.hits)-1].header.Get(HeaderDelivery))
		e.advance(waits[try])
	}
	for i, w := range waits {
		lo, hi := time.Duration(float64(Backoff[i])*0.79), time.Duration(float64(Backoff[i])*1.21)
		if w < lo || w > hi {
			t.Errorf("wait %d is %s, want about %s", i, w, Backoff[i])
		}
	}
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("the delivery id is the same on every retry: %v", ids)
		}
	}
	// The seventh try fails for good, shows as failed with the error, and the next tick sends nothing.
	e.svc.Tick(ctx)
	d := e.notices(t)[0].Deliveries[0]
	if d.Status != store.DeliveryFailed || !strings.Contains(d.LastError, "HTTP 503") || d.NextTry != nil {
		t.Fatalf("%+v", d)
	}
	n := r.count()
	e.advance(5 * time.Hour)
	e.svc.Tick(ctx)
	if r.count() != n || n != len(Backoff)+1 {
		t.Fatalf("%d posts, want %d", r.count(), len(Backoff)+1)
	}
}

func TestRetryAfterIsHonouredUpToAnHour(t *testing.T) { // R5.23
	r := newRecorder(t, 429)
	e := newHookEnv(t, r)
	ctx := context.Background()
	e.hook(t, r, "machine.offline")
	_ = e.svc.Observe(ctx, scopeOf("machine.offline"), []Fact{machineFact("m1")})
	r.retry.Store("1200")
	e.svc.Tick(ctx)
	d := e.notices(t)[0].Deliveries[0]
	if w := d.NextTry.Sub(*e.now); w < 20*time.Minute || w > 21*time.Minute {
		t.Fatalf("Retry-After 1200 s is honoured: %s", w)
	}
	e.advance(21 * time.Minute)
	r.retry.Store("86400")
	e.svc.Tick(ctx)
	d = e.notices(t)[0].Deliveries[0]
	if w := d.NextTry.Sub(*e.now); w != time.Hour {
		t.Fatalf("a Retry-After over 1 h is capped at 1 h: %s", w)
	}
}

func TestOtherClientErrorsAreNotRetried(t *testing.T) { // R5.23
	for _, code := range []int{400, 401, 403, 404, 410} {
		r := newRecorder(t, code)
		e := newHookEnv(t, r)
		ctx := context.Background()
		e.hook(t, r, "machine.offline")
		_ = e.svc.Observe(ctx, scopeOf("machine.offline"), []Fact{machineFact("m1")})
		e.svc.Tick(ctx)
		e.advance(3 * time.Hour)
		e.svc.Tick(ctx)
		d := e.notices(t)[0].Deliveries[0]
		if d.Status != store.DeliveryFailed || d.Attempt != 1 || r.count() != 1 {
			t.Fatalf("%d: %+v posts=%d", code, d, r.count())
		}
	}
}

func TestNetworkErrorIsRetried(t *testing.T) { // R5.23
	r := newRecorder(t, 200)
	e := newHookEnv(t, r)
	ctx := context.Background()
	e.hook(t, r, "machine.offline")
	r.srv.Close()
	_ = e.svc.Observe(ctx, scopeOf("machine.offline"), []Fact{machineFact("m1")})
	e.svc.Tick(ctx)
	d := e.notices(t)[0].Deliveries[0]
	if d.Status != store.DeliveryQueued || d.Attempt != 1 || !strings.Contains(d.LastError, "Could not reach") || strings.Contains(d.LastError, "token-xyz") {
		t.Fatalf("%+v", d)
	}
}

func TestFailingEndpointRaisesFinding(t *testing.T) { // R5.24
	r := newRecorder(t, 404) // a final failure at once
	e := newHookEnv(t, r)
	ctx := context.Background()
	e.hook(t, r, "machine.offline")
	for i := 0; i < 3; i++ {
		_ = e.svc.Observe(ctx, scopeOf("machine.offline"), []Fact{machineFact(fmt.Sprint("m", i))})
		e.svc.Tick(ctx)
		e.advance(time.Minute)
	}
	findings := []NoticeRow{}
	for _, row := range e.notices(t) {
		if row.Type == "doctor.finding" {
			findings = append(findings, row)
		}
	}
	if len(findings) != 1 || findings[0].SubjectKind != "endpoint" || findings[0].Severity != "warning" || findings[0].Tone != ToneNeedsYou ||
		!strings.Contains(findings[0].Summary, "Team chat") || strings.Contains(findings[0].Summary, "token-xyz") {
		t.Fatalf("three failed deliveries in a row raise one warning finding: %+v", findings)
	}
	// One delivered notice later, the finding resolves.
	r.status.Store(200)
	_ = e.svc.Observe(ctx, scopeOf("machine.offline"), []Fact{machineFact("m9")})
	e.advance(11 * time.Minute)
	e.svc.Tick(ctx)
	resolved := 0
	for _, row := range e.notices(t) {
		if row.Type == "doctor.finding" && row.State == StateResolved {
			resolved++
		}
	}
	if resolved != 1 {
		t.Fatalf("the finding resolves when the endpoint delivers again: %d", resolved)
	}
}

// brokenSecrets is a secret store that cannot be reached.
type brokenSecrets struct{ secrets.Store }

func (brokenSecrets) Get(context.Context, secrets.Key) ([]byte, error) { return nil, errors.New("down") }

func TestNoSecretNoSend(t *testing.T) { // R6.3
	r := newRecorder(t, 200)
	e := newHookEnv(t, r)
	ctx := context.Background()
	e.hook(t, r, "machine.offline")
	e.svc.cfg.Secrets = brokenSecrets{e.sec}
	_ = e.svc.Observe(ctx, scopeOf("machine.offline"), []Fact{machineFact("m1")})
	for i := 0; i < 10; i++ {
		e.svc.Tick(ctx)
		e.advance(2 * time.Minute)
	}
	if r.count() != 0 {
		t.Fatalf("never sent unsigned: %d posts", r.count())
	}
	var dev DeliveryRow
	var finding bool
	for _, row := range e.notices(t) {
		if row.Type == "machine.offline" {
			dev = row.Deliveries[0]
		}
		if row.Type == "doctor.finding" && row.State == StateFiring {
			finding = true
		}
	}
	if dev.Status != store.DeliveryQueued || dev.Attempt != 0 || !finding {
		t.Fatalf("it stays queued, uncounted, and raises a finding: %+v finding=%v", dev, finding)
	}
	// The secret store comes back: it is sent.
	e.svc.cfg.Secrets = e.sec
	e.svc.Tick(ctx)
	if r.count() != 1 {
		t.Fatalf("%d", r.count())
	}
}

func TestEndpointRateNeverDrops(t *testing.T) { // R5.14
	r := newRecorder(t, 200)
	e := newHookEnv(t, r)
	ctx := context.Background()
	ch := e.hook(t, r, "job.waiting")
	for i := 0; i < 70; i++ {
		_ = e.svc.Observe(ctx, scopeOf("job.waiting"), []Fact{{SubjectKind: "job", SubjectID: fmt.Sprint(i), SubjectName: fmt.Sprint("job ", i)}})
		_ = e.svc.Observe(ctx, scopeOf("job.waiting"), nil) // webhooks are not capped by R5.12
		e.advance(100 * time.Millisecond)
	}
	for i := 0; i < 5; i++ {
		e.svc.Tick(ctx)
	}
	if n := r.count(); n != WebhookPerMinute {
		t.Fatalf("at most 60 a minute: %d", n)
	}
	waiting := 0
	for _, row := range e.notices(t) {
		for _, d := range row.Deliveries {
			if d.Status == store.DeliveryQueued {
				waiting++
			}
		}
	}
	if waiting != 140-WebhookPerMinute {
		t.Fatalf("the rest wait in the queue: %d", waiting)
	}
	_ = ch
	for m := 0; m < 4; m++ {
		e.advance(time.Minute)
		for i := 0; i < 4; i++ {
			e.svc.Tick(ctx)
		}
	}
	left := 0
	for _, row := range e.notices(t) {
		for _, d := range row.Deliveries {
			if d.Status == store.DeliveryQueued {
				left++
			}
		}
	}
	if left != 0 || r.count() != 140 {
		t.Fatalf("none dropped: left=%d sent=%d", left, r.count())
	}
}

func TestEmailGoesThroughTheQueue(t *testing.T) { // R5.28
	srv := newFakeSMTP(t, nil, "")
	e := newEnv(t, nil)
	ctx := context.Background()
	in := emailInput(srv.port())
	in.SMTP.Password, in.SMTP.Username = "", ""
	in.Events = []string{"machine.offline"}
	if _, err := e.svc.Create(ctx, alice, in); err != nil {
		t.Fatal(err)
	}
	_ = e.svc.Observe(ctx, scopeOf("machine.offline"), []Fact{machineFact("build-02")})
	// Observe sent nothing: a send never runs inside the state change.
	srv.mu.Lock()
	sent := srv.Data
	srv.mu.Unlock()
	if sent != "" {
		t.Fatal("a send must not run inside the request")
	}
	e.svc.Tick(ctx)
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.To != "alice@example.com" || !strings.Contains(srv.Data, "Subject: [crsp] Machine offline (Problem)") || !strings.Contains(srv.Data, "build-02 stopped answering") {
		t.Fatalf("%q %q", srv.To, srv.Data)
	}
}

func TestEmailRefusedIsFinalAndShown(t *testing.T) { // R5.28
	srv := newFakeSMTP(t, nil, "")
	srv.rejectTo = "alice@example.com"
	e := newEnv(t, nil)
	ctx := context.Background()
	in := emailInput(srv.port())
	in.SMTP.Password, in.SMTP.Username = "", ""
	_, _ = e.svc.Create(ctx, alice, in)
	_ = e.svc.Observe(ctx, scopeOf("machine.offline"), []Fact{machineFact("m1")})
	e.svc.Tick(ctx)
	d := e.notices(t)[0].Deliveries[0]
	if d.Status != store.DeliveryFailed || !strings.Contains(d.LastError, "550") {
		t.Fatalf("%+v", d)
	}
}

func TestDesktopWaitsForTheTray(t *testing.T) { // R5.15, R5.17 seam
	e := newEnv(t, nil)
	ctx := context.Background()
	e.desktop(t, owner)
	e.desktop(t, alice)
	_ = e.svc.Observe(ctx, scopeOf("machine.offline"), []Fact{machineFact("build-02")})
	e.svc.Tick(ctx)
	got, err := e.svc.PendingDesktop(ctx, "octo", 10)
	if err != nil || len(got) != 1 || got[0].Title != "Machine offline" || got[0].Body != "build-02 stopped answering at 14:30 UTC." {
		t.Fatalf("%+v %v", got, err)
	}
	if err := e.svc.AckDesktop(ctx, got[0].DeliveryID); err != nil {
		t.Fatal(err)
	}
	if again, _ := e.svc.PendingDesktop(ctx, "octo", 10); len(again) != 0 {
		t.Fatalf("a shown notice is not shown twice: %+v", again)
	}
	if other, _ := e.svc.PendingDesktop(ctx, "alice", 10); len(other) != 1 {
		t.Fatalf("each user's tray gets their own: %+v", other)
	}
	// A resolved notice says so in the past tense.
	_ = e.svc.Observe(ctx, scopeOf("machine.offline"), nil)
	e.svc.Tick(ctx)
	got, _ = e.svc.PendingDesktop(ctx, "octo", 10)
	if len(got) != 1 || got[0].Body != "build-02 is back online." {
		t.Fatalf("%+v", got)
	}
}

func TestListShowsDeliveriesOnlyToTheirOwner(t *testing.T) { // R5.32, R5.6
	r := newRecorder(t, 200)
	e := newHookEnv(t, r)
	ctx := context.Background()
	e.hook(t, r, "machine.offline")
	e.desktop(t, alice, "machine.offline")
	_ = e.svc.Observe(ctx, scopeOf("machine.offline"), []Fact{machineFact("m1")})
	e.svc.Tick(ctx)
	admin, _ := e.svc.Notices(ctx, owner, 10)
	mine, _ := e.svc.Notices(ctx, alice, 10)
	other, _ := e.svc.Notices(ctx, bobber, 10)
	if len(admin[0].Deliveries) != 2 || len(mine[0].Deliveries) != 1 || mine[0].Deliveries[0].Kind != KindDesktop || len(other) != 1 || len(other[0].Deliveries) != 0 {
		t.Fatalf("an admin sees every delivery, a user their own: %d %d %d", len(admin[0].Deliveries), len(mine[0].Deliveries), len(other[0].Deliveries))
	}
}

func TestThirtyDayPrune(t *testing.T) { // R6.5
	e := newEnv(t, nil)
	ctx := context.Background()
	e.desktop(t, owner)
	_ = e.svc.Observe(ctx, scopeOf("machine.offline"), []Fact{machineFact("m1")})
	e.advance(29 * 24 * time.Hour)
	e.svc.Tick(ctx)
	if n := len(e.notices(t)); n != 1 {
		t.Fatalf("kept inside 30 days: %d", n)
	}
	e.advance(2 * 24 * time.Hour)
	e.svc.Tick(ctx)
	if n := len(e.notices(t)); n != 0 {
		t.Fatalf("deleted after 30 days: %d", n)
	}
	e.advance(time.Hour)
	e.svc.Tick(ctx)
	var left int
	for _, typ := range allEvents {
		evs, _ := e.db.ListNotifyEventsByType(ctx, typ)
		left += len(evs)
	}
	_ = left
}

func TestQueueCeiling(t *testing.T) { // R6.9
	e := newEnv(t, nil)
	ctx := context.Background()
	e.desktop(t, owner)
	// Put 10 003 queued deliveries in directly: 3 more than the ceiling, the oldest are Note notices.
	now := e.now.Format(store.TimeFormat)
	err := e.db.WithTx(ctx, func(tx *store.Tx) error {
		for i := 0; i < QueueCeiling+3; i++ {
			tone := ToneNote
			if i%2 == 1 {
				tone = ToneNeedsYou
			}
			id := fmt.Sprintf("n%05d", i)
			if err := tx.InsertNotice(store.Notice{ID: id, EventKey: id, Type: "update.available", SubjectKind: "pool", SubjectID: id, State: StateFiring, Tone: tone, Summary: "s", CreatedAt: now}); err != nil {
				return err
			}
			if err := tx.InsertDelivery(store.NoticeDelivery{ID: "d" + id, NoticeID: id, ChannelID: "c", ChannelKind: KindWebhook, ChannelName: "c",
				Status: store.DeliveryQueued, NextTryAt: store.FormatTime(e.now.Add(24 * time.Hour)), CreatedAt: fmt.Sprintf("2026-11-14T09:%02d:%02d.%03dZ", i/60000, i/1000%60, i%1000), UpdatedAt: now}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Tick(ctx)
	n, _ := e.db.CountQueuedDeliveries(ctx)
	// The finding that says what was dropped is itself one queued desktop notice.
	if n != QueueCeiling+1 {
		t.Fatalf("%d queued, want %d", n, QueueCeiling+1)
	}
	var finding bool
	for _, row := range e.notices(t) {
		if row.Type == "doctor.finding" && strings.Contains(row.Summary, "3 of the oldest") {
			finding = true
		}
	}
	if !finding {
		t.Fatal("a finding says how many were dropped")
	}
}

