package store

import (
	"context"
	"io/fs"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

func jobsDB(t *testing.T) *JobsStore {
	t.Helper()
	s, err := OpenForTests(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s.Jobs()
}

func at(h int) time.Time {
	return time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC).Add(time.Duration(h) * time.Minute)
}

func mkJob(id int64, name string, state jobsdata.State, res jobsdata.Result, created time.Time, waitS, runS int) jobsdata.Job {
	jt, m := jobsdata.SplitName(name)
	j := jobsdata.Job{
		ID: id, RepositoryID: 1, Repository: "o/r", RunID: 100, Attempt: 1, Workflow: ".github/workflows/ci.yml",
		Name: name, JobType: jt, Matrix: m, JobKey: jobsdata.MakeKey("o/r", ".github/workflows/ci.yml", name),
		Branch: "OPS34-x", State: state, Result: res, CreatedAt: created,
	}
	if state != jobsdata.Queued {
		j.StartedAt = created.Add(time.Duration(waitS) * time.Second)
	}
	if state == jobsdata.Done {
		j.CompletedAt = j.StartedAt.Add(time.Duration(runS) * time.Second)
	}
	return j
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationJobsMigrationDownAndUp(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	sub, err := fs.Sub(migrationsFS, "migrations")
	must(t, err)
	p, err := goose.NewProvider(goose.DialectSQLite3, s.db, sub)
	must(t, err)
	if _, err := p.DownTo(ctx, 16); err != nil {
		t.Fatalf("down: %v", err)
	}
	if got := columnsOf(t, s, "jobs"); len(got) != 0 {
		t.Fatalf("jobs table after down: %v", got)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	for _, tb := range []string{"runs", "jobs", "job_steps", "job_facts_pending", "job_daily", "workflow_graphs"} {
		if len(columnsOf(t, s, tb)) == 0 {
			t.Errorf("table %s missing", tb)
		}
	}
}

func TestIntegrationUpsertJobKeepsStateAndFacts(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	j := mkJob(1, "test (ubuntu, 22)", jobsdata.Queued, "", at(0), 0, 0)
	must(t, s.UpsertJob(ctx, j))
	must(t, s.UpsertJob(ctx, j)) // idempotent
	light := true
	must(t, s.SetFacts(ctx, 1, jobsdata.Facts{MachineID: "m1", RunnerName: "r1", RunnerKind: jobsdata.Ephemeral, Light: &light, CleanupMS: 40}))

	run := mkJob(1, "test (ubuntu, 22)", jobsdata.Running, "", at(0), 5, 0)
	must(t, s.UpsertJob(ctx, run))
	done := mkJob(1, "test (ubuntu, 22)", jobsdata.Done, jobsdata.Success, at(0), 5, 60)
	must(t, s.UpsertJob(ctx, done))
	// A late "queued" event must not move the job back.
	must(t, s.UpsertJob(ctx, j))
	must(t, s.UpsertJob(ctx, run))

	d, ok, err := s.GetJob(ctx, 1)
	must(t, err)
	if !ok {
		t.Fatal("job missing")
	}
	if d.State != jobsdata.Done || d.Result != jobsdata.Success || d.CompletedAt.IsZero() || d.StartedAt.IsZero() {
		t.Errorf("job went backward: %+v", d.Job)
	}
	if d.MachineID != "m1" || d.RunnerName != "r1" || d.RunnerKind != jobsdata.Ephemeral || !d.Light || d.CleanupMS != 40 {
		t.Errorf("facts lost: %+v", d.Job)
	}
	if got := d.Matrix; len(got) != 2 || got[0] != "ubuntu" || d.JobType != "test" {
		t.Errorf("matrix %v type %q", got, d.JobType)
	}
	must(t, s.SetForecast(ctx, 1, jobsdata.ForecastRecord{LoMS: 1, MidMS: 2, HiMS: 3, Basis: "own", Samples: 7}))
	must(t, s.UpsertJob(ctx, done))
	d, _, _ = s.GetJob(ctx, 1)
	if d.Forecast == nil || d.Forecast.MidMS != 2 || d.Forecast.Samples != 7 {
		t.Errorf("forecast lost: %+v", d.Forecast)
	}
	_, total, err := s.ListJobs(ctx, jobsdata.JobFilter{})
	must(t, err)
	if total != 1 {
		t.Errorf("total %d, want 1", total)
	}
}

func TestIntegrationPendingFactsMergeOnUpsert(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	must(t, s.SetFacts(ctx, 9, jobsdata.Facts{MachineID: "m9", Backend: "docker"}))
	must(t, s.SetFacts(ctx, 9, jobsdata.Facts{RunnerName: "r9", Lost: true}))
	var n int
	must(t, s.db.QueryRowContext(ctx, `SELECT count(*) FROM job_facts_pending`).Scan(&n))
	if n != 1 {
		t.Fatalf("pending rows %d", n)
	}
	j := mkJob(9, "build", jobsdata.Queued, "", at(0), 0, 0)
	must(t, s.UpsertJob(ctx, j))
	d, ok, err := s.GetJob(ctx, 9)
	must(t, err)
	if !ok || d.MachineID != "m9" || d.Backend != "docker" || d.RunnerName != "r9" || d.Result != jobsdata.Lost {
		t.Errorf("pending facts not merged: %+v", d.Job)
	}
	must(t, s.db.QueryRowContext(ctx, `SELECT count(*) FROM job_facts_pending`).Scan(&n))
	if n != 0 {
		t.Errorf("pending facts stayed: %d", n)
	}
}

func TestIntegrationUpsertRunNeverReopens(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	r := jobsdata.Run{RepositoryID: 1, Repository: "o/r", RunID: 100, Attempt: 1, Workflow: "w.yml", Branch: "b", HeadSHA: "abc", CreatedAt: at(0), StartedAt: at(1)}
	must(t, s.UpsertRun(ctx, r))
	fin := r
	fin.Conclusion, fin.CompletedAt = jobsdata.Success, at(9)
	must(t, s.UpsertRun(ctx, fin))
	must(t, s.UpsertRun(ctx, r))
	rows, total, err := s.ListRuns(ctx, jobsdata.RunFilter{})
	must(t, err)
	if total != 1 || len(rows) != 1 || rows[0].Conclusion != jobsdata.Success || rows[0].State != jobsdata.Done || rows[0].CompletedAt.IsZero() {
		t.Fatalf("run reopened: %+v", rows)
	}
	if rows[0].TotalMS != 9*60*1000 {
		t.Errorf("total ms %d", rows[0].TotalMS)
	}
}

func TestIntegrationListJobsFiltersAndPaging(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	for i := int64(1); i <= 5; i++ {
		j := mkJob(i, "test", jobsdata.Done, jobsdata.Success, at(int(i)), 5, 10)
		if i%2 == 0 {
			j.RunnerName, j.Branch = "rA", "other"
		}
		if i == 5 {
			j.State, j.Result, j.CompletedAt = jobsdata.Running, "", time.Time{}
		}
		must(t, s.UpsertJob(ctx, j))
	}
	rows, total, err := s.ListJobs(ctx, jobsdata.JobFilter{Limit: 2})
	must(t, err)
	if total != 5 || len(rows) != 3 || rows[0].ID != 5 || rows[2].ID != 3 {
		t.Fatalf("page 1: total %d ids %v", total, ids(rows))
	}
	rows, total, _ = s.ListJobs(ctx, jobsdata.JobFilter{Limit: 2, After: 3})
	if total != 5 || len(rows) != 2 || rows[0].ID != 2 {
		t.Fatalf("page 2: total %d ids %v", total, ids(rows))
	}
	rows, total, _ = s.ListJobs(ctx, jobsdata.JobFilter{RunnerName: "rA", Branch: "other"})
	if total != 2 || len(rows) != 2 {
		t.Errorf("runner filter: %d %v", total, ids(rows))
	}
	rows, total, _ = s.ListJobs(ctx, jobsdata.JobFilter{State: jobsdata.Running})
	if total != 1 || rows[0].ID != 5 {
		t.Errorf("state filter: %d %v", total, ids(rows))
	}
	_, total, _ = s.ListJobs(ctx, jobsdata.JobFilter{From: at(3), To: at(4)})
	if total != 2 {
		t.Errorf("time filter: %d", total)
	}
	open, err := s.OpenJobs(ctx)
	must(t, err)
	if len(open) != 1 || open[0].ID != 5 {
		t.Errorf("open jobs %v", ids(open))
	}
}

func ids(js []jobsdata.Job) []int64 {
	var o []int64
	for _, j := range js {
		o = append(o, j.ID)
	}
	return o
}

func TestIntegrationRunsAttemptsCountsAndPaging(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	for run := int64(100); run < 103; run++ {
		must(t, s.UpsertRun(ctx, jobsdata.Run{RepositoryID: 1, Repository: "o/r", RunID: run, Attempt: 1, Workflow: "w.yml", HeadSHA: "abc", Branch: "b", CreatedAt: at(int(run))}))
	}
	// run 100 has a second attempt
	must(t, s.UpsertRun(ctx, jobsdata.Run{RepositoryID: 1, Repository: "o/r", RunID: 100, Attempt: 2, Workflow: "w.yml", HeadSHA: "abc", Branch: "b", CreatedAt: at(100)}))
	a := mkJob(1, "build", jobsdata.Done, jobsdata.Failure, at(100), 1, 2)
	b := mkJob(2, "test", jobsdata.Running, "", at(100), 1, 0)
	c := mkJob(3, "build", jobsdata.Done, jobsdata.Success, at(100), 1, 2)
	c.Attempt = 2
	for _, j := range []jobsdata.Job{a, b, c} {
		must(t, s.UpsertJob(ctx, j))
	}
	must(t, s.PutGraph(ctx, jobsdata.Graph{RepositoryID: 1, Workflow: "w.yml", HeadSHA: "abc", OK: true, Needs: map[string][]string{"test": {"build"}}, Dynamic: map[string]bool{"test": true}}))

	d, ok, err := s.GetRun(ctx, 100, 0)
	must(t, err)
	if !ok || d.Attempt != 2 || len(d.Attempts) != 2 || len(d.Jobs) != 1 || d.JobsTotal != 1 || d.JobsDone != 1 || d.State != jobsdata.Running {
		t.Fatalf("latest attempt: %+v", d)
	}
	if !d.Graph.OK || d.Graph.Needs["test"][0] != "build" || !d.Graph.Dynamic["test"] {
		t.Errorf("graph %+v", d.Graph)
	}
	d, _, _ = s.GetRun(ctx, 100, 1)
	if d.JobsTotal != 2 || d.JobsDone != 1 || d.JobsFailed != 1 || d.State != jobsdata.Running || len(d.Jobs) != 2 {
		t.Fatalf("attempt 1: %+v", d.RunRow)
	}
	if _, ok, _ := s.GetRun(ctx, 999, 0); ok {
		t.Error("unknown run found")
	}
	det, _, _ := s.GetJob(ctx, 1)
	if len(det.Attempts) != 2 || det.Attempts[1].JobID != 3 || det.Needs != nil {
		t.Errorf("job attempts %+v", det.Attempts)
	}
	if g, ok, _ := s.GetRun(ctx, 101, 0); !ok || g.State != jobsdata.Queued || len(g.Jobs) != 0 {
		t.Errorf("run 101: %+v", g.State)
	}

	// Paging: 4 attempts, newest first, by created_at then run id.
	page1, total, err := s.ListRuns(ctx, jobsdata.RunFilter{Limit: 2})
	must(t, err)
	if total != 4 || len(page1) != 3 {
		t.Fatalf("page 1: total %d len %d", total, len(page1))
	}
	if page1[0].RunID != 102 || page1[1].RunID != 101 || page1[2].RunID != 100 || page1[2].Attempt != 2 {
		t.Errorf("order %d/%d %d/%d", page1[0].RunID, page1[0].Attempt, page1[1].RunID, page1[1].Attempt)
	}
	page2, _, _ := s.ListRuns(ctx, jobsdata.RunFilter{Limit: 2, After: page1[1].SortKey})
	if len(page2) != 2 || page2[0].RunID != 100 || page2[0].Attempt != 2 || page2[1].Attempt != 1 {
		t.Fatalf("page 2: %+v", page2)
	}
	if d2, _, _ := s.GetRun(ctx, 100, 2); d2.SortKey != page1[2].SortKey {
		t.Errorf("sort key of GetRun %d, list %d", d2.SortKey, page1[2].SortKey)
	}
	if rows, total, _ := s.ListRuns(ctx, jobsdata.RunFilter{State: jobsdata.Queued}); total != 2 || len(rows) != 2 {
		t.Errorf("queued runs: %d", total)
	}
}

func TestIntegrationHistoryAndSamples(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	for i := 1; i <= 6; i++ {
		res := jobsdata.Success
		if i == 4 {
			res = jobsdata.Failure
		}
		j := mkJob(int64(i), "test", jobsdata.Done, res, now.Add(-time.Duration(7-i)*time.Hour), 5, 10*i)
		if i == 6 {
			j.Name, j.JobType, j.JobKey = "other", "other", "o/r / w / other"
		}
		must(t, s.UpsertJob(ctx, j))
	}
	old := mkJob(7, "test", jobsdata.Done, jobsdata.Success, now.Add(-100*24*time.Hour), 5, 10)
	must(t, s.UpsertJob(ctx, old))
	key := jobsdata.MakeKey("o/r", ".github/workflows/ci.yml", "test")
	h, err := s.History(ctx, 1, key, 3)
	must(t, err)
	if len(h) != 3 || h[2].Seconds != 50 || h[1].Result != jobsdata.Failure || !h[0].At.Before(h[2].At) {
		t.Errorf("history %+v", h)
	}
	smp, err := s.Samples(ctx, jobsdata.SampleQuery{RepositoryID: 1, JobKey: key, Since: now.Add(-30 * 24 * time.Hour), Limit: 10})
	must(t, err)
	if len(smp) != 4 || smp[0].JobID != 5 || smp[0].Seconds != 50 || smp[3].JobID != 1 {
		t.Errorf("samples by key: %+v", smp)
	}
	smp, _ = s.Samples(ctx, jobsdata.SampleQuery{RepositoryID: 1, JobKey: key, Limit: 2})
	if len(smp) != 2 {
		t.Errorf("limit: %d", len(smp))
	}
	smp, _ = s.Samples(ctx, jobsdata.SampleQuery{Workflow: ".github/workflows/ci.yml", JobType: "test", Since: now.Add(-30 * 24 * time.Hour)})
	if len(smp) != 4 {
		t.Errorf("by type: %d", len(smp))
	}
	smp, _ = s.Samples(ctx, jobsdata.SampleQuery{RepositoryID: 1, Since: now.Add(-30 * 24 * time.Hour)})
	if len(smp) != 5 {
		t.Errorf("by repository: %d", len(smp))
	}
	must(t, s.ReplaceSteps(ctx, 5, []jobsdata.Step{
		{Number: 1, Name: "checkout", State: "done", Result: "success", StartedAt: now.Add(-3 * time.Hour), CompletedAt: now.Add(-3*time.Hour + 4*time.Second)},
		{Number: 2, Name: "build", State: "done", Result: "success", StartedAt: now.Add(-3 * time.Hour), CompletedAt: now.Add(-3*time.Hour + 9*time.Second)},
	}))
	must(t, s.ReplaceSteps(ctx, 5, []jobsdata.Step{
		{Number: 1, Name: "checkout", State: "done", Result: "success", StartedAt: now.Add(-3 * time.Hour), CompletedAt: now.Add(-3*time.Hour + 4*time.Second)},
		{Number: 2, Name: "build", State: "done", Result: "success", StartedAt: now.Add(-3 * time.Hour), CompletedAt: now.Add(-3*time.Hour + 9*time.Second)},
	}))
	smp, _ = s.Samples(ctx, jobsdata.SampleQuery{RepositoryID: 1, JobKey: key, StepName: "build"})
	if len(smp) != 1 || smp[0].Seconds != 9 || smp[0].StepName != "build" {
		t.Errorf("step samples: %+v", smp)
	}
	d, _, _ := s.GetJob(ctx, 5)
	if len(d.Steps) != 2 || d.Steps[1].Name != "build" {
		t.Errorf("steps %+v", d.Steps)
	}
}

func TestIntegrationGroupsAndStats(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	// A job made "an hour ago" must still be on today's date, so in the first hour of the UTC day it is made now.
	back := time.Hour
	if now.Hour() == 0 {
		back = 0
	}
	mk := func(id int64, name, runner, kind string, state jobsdata.State, res jobsdata.Result, wait, run int, daysAgo int) {
		j := mkJob(id, name, state, res, now.Add(-time.Duration(daysAgo)*24*time.Hour-back), wait, run)
		j.RunnerName, j.RunnerKind, j.Backend, j.MachineID, j.MachineName = runner, jobsdata.RunnerKind(kind), "docker", "m1", "Mac"
		must(t, s.UpsertJob(ctx, j))
	}
	mk(1, "test", "rA", "persistent", jobsdata.Done, jobsdata.Success, 10, 100, 0)
	mk(2, "test", "rA", "persistent", jobsdata.Done, jobsdata.Failure, 20, 200, 1)
	mk(3, "test", "rB", "ephemeral", jobsdata.Done, jobsdata.Success, 30, 300, 1)
	mk(4, "build", "rB", "ephemeral", jobsdata.Running, "", 5, 0, 0)
	mk(5, "build", "rB", "ephemeral", jobsdata.Queued, "", 0, 0, 0)
	mk(6, "build", "rB", "ephemeral", jobsdata.Done, jobsdata.Success, 1, 1, 120) // out of the 90 days window
	must(t, s.SetForecast(ctx, 1, jobsdata.ForecastRecord{LoMS: 80000, MidMS: 90000, HiMS: 130000, Basis: "own", Samples: 5}))

	g, err := s.Groups(ctx, jobsdata.ByRunner)
	must(t, err)
	if len(g) != 2 || g[0].Name != "rA" || g[0].Done != 2 || g[0].Failed != 1 || g[0].Filter != "runner=rA" || g[0].RunP50 != 100 || g[0].RunP90 != 200 || g[0].WaitP50 != 10 {
		t.Errorf("by runner %+v", g)
	}
	if g[1].Name != "rB" || g[1].Running != 1 || g[1].Queued != 1 || g[1].Done != 1 || len(g[1].Trend) != 1 || g[1].Trend[0] != 300 {
		t.Errorf("rB %+v", g[1])
	}
	g, _ = s.Groups(ctx, jobsdata.ByRunnerType)
	if len(g) != 2 || g[0].Name != "ephemeral/docker" || g[0].Filter != "runner_kind=ephemeral&backend=docker" {
		t.Errorf("by runner type %+v", g)
	}
	g, _ = s.Groups(ctx, jobsdata.ByJobType)
	if len(g) != 2 || g[0].Name != "build" || g[0].Light == nil || g[1].Done != 3 {
		t.Errorf("by job type %+v", g)
	}
	for _, by := range []jobsdata.GroupBy{jobsdata.ByRepository, jobsdata.ByMachine} {
		if g, err = s.Groups(ctx, by); err != nil || len(g) != 1 {
			t.Errorf("%s: %v %+v", by, err, g)
		}
	}
	if _, err := s.Groups(ctx, "nope"); err == nil {
		t.Error("unknown grouping accepted")
	}

	st, err := s.Stats(ctx, 3, jobsdata.JobFilter{})
	must(t, err)
	if len(st.Days) != 3 || st.Days[2].Day != now.Format("2006-01-02") {
		t.Fatalf("days %+v", st.Days)
	}
	if st.Days[2].Jobs+st.Days[1].Jobs != 3 || st.Days[0].Jobs != 0 {
		t.Errorf("day counts %+v", st.Days)
	}
	if len(st.ForecastPoints) != 1 || st.ForecastPoints[0].ForecastS != 90 || st.ForecastPoints[0].ActualS != 100 {
		t.Errorf("forecast points %+v", st.ForecastPoints)
	}
	n := 0
	for _, h := range st.Hours {
		n += h.Count
	}
	if n != 5 {
		t.Errorf("hour counts %d", n)
	}
	st, _ = s.Stats(ctx, 3, jobsdata.JobFilter{RunnerName: "rA"})
	if len(st.ForecastPoints) != 1 || st.Days[1].Failed+st.Days[2].Failed != 1 {
		t.Errorf("filtered stats %+v", st.Days)
	}
}

func TestIntegrationPruneBoundaries(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	// The fake clock is two days ahead of the real one, so the facts parked just now are past their one day.
	now := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	ret := jobsdata.DefaultRetention
	mk := func(id int64, ago time.Duration) {
		j := mkJob(id, "test", jobsdata.Done, jobsdata.Success, now.Add(-ago-time.Minute), 5, 10)
		j.RunID = 1000 + id
		j.StartedAt, j.CompletedAt = now.Add(-ago-10*time.Second), now.Add(-ago)
		must(t, s.UpsertRun(ctx, jobsdata.Run{RepositoryID: 1, Repository: "o/r", RunID: j.RunID, Attempt: 1, CreatedAt: j.CreatedAt}))
		must(t, s.UpsertJob(ctx, j))
		must(t, s.ReplaceSteps(ctx, id, []jobsdata.Step{{Number: 1, Name: "a", State: "done", Result: "success", StartedAt: j.StartedAt, CompletedAt: j.CompletedAt}}))
	}
	mk(1, ret.Steps-time.Hour) // steps and job kept
	mk(2, ret.Steps+time.Hour) // steps gone, job kept
	mk(3, ret.Jobs-time.Hour)  // job kept
	mk(4, ret.Jobs+time.Hour)  // job and its steps gone, summary rolled first
	must(t, s.SetFacts(ctx, 77, jobsdata.Facts{MachineID: "x"}))
	day := func(d time.Duration) string { return now.Add(d).Format("2006-01-02") }
	for _, d := range []string{day(-ret.Summaries - 24*time.Hour), day(-ret.Summaries + 24*time.Hour)} {
		must(t, s.q.UpsertDailySummary(ctx, sqlcgen.UpsertDailySummaryParams{Day: d, Repository: "o/r", JobType: "old", Jobs: 1}))
	}
	must(t, s.PutGraph(ctx, jobsdata.Graph{RepositoryID: 1, Workflow: "w", HeadSHA: "old", OK: true, FetchedAt: now.Add(-ret.Jobs - time.Hour)}))
	must(t, s.PutGraph(ctx, jobsdata.Graph{RepositoryID: 1, Workflow: "w", HeadSHA: "new", OK: true, FetchedAt: now.Add(-time.Hour)}))

	p, err := s.Prune(ctx, now, ret)
	must(t, err)
	if p.Jobs != 1 || p.Steps != 3 || p.Runs != 1 || p.Summaries != 1 || p.Graphs != 1 {
		t.Errorf("pruned %+v", p)
	}
	var steps, jobs, runs, pending, graphs int
	must(t, s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM job_steps), (SELECT count(*) FROM jobs), (SELECT count(*) FROM runs),
		(SELECT count(*) FROM job_facts_pending), (SELECT count(*) FROM workflow_graphs)`).Scan(&steps, &jobs, &runs, &pending, &graphs))
	if steps != 1 || jobs != 3 || runs != 3 || pending != 0 || graphs != 1 {
		t.Errorf("left: steps %d jobs %d runs %d pending %d graphs %d", steps, jobs, runs, pending, graphs)
	}
	sum, err := s.Summaries(ctx, now.Add(-500*24*time.Hour), now)
	must(t, err)
	var old, rolled int
	for _, r := range sum {
		switch r.JobType {
		case "old":
			old++
		case "test":
			rolled += r.Jobs
			if r.RunP50MS != 10000 || r.WaitP50MS != 50000 {
				t.Errorf("rolled summary %+v", r)
			}
		}
	}
	if old != 1 || rolled != 4 {
		t.Errorf("summaries: old %d rolled %d (%+v)", old, rolled, sum)
	}
	// Idempotent: a second prune changes nothing.
	p, err = s.Prune(ctx, now, ret)
	must(t, err)
	if p != (jobsdata.Pruned{}) {
		t.Errorf("second prune %+v", p)
	}
	if sum2, _ := s.Summaries(ctx, now.Add(-500*24*time.Hour), now); len(sum2) != len(sum) {
		t.Errorf("summaries changed on second prune")
	}
}
