package insights

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// memStore is an in-memory FindingStore with the Sync semantics of the contract.
type memStore struct {
	mu     sync.Mutex
	open   map[string]Finding
	closed []string
	syncs  int
}

func newMemStore() *memStore { return &memStore{open: map[string]Finding{}} }

func key(f Finding) string { return string(f.Kind) + "|" + f.Subject }

func (m *memStore) Sync(_ context.Context, kinds []Kind, found []Finding, _ time.Time) (SyncResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.syncs++
	var r SyncResult
	seen := map[string]bool{}
	for _, f := range found {
		seen[key(f)] = true
		if _, ok := m.open[key(f)]; ok {
			r.Updated++
		} else {
			r.Opened++
		}
		m.open[key(f)] = f
	}
	for k, f := range m.open {
		for _, kind := range kinds {
			if f.Kind == kind && !seen[k] {
				delete(m.open, k)
				m.closed = append(m.closed, k)
				r.Resolved++
			}
		}
	}
	return r, nil
}
func (m *memStore) List(context.Context, Filter) ([]StoredFinding, int, string, error) {
	return nil, 0, "", nil
}
func (m *memStore) Get(context.Context, int64) (StoredFinding, error) { return StoredFinding{}, nil }
func (m *memStore) Summary(context.Context) (Summary, error)          { return Summary{}, nil }

type fakeAnalyser struct {
	name  string
	kinds []Kind
	out   []Finding
	err   error
	pan   bool
	got   []Window
}

func (a *fakeAnalyser) Name() string  { return a.name }
func (a *fakeAnalyser) Kinds() []Kind { return a.kinds }
func (a *fakeAnalyser) Analyse(_ context.Context, w Window) ([]Finding, error) {
	a.got = append(a.got, w)
	if a.pan {
		panic("boom")
	}
	return a.out, a.err
}

type runRow struct {
	ok       bool
	found    int
	resolved int
	errText  string
	ended    bool
}
type memRuns struct{ rows []*runRow }

func (r *memRuns) Begin(context.Context, string, Window, time.Time) (int64, error) {
	r.rows = append(r.rows, &runRow{})
	return int64(len(r.rows)), nil
}
func (r *memRuns) End(_ context.Context, id int64, _ time.Time, ok bool, found, resolved int, errText string) error {
	*r.rows[id-1] = runRow{ok: ok, found: found, resolved: resolved, errText: errText, ended: true}
	return nil
}

type fakeClock struct {
	now   time.Time
	after chan time.Time
	waits []time.Duration
	hook  func()
}

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.waits = append(c.waits, d)
	if c.hook != nil {
		c.hook()
	}
	return c.after
}

var t0 = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

func quiet() Option { return WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))) }

type jobsFake struct{ facts []JobFact }

func (j jobsFake) JobsBetween(_ context.Context, w Window, repo int64) ([]JobFact, error) {
	var out []JobFact
	for _, f := range j.facts {
		if !f.CreatedAt.Before(w.From) && f.CreatedAt.Before(w.To) && (repo == 0 || f.RepositoryID == repo) {
			out = append(out, f)
		}
	}
	return out, nil
}

// jobCounter is an analyser over JobSource that reports one finding per origin it sees.
type jobCounter struct{ src JobSource }

func (jobCounter) Name() string  { return "counter" }
func (jobCounter) Kinds() []Kind { return []Kind{KindWaitHold} }
func (a jobCounter) Analyse(ctx context.Context, w Window) ([]Finding, error) {
	facts, err := a.src.JobsBetween(ctx, w, 0)
	if err != nil {
		return nil, err
	}
	seen := map[Origin]bool{}
	var out []Finding
	for _, f := range facts {
		if !seen[f.Origin] {
			seen[f.Origin] = true
			out = append(out, Finding{Kind: KindWaitHold, Scope: ScopePool, Subject: string(f.Origin)})
		}
	}
	return out, nil
}

// R5.1: jobs of crsp runners, other self-hosted runners and GitHub-hosted runners all reach an analyser.
func TestSeesOtherRunnersJobs(t *testing.T) {
	src := jobsFake{facts: []JobFact{
		{ID: 1, Origin: OriginCrsp, CreatedAt: t0.Add(-time.Hour)},
		{ID: 2, Origin: OriginOther, CreatedAt: t0.Add(-time.Hour)},
		{ID: 3, Origin: OriginHosted, CreatedAt: t0.Add(-time.Hour)},
		{ID: 4, Origin: OriginCrsp, CreatedAt: t0.Add(-8 * 24 * time.Hour)}, // outside the window
	}}
	st := newMemStore()
	if err := NewEngine([]Analyser{jobCounter{src}}, st, nil, quiet()).RunOnce(context.Background(), t0); err != nil {
		t.Fatal(err)
	}
	if len(st.open) != 3 {
		t.Fatalf("want one finding per origin, got %v", st.open)
	}
}

