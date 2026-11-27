package notify

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

var allEvents = []string{"machine.offline", "job.waiting", "disk.low", "runner.outdated", "doctor.finding", "update.available"}

func machineFact(id string) Fact {
	return Fact{Type: "machine.offline", SubjectKind: "machine", SubjectID: id, SubjectName: id,
		Summary: id + " stopped answering at 14:30 UTC.", Link: "/setup/machines/" + id}
}

func scopeOf(typ string) Scope { return Scope{Type: typ} }

func (e *env) desktop(t *testing.T, who Caller, events ...string) Channel {
	t.Helper()
	if len(events) == 0 {
		events = allEvents
	}
	c, err := e.svc.Create(context.Background(), who, Input{Kind: KindDesktop, Name: "Desk " + who.Login, Events: events, MinSeverity: "info"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (e *env) notices(t *testing.T) []NoticeRow {
	t.Helper()
	rows, err := e.svc.Notices(context.Background(), owner, 500)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func (e *env) advance(d time.Duration) { *e.now = e.now.Add(d) }

func TestEachEventFiresAndResolves(t *testing.T) { // R5.1, R5.2
	e := newEnv(t, nil)
	ctx := context.Background()
	e.desktop(t, owner)
	for _, typ := range allEvents {
		f := Fact{Type: typ, SubjectKind: "x", SubjectID: "s1", SubjectName: "s1", Severity: "warning"}
		if err := e.svc.Observe(ctx, scopeOf(typ), []Fact{f}); err != nil {
			t.Fatal(typ, err)
		}
		e.advance(11 * time.Minute) // past the flap window
		if err := e.svc.Observe(ctx, scopeOf(typ), nil); err != nil {
			t.Fatal(typ, err)
		}
	}
	rows := e.notices(t)
	if len(rows) != 2*len(allEvents) {
		t.Fatalf("want a firing and a resolved notice per event, got %d", len(rows))
	}
	seen := map[string]int{}
	for _, r := range rows {
		seen[r.Type+"/"+r.State]++
		if r.State == StateResolved && r.Tone != ToneDone {
			t.Errorf("resolved tone %s", r.Tone)
		}
		if len(r.Deliveries) != 1 {
			t.Errorf("%s: %d deliveries", r.Type, len(r.Deliveries))
		}
	}
	for _, typ := range allEvents {
		if seen[typ+"/firing"] != 1 || seen[typ+"/resolved"] != 1 {
			t.Errorf("%s: %v", typ, seen)
		}
	}
}

func TestNoticeOnlyOnChange(t *testing.T) { // R5.2, R5.3
	e := newEnv(t, nil)
	ctx := context.Background()
	e.desktop(t, owner)
	f := machineFact("build-02")
	for i := 0; i < 5; i++ {
		if err := e.svc.Observe(ctx, scopeOf("machine.offline"), []Fact{f}); err != nil {
			t.Fatal(err)
		}
		e.advance(time.Second)
	}
	if n := len(e.notices(t)); n != 1 {
		t.Fatalf("a repeat of the same state makes nothing: %d notices", n)
	}
	// One state per key, with the time of the last change.
	evs, _ := e.db.ListNotifyEventsByType(ctx, "machine.offline")
	if len(evs) != 1 || evs[0].State != StateFiring {
		t.Fatalf("%+v", evs)
	}
	// A key that was never firing and is absent makes nothing.
	if err := e.svc.Observe(ctx, scopeOf("disk.low"), nil); err != nil {
		t.Fatal(err)
	}
	if n := len(e.notices(t)); n != 1 {
		t.Fatalf("%d", n)
	}
}

func TestOfflineGraceSkipsRestart(t *testing.T) { // R5.5
	e := newEnv(t, nil)
	ctx := context.Background()
	e.desktop(t, owner)
	f := machineFact("build-02")
	f.Since, f.Grace = (*e.now).Add(-30*time.Second), 2*time.Minute
	if err := e.svc.Observe(ctx, scopeOf("machine.offline"), []Fact{f}); err != nil {
		t.Fatal(err)
	}
	// The agent came back inside the grace time: no notice at all.
	e.advance(40 * time.Second)
	if err := e.svc.Observe(ctx, scopeOf("machine.offline"), nil); err != nil {
		t.Fatal(err)
	}
	if n := len(e.notices(t)); n != 0 {
		t.Fatalf("a restart inside the grace time must make no notice: %d", n)
	}
	// Down longer than the grace time: fires.
	f.Since = (*e.now).Add(-3 * time.Minute)
	if err := e.svc.Observe(ctx, scopeOf("machine.offline"), []Fact{f}); err != nil {
		t.Fatal(err)
	}
	if rows := e.notices(t); len(rows) != 1 || rows[0].State != StateFiring || rows[0].Tone != ToneProblem {
		t.Fatalf("%+v", rows)
	}
	// Once firing it stays firing even if the fact's grace would not hold any more.
	f.Since = *e.now
	if err := e.svc.Observe(ctx, scopeOf("machine.offline"), []Fact{f}); err != nil {
		t.Fatal(err)
	}
	if n := len(e.notices(t)); n != 1 {
		t.Fatalf("%d", n)
	}
}

func TestDoctorSeverityFloor(t *testing.T) { // R5.4
	e := newEnv(t, nil)
	ctx := context.Background()
	warn, err := e.svc.Create(ctx, owner, Input{Kind: KindDesktop, Name: "Warn", Events: []string{"doctor.finding"}})
	if err != nil {
		t.Fatal(err)
	}
	e.desktop(t, alice, "doctor.finding") // alice: lowest severity info
	_ = warn
	mk := func(sev, id string) Fact {
		return Fact{Type: "doctor.finding", SubjectKind: "machine", SubjectID: id, SubjectName: id, Severity: sev}
	}
	if err := e.svc.Observe(ctx, scopeOf("doctor.finding"), []Fact{mk("info", "a"), mk("warning", "b"), mk("error", "c")}); err != nil {
		t.Fatal(err)
	}
	perChannel := map[string]int{}
	for _, r := range e.notices(t) {
		for _, d := range r.Deliveries {
			perChannel[d.Channel]++
		}
		if r.SubjectID == "a" && r.Tone != ToneNote || r.SubjectID == "b" && r.Tone != ToneNeedsYou || r.SubjectID == "c" && r.Tone != ToneProblem {
			t.Errorf("tone of %s is %s", r.SubjectID, r.Tone)
		}
	}
	// The default lowest severity is warning: the info finding does not reach it.
	if perChannel["Warn"] != 2 || perChannel["Desk alice"] != 3 {
		t.Fatalf("%v", perChannel)
	}
}

func TestChannelsGetOnlyTheirEvents(t *testing.T) { // R5.7, R5.9 (events), R5.6
	e := newEnv(t, nil)
	ctx := context.Background()
	e.desktop(t, owner, "machine.offline")
	off, _ := e.svc.Create(ctx, owner, Input{Kind: KindDesktop, Name: "Off", Events: allEvents, Enabled: ptr(false)})
	_ = off
	if err := e.svc.Observe(ctx, scopeOf("disk.low"), []Fact{{SubjectKind: "machine", SubjectID: "m", SubjectName: "m"}}); err != nil {
		t.Fatal(err)
	}
	rows := e.notices(t)
	if len(rows) != 1 || len(rows[0].Deliveries) != 0 {
		t.Fatalf("a channel gets only the events it carries, and a channel that is off nothing: %+v", rows)
	}
	// The notice is still kept in the list (section 8, fail open).
}

func ptr[T any](v T) *T { return &v }

func TestFlapWindow(t *testing.T) { // R5.11
	e := newEnv(t, nil)
	ctx := context.Background()
	e.desktop(t, owner)
	f := machineFact("m1")
	sc := scopeOf("machine.offline")
	fire := func() {
		if err := e.svc.Observe(ctx, sc, []Fact{f}); err != nil {
			t.Fatal(err)
		}
	}
	fire()
	e.advance(time.Minute)
	_ = e.svc.Observe(ctx, sc, nil) // resolved
	e.advance(time.Minute)
	fire() // inside the 10 min flap window: no new firing notice
	e.advance(time.Minute)
	_ = e.svc.Observe(ctx, sc, nil)
	e.advance(time.Minute)
	fire()
	rows := e.notices(t)
	firing, resolved, maxFlaps := 0, 0, 0
	for _, r := range rows {
		if r.State == StateFiring {
			firing++
			maxFlaps = max(maxFlaps, r.Flaps)
		} else {
			resolved++
		}
	}
	if firing != 1 || resolved != 2 || maxFlaps != 2 {
		t.Fatalf("firing=%d resolved=%d flaps=%d: %+v", firing, resolved, maxFlaps, rows)
	}
	// After the window a new firing makes a new notice.
	e.advance(time.Minute)
	_ = e.svc.Observe(ctx, sc, nil)
	e.advance(11 * time.Minute)
	fire()
	n := 0
	for _, r := range e.notices(t) {
		if r.State == StateFiring {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("%d firing notices", n)
	}
}

func TestHourlyCapAndSummary(t *testing.T) { // R5.12
	e := newEnv(t, func(c *Config) {
		c.Thresholds = func(context.Context) Thresholds { th := DefaultThresholds(); th.HourlyCap = 3; return th }
	})
	ctx := context.Background()
	e.desktop(t, owner, "disk.low")
	for i := 0; i < 8; i++ {
		f := Fact{Type: "disk.low", SubjectKind: "machine", SubjectID: fmt.Sprint("m", i), SubjectName: fmt.Sprint("m", i)}
		_ = e.svc.Observe(ctx, scopeOf("disk.low"), []Fact{f})
		e.advance(61 * time.Second) // spread out, so no burst group
		_ = e.svc.Observe(ctx, scopeOf("disk.low"), nil)
		e.advance(61 * time.Second)
	}
	held, queued := 0, 0
	for _, r := range e.notices(t) {
		for _, d := range r.Deliveries {
			switch d.Status {
			case store.DeliveryHeld:
				held++
			case store.DeliveryQueued:
				queued++
			}
		}
	}
	if queued != 3 || held == 0 {
		t.Fatalf("the cap lets 3 through an hour: queued=%d held=%d", queued, held)
	}
	// An hour after the oldest held notice, one summary replaces them all.
	e.advance(2 * time.Hour)
	e.svc.Tick(ctx)
	var summary *NoticeRow
	for _, r := range e.notices(t) {
		if r.Type == TypeSummary {
			r := r
			summary = &r
		}
	}
	if summary == nil || summary.Summary != heldSummary(held) || len(summary.Deliveries) != 1 {
		t.Fatalf("%+v want %q", summary, heldSummary(held))
	}
	for _, r := range e.notices(t) {
		for _, d := range r.Deliveries {
			if d.Status == store.DeliveryHeld {
				t.Fatal("a held delivery is left after the summary")
			}
		}
	}
}

func TestBurstIsGrouped(t *testing.T) { // R5.13
	e := newEnv(t, nil)
	ctx := context.Background()
	e.desktop(t, owner, "machine.offline")
	var facts []Fact
	for i := 0; i < 8; i++ {
		facts = append(facts, machineFact(fmt.Sprint("m", i)))
	}
	// Eight machines go offline one after another inside a minute.
	for i := range facts {
		_ = e.svc.Observe(ctx, scopeOf("machine.offline"), facts[:i+1])
		e.advance(2 * time.Second)
	}
	single, group, grouped := 0, 0, 0
	groupCount := 0
	for _, r := range e.notices(t) {
		for _, d := range r.Deliveries {
			switch {
			case d.Status == store.DeliveryGrouped:
				grouped++
			case d.Count > 0:
				group++
				groupCount = d.Count
			default:
				single++
			}
		}
	}
	if single != 4 || group != 1 || grouped != 3 || groupCount < BurstCount {
		t.Fatalf("single=%d group=%d grouped=%d count=%d", single, group, grouped, groupCount)
	}
	e.advance(2 * time.Minute)
	e.svc.Tick(ctx)
	got, err := e.svc.PendingDesktop(ctx, "octo", 100)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, m := range got {
		if m.Body == groupedSummary("machine.offline", StateFiring, 8) {
			found++
		}
	}
	if len(got) != 5 || found != 1 {
		t.Fatalf("four single notices and one grouped one with the whole count: %+v", got)
	}
}

func TestStateNoticeAndDeliveryTogether(t *testing.T) { // R6.4
	e := newEnv(t, nil)
	ctx := context.Background()
	e.desktop(t, owner)
	e.desktop(t, alice)
	if err := e.svc.Observe(ctx, scopeOf("machine.offline"), []Fact{machineFact("m1")}); err != nil {
		t.Fatal(err)
	}
	rows := e.notices(t)
	evs, _ := e.db.ListNotifyEventsByType(ctx, "machine.offline")
	if len(rows) != 1 || len(evs) != 1 || len(rows[0].Deliveries) != 2 || evs[0].OpenNoticeID != rows[0].ID {
		t.Fatalf("state, notice and both deliveries are written together: %+v %+v", rows, evs)
	}
}
