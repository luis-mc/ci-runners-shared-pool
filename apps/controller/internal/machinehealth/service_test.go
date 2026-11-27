package machinehealth

import (
	"context"
	"testing"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

var t0 = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newSvc(t *testing.T) (*Service, *clock, *store.Store) {
	t.Helper()
	db, err := store.OpenForTests(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	c := &clock{t0}
	return &Service{Store: db, Now: c.now, ControllerVersion: "v1.4.0", LatestRunner: func() string { return "2.330.0" }}, c, db
}

func vol(free int64, at time.Time) *agentv1.HealthReport {
	return &agentv1.HealthReport{SampledAtUnixMs: at.UnixMilli(), Volumes: []*agentv1.VolumeHealth{
		{Role: "system", Path: "/", Known: true, TotalBytes: 1000 << 30, FreeBytes: free},
		{Role: "docker", Path: "/var/lib/docker", UnknownReason: "not mounted"}}}
}

func TestIngestAndDaysUntilFull(t *testing.T) {
	ctx := context.Background()
	s, c, _ := newSvc(t)
	if v, err := s.View(ctx, "m1"); err != nil || v.Reported {
		t.Fatalf("nothing yet: %+v %v", v, err)
	}
	// Free space falls 10 GiB per hour from 100 GiB: 240 GiB per day, so 100 GiB lasts 10/24 day.
	for i := 0; i <= 120; i += 10 {
		c.t = t0.Add(time.Duration(i) * time.Minute)
		free := int64(100<<30) - int64(i)*(10<<30)/60
		if err := s.Ingest(ctx, "m1", vol(free, c.t)); err != nil {
			t.Fatal(err)
		}
	}
	v, err := s.View(ctx, "m1")
	if err != nil || !v.Reported || v.Stale || len(v.Disks) != 2 {
		t.Fatalf("%+v %v", v, err)
	}
	d := v.Disks[0]
	if !d.Known || d.DaysUntilFull == nil {
		t.Fatalf("%+v", d)
	}
	// 80 GiB left at the end, falling 240 GiB a day.
	if want := 80.0 / 240; *d.DaysUntilFull < want*0.99 || *d.DaysUntilFull > want*1.01 {
		t.Fatalf("days %v want %v", *d.DaysUntilFull, want)
	}
	if v.Disks[1].Known || v.Disks[1].UnknownReason != "not mounted" || v.Disks[1].DaysUntilFull != nil {
		t.Fatalf("unknown volume %+v", v.Disks[1])
	}
	c.t = c.t.Add(10 * time.Minute)
	if v, _ := s.View(ctx, "m1"); !v.Stale {
		t.Fatal("a report older than 3 minutes is stale")
	}
}

func TestTrendNeedsData(t *testing.T) {
	at := func(m int, free int64) store.DiskSample {
		return store.DiskSample{At: t0.Add(time.Duration(m) * time.Minute), FreeBytes: free}
	}
	for name, c := range map[string]struct {
		in   []store.DiskSample
		days bool
	}{
		"one sample": {[]store.DiskSample{at(0, 10)}, false},
		"short span": {[]store.DiskSample{at(0, 10), at(10, 5)}, false},
		"flat":       {[]store.DiskSample{at(0, 10), at(60, 10)}, false},
		"growing":    {[]store.DiskSample{at(0, 10), at(60, 20)}, false},
		"falling":    {[]store.DiskSample{at(0, 20), at(60, 10)}, true},
		"same time":  {[]store.DiskSample{at(60, 20), at(60, 10)}, false},
	} {
		got := DaysUntilFull(c.in)
		if (got.Days != nil) != c.days || (got.Days == nil && got.Note == "") {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

func TestOOMStateAndVersions(t *testing.T) {
	ctx := context.Background()
	s, c, _ := newSvc(t)
	rep := func(total int64) *agentv1.HealthReport {
		return &agentv1.HealthReport{Oom: &agentv1.OomHealth{Known: true, TotalKills: total}, Versions: &agentv1.VersionHealth{AgentVersion: "v1.3.9", RunnerAppVersion: "2.329.0"}}
	}
	_ = s.Ingest(ctx, "m1", rep(2)) // the first read is a baseline
	if v, _ := s.View(ctx, "m1"); v.OOMRecent || !v.OOMLastAt.IsZero() {
		t.Fatalf("baseline %+v", v)
	}
	c.t = c.t.Add(time.Minute)
	_ = s.Ingest(ctx, "m1", rep(3))
	v, _ := s.View(ctx, "m1")
	if !v.OOMRecent || !v.OOMLastAt.Equal(c.t) || !v.AgentOutOfDate || !v.RunnerOutOfDate || v.LatestRunner != "2.330.0" {
		t.Fatalf("%+v", v)
	}
	c.t = c.t.Add(2 * time.Hour)
	if v, _ := s.View(ctx, "m1"); v.OOMRecent {
		t.Fatal("the OOM kill is older than an hour")
	}
	th := DefaultThresholds
	th.OOMWindowMinutes = 600
	if err := s.SetThresholds(ctx, th, "test"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.View(ctx, "m1"); !v.OOMRecent {
		t.Fatal("a wider window counts it")
	}
}

func TestOutOfDate(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"v1.3.9", "v1.4.0", true}, {"1.4.0", "v1.4.0", false}, {"1.4.1-rc1", "1.4.0", false}, {"dev", "1.4.0", false}, {"1.4.0", "", false}, {"2.329.0", "2.330.0", true}, {"1.4", "1.4.1", true}} {
		if got := OutOfDate(c.a, c.b); got != c.want {
			t.Errorf("%q < %q = %v", c.a, c.b, got)
		}
	}
}

func TestRestartMayHitJobs(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newSvc(t)
	rep := &agentv1.HealthReport{Updates: &agentv1.UpdateHealth{Known: true, RestartPending: true, RestartAllowedNow: true}}
	_ = s.Ingest(ctx, "m1", rep)
	s.Jobs = func(context.Context, string) (int, bool) { return 0, false }
	if v, _ := s.View(ctx, "m1"); !v.RestartMayHappenNow || v.RestartMayHitJobs {
		t.Fatalf("drained and idle: %+v", v)
	}
	s.Jobs = func(context.Context, string) (int, bool) { return 2, false }
	if v, _ := s.View(ctx, "m1"); !v.RestartMayHitJobs {
		t.Fatalf("jobs run: %+v", v)
	}
	rep.Updates.RestartAllowedNow = false
	s.Jobs = func(context.Context, string) (int, bool) { return 2, true }
	_ = s.Ingest(ctx, "m1", rep)
	if v, _ := s.View(ctx, "m1"); v.RestartMayHappenNow || v.RestartMayHitJobs {
		t.Fatalf("inside the active hours: %+v", v)
	}
}

func TestThresholds(t *testing.T) {
	ctx := context.Background()
	s, _, db := newSvc(t)
	if got, _ := s.Thresholds(ctx); got != DefaultThresholds {
		t.Fatalf("%+v", got)
	}
	bad := DefaultThresholds
	bad.ClockSkewSeconds = 0
	if err := s.SetThresholds(ctx, bad, "x"); err == nil {
		t.Fatal("0 is out of range")
	}
	ok := DefaultThresholds
	ok.DiskDaysUnder = 7
	if err := s.SetThresholds(ctx, ok, "x"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Thresholds(ctx); got.DiskDaysUnder != 7 {
		t.Fatalf("%+v", got)
	}
	_ = db.PutRawSetting(ctx, KeyThresholds, `{"disk_days_under":-5}`, "x")
	if got, _ := s.Thresholds(ctx); got != DefaultThresholds {
		t.Fatalf("a bad row falls back: %+v", got)
	}
}

func TestOnMessageAndRemove(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newSvc(t)
	if s.OnMessage(ctx, "m1", &agentv1.SessionRequest{Message: &agentv1.SessionRequest_Heartbeat{}}) {
		t.Fatal("not a health report")
	}
	if !s.OnMessage(ctx, "m1", &agentv1.SessionRequest{Message: &agentv1.SessionRequest_HealthReport{HealthReport: vol(5, t0)}}) {
		t.Fatal("a health report")
	}
	if all, _ := s.Views(ctx); len(all) != 1 {
		t.Fatalf("%+v", all)
	}
	_ = s.Remove(ctx, "m1")
	if all, _ := s.Views(ctx); len(all) != 0 {
		t.Fatalf("%+v", all)
	}
}

func TestJobStatsWindowsAndRates(t *testing.T) {
	ctx := context.Background()
	s, c, db := newSvc(t)
	s.Slots = func(context.Context, string) int { return 2 }
	mk := func(id int64, runner string, res jobsdata.Result, ago, ran time.Duration) jobsdata.Job {
		done := c.t.Add(-ago)
		return jobsdata.Job{ID: id, RepositoryID: 1, Repository: "o/r", RunID: id, Attempt: 1, Name: "build", State: jobsdata.Done, Result: res,
			StartedAt: done.Add(-ran), CompletedAt: done, MachineID: "m1", RunnerID: runner, RunnerName: runner}
	}
	for _, j := range []jobsdata.Job{mk(1, "ra", jobsdata.Success, time.Hour, time.Hour), mk(2, "rb", jobsdata.Failure, 2*time.Hour, 30*time.Minute),
		mk(3, "ra", jobsdata.Failure, 48*time.Hour, time.Minute)} {
		if err := db.Jobs().UpsertJob(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	day, err := s.MachineJobStats(ctx, "m1", Window24h)
	if err != nil {
		t.Fatal(err)
	}
	if day.Machine.Counts.Total() != 2 || *day.Machine.Rates.Succeeded != 0.5 || *day.Machine.Rates.Failed != 0.5 || len(day.Runners) != 2 {
		t.Fatalf("%+v", day)
	}
	// 1.5 h of running in 24 h on 2 slots.
	if p := *day.Machine.BusyPct; p < 3.12 || p > 3.13 {
		t.Fatalf("busy %v", p)
	}
	week, _ := s.MachineJobStats(ctx, "m1", Window7d)
	if week.Machine.Counts.Failed != 2 {
		t.Fatalf("%+v", week.Machine.Counts)
	}
	r, _, _, err := s.RunnerJobStats(ctx, "ra", Window24h)
	if err != nil || r.Stats.Counts.Total() != 1 || *r.Stats.BusyPct < 4.1 || *r.Stats.BusyPct > 4.2 {
		t.Fatalf("%+v %v", r, err)
	}
	empty, _ := s.MachineJobStats(ctx, "nobody", Window24h)
	if empty.Machine.Rates.Succeeded != nil || empty.Machine.Counts.Total() != 0 {
		t.Fatalf("no jobs, no rates: %+v", empty.Machine)
	}
	if _, err := s.MachineJobStats(ctx, "m1", "30d"); err == nil {
		t.Fatal("bad window")
	}
}
