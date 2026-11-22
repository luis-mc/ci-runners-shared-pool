package timers

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.OpenForTests(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// R5.105: the clocks and their durations survive in the store and are read back.
func TestStoreAdapterRoundTrip(t *testing.T) {
	db := openStore(t)
	a := StoreAdapter{DB: db}
	k := jobKey(5)
	jc := JobClock{Key: k, MachineID: "m1", RunnerRef: "svc", StartedAt: t0, Limit: 30 * time.Minute, State: StateGrace,
		Deadline: t0.Add(30*time.Minute + Grace), Note: "n", Queued: time.Minute, Startup: 20 * time.Second, Hold: time.Second,
		Run: 2 * time.Minute, JobTimer: 2 * time.Minute, RunTimer: 3 * time.Minute, Extended: true}
	if err := a.SaveJob(ctx, jc); err != nil {
		t.Fatal(err)
	}
	rc := RunClock{Key: k.Run(), StartedAt: t0, Limit: 45 * time.Minute, State: StateRunning}
	if err := a.SaveRun(ctx, rc); err != nil {
		t.Fatal(err)
	}
	got, ok, err := a.LoadJob(ctx, k)
	if err != nil || !ok || got != jc {
		t.Fatalf("job %+v %v %v", got, ok, err)
	}
	gr, ok, err := a.LoadRun(ctx, k.Run())
	if err != nil || !ok || gr != rc {
		t.Fatalf("run %+v %v %v", gr, ok, err)
	}
	js, rs, err := a.Open(ctx)
	if err != nil || len(js) != 1 || len(rs) != 1 {
		t.Fatalf("open %v %v %v", js, rs, err)
	}
	jc.State = StateEnded
	_ = a.SaveJob(ctx, jc)
	if js, _, _ = a.Open(ctx); len(js) != 0 {
		t.Fatalf("an ended clock is still open: %v", js)
	}
}

// R5.104: a refused set is not saved; a saved one is read back.
func TestSettingsStore(t *testing.T) {
	s := SettingsStore{DB: openStore(t)}
	if got := s.Get(ctx); got != Defaults() {
		t.Fatalf("%+v", got)
	}
	bad := Defaults()
	bad.JobMin = 60
	if err := s.Save(ctx, bad, "luis"); err == nil {
		t.Fatal("saved a job timer above the run timer")
	}
	ok := Defaults()
	ok.JobMin, ok.RunMin = 20, 50
	if err := s.Save(ctx, ok, "luis"); err != nil {
		t.Fatal(err)
	}
	if got := s.Get(ctx); got != ok {
		t.Fatalf("%+v", got)
	}
}

type rec struct {
	actor  audit.Actor
	action string
	target audit.Target
}

type fakeRecorder struct{ got []rec }

func (f *fakeRecorder) Record(_ context.Context, a audit.Actor, action string, t audit.Target, _ map[string]any) error {
	f.got = append(f.got, rec{a, action, t})
	return nil
}

func (f *fakeRecorder) RecordRefused(context.Context, audit.Actor, string, audit.Target, map[string]any) error {
	return nil
}

// R5.102: an event becomes an audit row for the job or the run.
func TestAuditAdapter(t *testing.T) {
	f := &fakeRecorder{}
	a := AuditAdapter{R: f}
	k := jobKey(5)
	_ = a.Record(ctx, Event{Action: ActJobStop, Actor: "luis", Job: &k, Run: k.Run(), Text: "x"})
	_ = a.Record(ctx, Event{Action: ActRunCancel, Actor: ActorSystem, Run: k.Run(), Text: "y"})
	if len(f.got) != 2 || f.got[0].actor.Login != "luis" || f.got[0].target != (audit.Target{Type: "job", ID: "5"}) ||
		f.got[1].actor != audit.System || f.got[1].target.Type != "run" {
		t.Fatalf("%+v", f.got)
	}
}

type svcStop struct{ got []string }

func (s *svcStop) StopService(_ context.Context, m, svc string) error {
	s.got = append(s.got, m+"/"+svc)
	return nil
}

type canceller struct{ got []string }

func (c *canceller) CancelWorkflowRun(_ context.Context, inst int64, o, r string, id int64) error {
	c.got = append(c.got, o+"/"+r)
	_ = inst
	_ = id
	return nil
}

// R5.100: the agent stops the runner's service; GitHub cancels by owner and name.
func TestAgentAndGitHubAdapters(t *testing.T) {
	s := &svcStop{}
	if err := (AgentAdapter{S: s}).StopJobRunner(ctx, "m1", "svc-1"); err != nil || s.got[0] != "m1/svc-1" {
		t.Fatalf("%v %v", s.got, err)
	}
	if err := (AgentAdapter{S: s}).StopJobRunner(ctx, "m1", ""); err == nil {
		t.Fatal("no runner")
	}
	c := &canceller{}
	g := GitHubAdapter{C: c, Installation: func(context.Context, string) (int64, error) { return 10, nil }}
	if err := g.CancelRun(ctx, "luis-mc/app", 9); err != nil || c.got[0] != "luis-mc/app" {
		t.Fatalf("%v %v", c.got, err)
	}
	if err := g.CancelRun(ctx, "nonsense", 9); err == nil {
		t.Fatal("bad repo")
	}
}

// R5.99: the gate's first go starts the clock and the release ends it; a job
// with no GitHub job id has no clock.
func TestHookStartsAndEndsTheClock(t *testing.T) {
	r := newRig()
	h := &Hook{E: r.eng, Go: func(f func()) { f() },
		RunnerRef: func(m, n string) string { return m + ":" + n },
		LimitMin:  func(repo, job string) int { return map[string]int{"slow": 40}[job] }}
	job := scheduler.JobRef{Repository: "luis-mc/app", RunID: 900, RunAttempt: 1, Job: "slow", GitHubJobID: 5, RunnerName: "r1"}
	h.JobStarted("m1", job, 4*time.Second)
	v, ok := r.eng.Job(ctx, jobKey(5))
	if !ok || v.Limit != 40*time.Minute {
		t.Fatalf("view %+v %v", v, ok)
	}
	if c := r.store.jobs[jobKey(5)]; c.RunnerRef != "m1:r1" || c.Hold != 4*time.Second {
		t.Fatalf("stored %+v", c)
	}
	h.JobStarted("m1", scheduler.JobRef{Repository: "luis-mc/app", RunID: 900}, 0)
	if len(r.store.jobs) != 1 {
		t.Fatalf("a job with no id started a clock: %v", r.store.jobs)
	}
	r.clock.Advance(time.Minute)
	h.JobEnded("m1", job)
	h.JobEnded("m1", job) // a second release is not an error
	if v, _ = r.eng.Job(ctx, jobKey(5)); v.State != StateEnded {
		t.Fatalf("view %+v", v)
	}
}
