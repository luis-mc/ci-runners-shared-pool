package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsdata"
)

var statsNow = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

func statJob(id int64, machine, runner string, res jobsdata.Result, doneAgo, ranFor, waited time.Duration) jobsdata.Job {
	done := statsNow.Add(-doneAgo)
	started := done.Add(-ranFor)
	return jobsdata.Job{ID: id, RepositoryID: 1, Repository: "o/r", RunID: id, Attempt: 1, Name: "test", JobType: "test", JobKey: "o/r/ci/test",
		State: jobsdata.Done, Result: res, HTMLURL: "https://gh/job/" + string(rune('a'+id)), CreatedAt: started.Add(-waited), QueuedAt: started.Add(-waited),
		StartedAt: started, CompletedAt: done, MachineID: machine, RunnerID: runner, RunnerName: "r-" + runner}
}

func TestJobStatsCountsAveragesAndFailures(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	js := s.Jobs()
	h := time.Hour
	for _, j := range []jobsdata.Job{
		statJob(1, "m1", "ra", jobsdata.Success, h, 10*time.Minute, 2*time.Minute),
		statJob(2, "m1", "ra", jobsdata.Success, 2*h, 20*time.Minute, 4*time.Minute),
		statJob(3, "m1", "rb", jobsdata.Failure, 3*h, 5*time.Minute, time.Minute),
		statJob(4, "m1", "rb", jobsdata.Cancelled, 4*h, time.Minute, time.Minute),
		statJob(5, "m1", "ra", jobsdata.Failure, 5*h, time.Minute, time.Minute),
		statJob(6, "m1", "ra", jobsdata.TimedOut, 6*h, time.Minute, time.Minute),
		statJob(7, "m1", "ra", jobsdata.Failure, 30*h, time.Minute, time.Minute), // out of 24 h, in 7 d
		statJob(8, "m2", "rc", jobsdata.Success, h, time.Minute, time.Minute),
		statJob(9, "m1", "ra", jobsdata.Success, 7*h, time.Minute, time.Minute), // ended by crsp below
		statJob(10, "m1", "ra", jobsdata.Failure, 8*h, time.Minute, time.Minute),
		statJob(11, "m1", "ra", jobsdata.Cancelled, 9*h, time.Minute, time.Minute),
	} {
		if err := js.UpsertJob(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	for id, why := range map[int64]string{9: JobEndOverMemory, 10: JobEndTimeout, 11: JobEndStopped} {
		if err := s.RecordJobEndReason(ctx, id, why, "", statsNow); err != nil {
			t.Fatal(err)
		}
	}
	day, err := s.JobStats(ctx, JobStatsScope{MachineID: "m1"}, statsNow.Add(-24*h), statsNow)
	if err != nil {
		t.Fatal(err)
	}
	c := day.Counts
	if c.Succeeded != 2 || c.Failed != 2 || c.Cancelled != 1 || c.TimedOut != 2 || c.OverMemory != 1 || c.Stopped != 1 || c.Total() != 9 {
		t.Fatalf("counts %+v", c)
	}
	if day.LastFailure == nil || day.LastFailure.JobID != 3 || day.LastFailure.JobURL == "" || day.LastFailure.Class != "failed" {
		t.Fatalf("last failure %+v", day.LastFailure)
	}
	if len(day.TopFailing) != 1 || day.TopFailing[0].Failures != 6 {
		t.Fatalf("top %+v", day.TopFailing)
	}
	week, _ := s.JobStats(ctx, JobStatsScope{MachineID: "m1"}, statsNow.Add(-7*24*h), statsNow)
	if week.Counts.Failed != 3 {
		t.Fatalf("week %+v", week.Counts)
	}
}

func TestJobStatsAveragesRunnerAndEmpty(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	h := time.Hour
	for _, j := range []jobsdata.Job{
		statJob(1, "m1", "ra", jobsdata.Success, h, 10*time.Minute, 2*time.Minute),
		statJob(2, "m1", "ra", jobsdata.Success, 2*h, 20*time.Minute, 4*time.Minute),
		statJob(3, "m1", "rb", jobsdata.Success, 2*h, time.Minute, time.Minute),
	} {
		if err := s.Jobs().UpsertJob(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	st, err := s.JobStats(ctx, JobStatsScope{RunnerID: "ra"}, statsNow.Add(-24*h), statsNow)
	if err != nil {
		t.Fatal(err)
	}
	if st.Counts.Total() != 2 || !st.AvgRunKnown || st.AvgRunSeconds != 900 || !st.AvgQueueWaitKnown || st.AvgQueueWaitSeconds != 180 {
		t.Fatalf("%+v", st)
	}
	if st.LastFailure != nil || len(st.TopFailing) != 0 {
		t.Fatalf("no failures: %+v", st)
	}
	none, err := s.JobStats(ctx, JobStatsScope{MachineID: "nobody"}, statsNow.Add(-24*h), statsNow)
	if err != nil || none.AvgRunKnown || none.AvgQueueWaitKnown || none.Counts.Total() != 0 || none.BusySeconds != 0 {
		t.Fatalf("%+v %v", none, err)
	}
	rs, err := s.MachineRunnersWithJobs(ctx, "m1", statsNow.Add(-24*h), statsNow)
	if err != nil || len(rs) != 2 || rs[0].ID != "ra" {
		t.Fatalf("%+v %v", rs, err)
	}
}

func TestJobStatsBusyClipsToWindow(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	h := time.Hour
	// Ran 3 h ending 23 h ago: 2 h of it is inside a 24 h window... the window starts 24 h ago, so the whole 3 h minus 2 h before it.
	if err := s.Jobs().UpsertJob(ctx, statJob(1, "m1", "ra", jobsdata.Success, 23*h, 3*h, 0)); err != nil {
		t.Fatal(err)
	}
	// Ran 1 h, entirely inside.
	if err := s.Jobs().UpsertJob(ctx, statJob(2, "m1", "ra", jobsdata.Success, 5*h, h, 0)); err != nil {
		t.Fatal(err)
	}
	// Still running: started 30 min ago, no end.
	run := statJob(3, "m1", "rb", jobsdata.Success, 0, 0, 0)
	run.State, run.Result, run.CompletedAt, run.StartedAt = jobsdata.Running, "", time.Time{}, statsNow.Add(-30*time.Minute)
	if err := s.Jobs().UpsertJob(ctx, run); err != nil {
		t.Fatal(err)
	}
	st, err := s.JobStats(ctx, JobStatsScope{MachineID: "m1"}, statsNow.Add(-24*h), statsNow)
	if err != nil {
		t.Fatal(err)
	}
	// The first job started 26 h ago and ended 23 h ago, so 1 h of it is inside the window.
	want := (1*h + h + 30*time.Minute).Seconds()
	if d := st.BusySeconds - want; d > 1 || d < -1 {
		t.Fatalf("busy %v want %v", st.BusySeconds, want)
	}
}

func TestMachineHealthRowsAndDiskSamples(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	if _, ok, err := s.GetMachineHealth(ctx, "m1"); ok || err != nil {
		t.Fatalf("empty: %v %v", ok, err)
	}
	for i, rep := range []string{`{"a":1}`, `{"a":2}`} {
		if err := s.PutMachineHealth(ctx, MachineHealthRow{MachineID: "m1", ReceivedAt: statsNow.Add(time.Duration(i) * time.Minute), Report: rep, OOMTotal: int64(i), LastOOMAt: statsNow}); err != nil {
			t.Fatal(err)
		}
	}
	r, ok, err := s.GetMachineHealth(ctx, "m1")
	if err != nil || !ok || r.Report != `{"a":2}` || r.OOMTotal != 1 || !r.LastOOMAt.Equal(statsNow) {
		t.Fatalf("%+v %v %v", r, ok, err)
	}
	all, _ := s.ListMachineHealth(ctx)
	if len(all) != 1 {
		t.Fatalf("%+v", all)
	}
	old, fresh := statsNow.Add(-25*time.Hour), statsNow.Add(-time.Hour)
	if err := s.AddDiskSamples(ctx, "m1", []DiskSample{{"system", "/", old, 50, 100}, {"system", "/", fresh, 40, 100}, {"data_dir", "/d", fresh, 9, 10}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.DiskSamples(ctx, "m1", statsNow.Add(-24*time.Hour))
	if err != nil || len(got) != 2 || got[0].Role != "data_dir" {
		t.Fatalf("%+v %v", got, err)
	}
	n, err := s.PruneDiskSamples(ctx, statsNow.Add(-24*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("pruned %d %v", n, err)
	}
	if err := s.DeleteMachineHealth(ctx, "m1"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.GetMachineHealth(ctx, "m1"); ok {
		t.Fatal("deleted")
	}
	// The first reason of a job stays.
	_ = s.RecordJobEndReason(ctx, 1, JobEndTimeout, "", statsNow)
	_ = s.RecordJobEndReason(ctx, 1, JobEndStopped, "", statsNow)
	var why string
	_ = s.db.QueryRowContext(ctx, `SELECT reason FROM job_end_reasons WHERE job_id = 1`).Scan(&why)
	if why != JobEndTimeout {
		t.Fatalf("reason %q", why)
	}
}

func TestJobStatsUseTheIndexes(t *testing.T) {
	s := openTemp(t)
	for col, idx := range map[string]string{"machine_id": "jobs_machine_done", "runner_id": "jobs_runner_done"} {
		rows, err := s.db.QueryContext(context.Background(), `EXPLAIN QUERY PLAN SELECT j.id FROM jobs j LEFT JOIN job_end_reasons r ON r.job_id = j.id
 WHERE j.`+col+` = ? AND j.state = 'done' AND j.completed_at >= ? AND j.completed_at < ?`, "x", "a", "b")
		if err != nil {
			t.Fatal(err)
		}
		plan := ""
		for rows.Next() {
			var a, b, c int
			var d string
			_ = rows.Scan(&a, &b, &c, &d)
			plan += d + "\n"
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		_ = rows.Close() //nolint:sqlclosecheck // closed per loop pass
		if !strings.Contains(plan, idx) {
			t.Errorf("%s: plan does not use %s:\n%s", col, idx, plan)
		}
	}
}