func TestWindowIsLastSevenDays(t *testing.T) {
	a := &fakeAnalyser{name: "a", kinds: []Kind{KindWaitHold}}
	if err := NewEngine([]Analyser{a}, newMemStore(), nil, quiet()).RunOnce(context.Background(), t0); err != nil {
		t.Fatal(err)
	}
	if len(a.got) != 1 || !a.got[0].To.Equal(t0) || !a.got[0].From.Equal(t0.Add(-7*24*time.Hour)) {
		t.Fatalf("window %v", a.got)
	}
}

// R5.8: a full successful pass that no longer reports a finding resolves it.
func TestResolvedWhenConditionEnds(t *testing.T) {
	f := Finding{Kind: KindWaitHold, Scope: ScopeRepo, Subject: "ci/build"}
	a := &fakeAnalyser{name: "waits", kinds: []Kind{KindWaitHold}, out: []Finding{f}}
	st, runs := newMemStore(), &memRuns{}
	e := NewEngine([]Analyser{a}, st, runs, quiet())
	if err := e.RunOnce(context.Background(), t0); err != nil || len(st.open) != 1 {
		t.Fatalf("open: %v %v", err, st.open)
	}
	a.out = nil
	if err := e.RunOnce(context.Background(), t0.Add(Interval)); err != nil {
		t.Fatal(err)
	}
	if len(st.open) != 0 || !reflect.DeepEqual(st.closed, []string{"wait.hold|ci/build"}) {
		t.Fatalf("not resolved: %v %v", st.open, st.closed)
	}
	if r := runs.rows[1]; !r.ok || r.resolved != 1 || !r.ended {
		t.Fatalf("run row %+v", r)
	}
}

// R5.8, R6.8: an error resolves nothing, and is logged on the run row without a secret.
func TestFailedRunResolvesNothing(t *testing.T) {
	f := Finding{Kind: KindWaitHold, Scope: ScopeRepo, Subject: "ci/build"}
	a := &fakeAnalyser{name: "waits", kinds: []Kind{KindWaitHold}, out: []Finding{f}}
	st, runs := newMemStore(), &memRuns{}
	e := NewEngine([]Analyser{a}, st, runs, quiet())
	_ = e.RunOnce(context.Background(), t0)
	a.out, a.err = nil, errors.New("read failed token=ghp_"+"abcdefghijklmnopqrstuvwxyz0123456789")
	err := e.RunOnce(context.Background(), t0.Add(Interval))
	if err == nil || len(st.open) != 1 || len(st.closed) != 0 {
		t.Fatalf("err=%v open=%v closed=%v", err, st.open, st.closed)
	}
	r := runs.rows[1]
	if r.ok || r.errText == "" || strings.Contains(r.errText, "ghp_"+"abcdefghijklmnopqrstuvwxyz0123456789") {
		t.Fatalf("run row %+v", r)
	}
}

// R6.8: a failed or panicking analyser never stops the next one.
func TestFailOpen(t *testing.T) {
	bad := &fakeAnalyser{name: "bad", kinds: []Kind{KindPoolSize}, pan: true}
	bad2 := &fakeAnalyser{name: "bad2", kinds: []Kind{KindRepoSlots}, err: errors.New("x")}
	good := &fakeAnalyser{name: "good", kinds: []Kind{KindWaitHold}, out: []Finding{{Kind: KindWaitHold, Subject: "s"}}}
	st := newMemStore()
	var stats []PassStats
	e := NewEngine([]Analyser{bad, bad2, good}, st, nil, quiet(), WithPassHook(func(p PassStats) { stats = append(stats, p) }))
	if err := e.RunOnce(context.Background(), t0); err == nil {
		t.Fatal("failures must be reported")
	}
	if len(st.open) != 1 || len(stats) != 3 || stats[0].OK || stats[1].OK || !stats[2].OK {
		t.Fatalf("open=%v stats=%+v", st.open, stats)
	}
}

