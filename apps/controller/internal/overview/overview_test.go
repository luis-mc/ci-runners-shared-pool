package overview

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
)

type src struct {
	machines []gen.Machine
	queued   []gen.Job
	running  []gen.Job
	saved    []OSMinutes
	since    time.Time
	err      error
}

func (s *src) Machines(context.Context) ([]gen.Machine, error) { return s.machines, s.err }
func (s *src) QueuedJobs(context.Context) ([]gen.Job, error)   { return s.queued, nil }
func (s *src) RunningJobs(context.Context) ([]gen.Job, error)  { return s.running, nil }
func (s *src) MinutesSavedSince(_ context.Context, t time.Time) ([]OSMinutes, error) {
	s.since = t
	return s.saved, nil
}

type last uint64

func (l last) LastID() uint64 { return uint64(l) }

func TestRoundUpMinutes(t *testing.T) {
	cases := map[time.Duration]int{
		0: 0, -time.Second: 0, time.Second: 1, 59 * time.Second: 1, time.Minute: 1,
		61 * time.Second: 2, 10*time.Minute + time.Millisecond: 11,
	}
	for d, want := range cases {
		if got := RoundUpMinutes(d); got != want {
			t.Errorf("RoundUpMinutes(%v) = %d, want %d", d, got, want)
		}
	}
}

func TestStartOfDayUsesLocalZone(t *testing.T) {
	loc := time.FixedZone("x", 2*3600)
	now := time.Date(2026, 11, 14, 23, 30, 0, 0, time.UTC) // 01:30 next day in x
	got := StartOfDay(now, loc)
	if want := time.Date(2026, 11, 15, 0, 0, 0, 0, loc); !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestGetBuildsOverview(t *testing.T) {
	loc := time.FixedZone("x", 0)
	s := &src{
		machines: []gen.Machine{{Id: "m2", Name: "zed"}, {Id: "m1", Name: "alpha"}, {Id: "mc", Name: "ctl", IsController: true}},
		queued:   []gen.Job{{Id: 1, JobName: "build"}},
		saved:    []OSMinutes{{"windows", 12}, {"linux", 30}, {"", 5}, {"macos", 0}},
	}
	svc := &Service{Source: s, Events: last(42), Now: func() time.Time { return time.Date(2026, 11, 14, 15, 0, 0, 0, loc) }, Loc: loc}
	o, err := svc.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if o.LastEventId != "42" || o.QueuedCount != 1 || len(o.Running) != 0 {
		t.Fatalf("overview = %+v", o)
	}
	if o.Machines[0].Id != "mc" || o.Machines[1].Id != "m1" || o.Machines[2].Id != "m2" {
		t.Fatalf("machine order = %v", o.Machines)
	}
	if !s.since.Equal(time.Date(2026, 11, 14, 0, 0, 0, 0, loc)) {
		t.Fatalf("since = %v", s.since)
	}
	m := *o.MinutesSavedToday
	if len(m) != 2 || m[0].Os != gen.OSLinux || m[0].Minutes != 30 || m[1].Os != gen.OSWindows {
		t.Fatalf("minutes = %+v", m)
	}
}

func TestQueueListIsCappedButCountIsExact(t *testing.T) {
	jobs := make([]gen.Job, MaxQueuedListed+30)
	svc := &Service{Source: &src{queued: jobs}}
	o, _ := svc.Get(context.Background())
	if o.QueuedCount != MaxQueuedListed+30 || len(*o.Queued) != MaxQueuedListed {
		t.Fatalf("count %d listed %d", o.QueuedCount, len(*o.Queued))
	}
}

func TestEmptyOverviewHasArraysNotNull(t *testing.T) {
	svc := &Service{Source: &src{}}
	b, err := svc.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"machines", "running", "queued"} {
		if string(m[k]) != "[]" {
			t.Errorf("%s = %s", k, m[k])
		}
	}
}

func TestSourceErrorIsReturned(t *testing.T) {
	svc := &Service{Source: &src{err: errors.New("db down")}}
	if _, err := svc.Get(context.Background()); err == nil {
		t.Fatal("want error")
	}
}

// R5.48: each queued job gets its first reason in words, from the gate's own text.
func TestWaitingListHasWords(t *testing.T) {
	over, slot, none := gen.WaitReasonLoadOverBudget, gen.WaitReasonFreeSlot, "memory 91% > 85%"
	ahead := "2 ahead in the queue"
	svc := &Service{Source: &src{queued: []gen.Job{
		{Id: 1, JobName: "a", WaitReason: &over, WaitText: &none},
		{Id: 2, JobName: "b", WaitReason: &slot, WaitText: &ahead},
		{Id: 3, JobName: "c"},
	}}}
	o, err := svc.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Over the load budget: memory 91% > 85%.", "No free slot: 2 ahead in the queue.", "Waiting. The reason is not known yet."}
	if o.Waiting == nil || len(*o.Waiting) != 3 {
		t.Fatalf("waiting = %+v", o.Waiting)
	}
	for i, w := range *o.Waiting {
		if w.Words != want[i] {
			t.Errorf("%d: %q want %q", i, w.Words, want[i])
		}
	}
}

type pagedSrc struct {
	src
	total, gotLimit int
}

func (p *pagedSrc) QueuedJobsPage(_ context.Context, limit int) ([]gen.Job, int, error) {
	p.gotLimit = limit
	return p.queued, p.total, nil
}

// The source caps and counts in SQL: the snapshot trusts its total and never loads the whole queue.
func TestGetUsesQueuePagerTotal(t *testing.T) {
	p := &pagedSrc{src: src{queued: make([]gen.Job, 3)}, total: 5000}
	o, err := (&Service{Source: p, Events: last(1)}).Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if o.QueuedCount != 5000 || len(*o.Queued) != 3 || p.gotLimit != MaxQueuedListed {
		t.Fatalf("count %d listed %d limit %d", o.QueuedCount, len(*o.Queued), p.gotLimit)
	}
}