// A finding of a kind the analyser does not own is dropped: it could never be resolved.
func TestStrayKindDropped(t *testing.T) {
	a := &fakeAnalyser{name: "a", kinds: []Kind{KindWaitHold}, out: []Finding{{Kind: KindPoolSize, Subject: "x"}}}
	st := newMemStore()
	if err := NewEngine([]Analyser{a}, st, nil, quiet()).RunOnce(context.Background(), t0); err != nil || len(st.open) != 0 {
		t.Fatalf("%v %v", err, st.open)
	}
}

// R6.3: a pass over 30 s is flagged slow (fake clock, no waiting).
func TestSlowPassFlagged(t *testing.T) {
	c := &fakeClock{now: t0}
	a := &fakeAnalyser{name: "a", kinds: []Kind{KindWaitHold}}
	var st PassStats
	slow := &advancing{Analyser: a, c: c, by: 31 * time.Second}
	e := NewEngine([]Analyser{slow}, newMemStore(), nil, quiet(), WithClock(c), WithPassHook(func(p PassStats) { st = p }))
	_ = e.RunOnce(context.Background(), t0)
	if !st.Slow || st.Took != 31*time.Second {
		t.Fatalf("%+v", st)
	}
}

type advancing struct {
	Analyser
	c  *fakeClock
	by time.Duration
}

func (a *advancing) Analyse(ctx context.Context, w Window) ([]Finding, error) {
	a.c.now = a.c.now.Add(a.by)
	return a.Analyser.Analyse(ctx, w)
}

// R6.3: Run passes every 15 minutes and stops with its context.
func TestRunSchedule(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &fakeClock{now: t0, after: make(chan time.Time)}
	a := &fakeAnalyser{name: "a", kinds: []Kind{KindWaitHold}}
	calls := 0
	c.hook = func() {
		calls++
		c.now = c.now.Add(Interval) // time passes while the engine waits
		if calls == 3 {
			cancel() // the third wait is cancelled: three passes ran
		}
	}
	done := make(chan error)
	go func() { done <- NewEngine([]Analyser{a}, newMemStore(), nil, quiet(), WithClock(c)).Run(ctx) }()
	for i := 0; i < 2; i++ { // release the first two waits: the third is cancelled by the hook
		select {
		case c.after <- t0:
		case err := <-done:
			t.Fatalf("stopped early: %v", err)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(a.got) != 3 || c.waits[0] != Interval {
		t.Fatalf("passes=%d waits=%v", len(a.got), c.waits)
	}
}

// R5.8: the store contract has no way to dismiss or hide a finding.
func TestNoDismissRoute(t *testing.T) {
	typ := reflect.TypeOf((*FindingStore)(nil)).Elem()
	for i := 0; i < typ.NumMethod(); i++ {
		n := strings.ToLower(typ.Method(i).Name)
		for _, bad := range []string{"dismiss", "hide", "ignore", "snooze", "delete", "resolve", "close"} {
			if strings.Contains(n, bad) {
				t.Fatalf("FindingStore has %s", typ.Method(i).Name)
			}
		}
	}
}

// R6.6: an analyser sees a window and a job source; the only writer is FindingStore, which holds findings.
func TestAnalysersNeverWriteSettings(t *testing.T) {
	typ := reflect.TypeOf((*Analyser)(nil)).Elem()
	if typ.NumMethod() != 3 {
		t.Fatalf("Analyser has %d methods", typ.NumMethod())
	}
	m, _ := typ.MethodByName("Analyse")
	if m.Type.NumIn() != 2 || m.Type.NumOut() != 2 {
		t.Fatalf("Analyse signature %v", m.Type)
	}
	st := reflect.TypeOf((*FindingStore)(nil)).Elem()
	want := []string{"Get", "List", "Summary", "Sync"}
	var got []string
	for i := 0; i < st.NumMethod(); i++ {
		got = append(got, st.Method(i).Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindingStore methods %v", got)
	}
}

// R5.9: minutes are whole minutes, rounded, never negative.
func TestMinutesRoundedAndLabelled(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want int
	}{{0, 0}, {-time.Minute, 0}, {29 * time.Second, 0}, {30 * time.Second, 1}, {89 * time.Second, 1}, {90 * time.Second, 2}, {125 * time.Minute, 125}}
	for _, c := range cases {
		if got := Minutes(c.in); got != c.want {
			t.Errorf("Minutes(%v)=%d want %d", c.in, got, c.want)
		}
	}
}
