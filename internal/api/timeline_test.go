package api

import (
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/forecast"
	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsdata"
)

func ago(d time.Duration) time.Time { return testNow.Add(-d) }

// finished is a done job on a machine that ran from start to end ago (minutes).
func finished(id int64, machine string, startMin, endMin int, res jobsdata.Result) jobsdata.Job {
	j := aJob(id, "test", jobsdata.Done)
	j.MachineID, j.Result = machine, res
	j.StartedAt, j.CompletedAt = ago(time.Duration(startMin)*time.Minute), ago(time.Duration(endMin)*time.Minute)
	return j
}

func running(id int64, machine string, startMin int) jobsdata.Job {
	j := aJob(id, "build", jobsdata.Running)
	j.MachineID, j.StartedAt = machine, ago(time.Duration(startMin)*time.Minute)
	return j
}

func queuedJob(id int64, machine, reason string) jobsdata.Job {
	j := aJob(id, "lint", jobsdata.Queued)
	j.MachineID, j.WaitReason, j.QueuedAt = machine, reason, ago(2*time.Minute)
	return j
}

func fcOf(seconds float64) forecast.Forecast {
	return forecast.Forecast{Basis: forecast.BasisJobKey, Lo: seconds * 0.8, P50: seconds, Hi: seconds * 1.5}
}

var twoMachines = []timelineMachine{{ID: "m2", Name: "M2", Slots: 1}, {ID: "m1", Name: "M1", Slots: 2}}

func barsByJob(tl gen.Timeline) map[int64]gen.TimelineBar {
	out := map[int64]gen.TimelineBar{}
	for _, b := range tl.Bars {
		if b.JobId != nil {
			out[*b.JobId] = b
		}
	}
	return out
}

func TestTimelineRowsAndSlotAssignment(t *testing.T) {
	tl := buildTimeline(timelineInput{Now: testNow, Machines: twoMachines, Finished: []jobsdata.Job{
		finished(3, "m1", 50, 40, jobsdata.Success), // after job 1 ended: row 1 again
		finished(1, "m1", 60, 45, jobsdata.Success),
		finished(2, "m1", 58, 30, jobsdata.Success), // overlaps job 1: row 2
		finished(4, "m2", 20, 10, jobsdata.Success),
	}, Open: []jobsdata.Job{running(5, "m1", 5)}})
	want := map[int64]string{1: "m1:1", 2: "m1:2", 3: "m1:2", 4: "m2:1", 5: "m1:1"}
	got := barsByJob(tl)
	// job 3 starts at -50, job 1 (row 1) still runs until -45, job 2 (row 2) until -30: a third row is needed.
	want[3] = "m1:3"
	for id, row := range want {
		if got[id].RowId != row {
			t.Fatalf("job %d in %q, want %q", id, got[id].RowId, row)
		}
	}
	keys := []string{}
	for _, r := range tl.Rows {
		keys = append(keys, r.Key)
	}
	if len(keys) != 4 || keys[0] != "m1:1" || keys[2] != "m1:3" || keys[3] != "m2:1" {
		t.Fatalf("rows %v", keys)
	}
	if tl.Rows[0].Label != "M1 slot 1" || tl.Rows[0].MachineName != "M1" || *tl.Rows[0].Slot != 1 {
		t.Fatalf("row %+v", tl.Rows[0])
	}
}

func TestTimelineFinishedAndRunningStates(t *testing.T) {
	tl := buildTimeline(timelineInput{Now: testNow, Machines: twoMachines, Finished: []jobsdata.Job{
		finished(1, "m1", 100, 90, jobsdata.Success),
		finished(2, "m1", 100, 90, jobsdata.Failure),
		finished(3, "m1", 100, 90, jobsdata.TimedOut),
		finished(4, "m1", 100, 90, jobsdata.Cancelled),
		finished(5, "m1", 100, 90, jobsdata.Lost),
	}, Open: []jobsdata.Job{running(6, "m2", 4)}, Forecasts: map[int64]forecast.Forecast{6: fcOf(600)}})
	got := barsByJob(tl)
	want := map[int64]gen.TimelineBarState{1: "done", 2: "failed", 3: "timer_ran_out", 4: "done", 5: "failed", 6: "running"}
	for id, st := range want {
		if got[id].State != st {
			t.Fatalf("job %d is %s, want %s", id, got[id].State, st)
		}
	}
	if got[1].End == nil || got[6].End != nil {
		t.Fatalf("end: done %v, running %v", got[1].End, got[6].End)
	}
	if d := *got[6].Detail; d != "build running for about 4 min, ends in about 6 min" {
		t.Fatalf("running detail %q", d)
	}
	if d := *got[3].Detail; d != "test ran out of time after about 10 min" {
		t.Fatalf("timer detail %q", d)
	}
	if !tl.To.Equal(testNow) || tl.MoreBefore {
		t.Fatalf("to %v more %v", tl.To, tl.MoreBefore)
	}
}

func TestTimelinePagingByTime(t *testing.T) {
	h := func(n int) int { return n * 60 }
	in := timelineInput{Now: testNow, Machines: twoMachines, Finished: []jobsdata.Job{
		finished(1, "m1", h(1)+10, h(1), jobsdata.Success),   // 1 h ago
		finished(2, "m1", h(7)+10, h(7), jobsdata.Success),   // 7 h ago
		finished(3, "m1", h(20)+10, h(20), jobsdata.Success), // 20 h ago
		finished(4, "m1", h(25)+10, h(25), jobsdata.Success), // older than 24 h: never shown
	}}
	ids := func(tl gen.Timeline) (out []int64) {
		for _, b := range tl.Bars {
			out = append(out, *b.JobId)
		}
		return out
	}
	first := buildTimeline(in)
	if g := ids(first); len(g) != 1 || g[0] != 1 || !first.MoreBefore || !first.From.Equal(ago(6*time.Hour)) && first.From.After(ago(time.Hour)) {
		t.Fatalf("first %v more %v from %v", ids(first), first.MoreBefore, first.From)
	}
	before := ago(6 * time.Hour)
	in.Before = &before
	second := buildTimeline(in)
	if g := ids(second); len(g) != 1 || g[0] != 2 || !second.MoreBefore {
		t.Fatalf("second %v more %v", ids(second), second.MoreBefore)
	}
	if len(second.Rows) != len(first.Rows) || !second.To.Equal(testNow) {
		t.Fatalf("rows %d, to %v", len(second.Rows), second.To)
	}
	before, in.Hours = ago(12*time.Hour), 24
	third := buildTimeline(in)
	if g := ids(third); len(g) != 1 || g[0] != 3 || third.MoreBefore || !third.From.Equal(ago(24*time.Hour)) && third.From.After(ago(20*time.Hour)) {
		t.Fatalf("third %v more %v from %v", ids(third), third.MoreBefore, third.From)
	}
	in.Before, in.Hours = nil, 24
	if g := ids(buildTimeline(in)); len(g) != 3 {
		t.Fatalf("24 h at once: %v", g)
	}
}

func TestTimelineKeepsTheRowAcrossPages(t *testing.T) {
	in := timelineInput{Now: testNow, Machines: twoMachines, Finished: []jobsdata.Job{
		finished(1, "m1", 600, 400, jobsdata.Success), finished(2, "m1", 590, 580, jobsdata.Success),
	}}
	before := ago(8 * time.Hour)
	in.Before = &before
	tl := buildTimeline(in)
	if got := barsByJob(tl); len(got) != 1 || got[2].RowId != "m1:2" {
		t.Fatalf("bars %+v", tl.Bars)
	}
}

func TestTimelineMachineFilter(t *testing.T) {
	in := timelineInput{Now: testNow, Machines: twoMachines, MachineID: "m2", Finished: []jobsdata.Job{
		finished(1, "m1", 30, 20, jobsdata.Success), finished(2, "m2", 30, 20, jobsdata.Success),
		finished(3, "m1", 22*60+10, 22*60, jobsdata.Success),
	}, Open: []jobsdata.Job{queuedJob(9, "m1", "free_slot"), queuedJob(10, "m2", "no_runner_with_labels")},
		Forecasts: map[int64]forecast.Forecast{9: fcOf(60), 10: fcOf(60)}}
	tl := buildTimeline(in)
	if len(tl.Rows) != 2 || tl.Rows[0].Key != "m2:1" || tl.Rows[1].Key != "m2:waiting" {
		t.Fatalf("rows %+v", tl.Rows)
	}
	got := barsByJob(tl)
	if len(got) != 2 || got[2].RowId != "m2:1" || got[10].State != "no_runner_fits" {
		t.Fatalf("bars %+v", tl.Bars)
	}
	if tl.MoreBefore { // the old job is on m1
		t.Fatal("more_before counts another machine")
	}
	in.MachineID = "m1"
	if tl = buildTimeline(in); !tl.MoreBefore || len(tl.Rows) != 2 {
		t.Fatalf("m1: more %v rows %d", tl.MoreBefore, len(tl.Rows))
	}
}

func TestTimelineForecastPlacementAndStates(t *testing.T) {
	in := timelineInput{Now: testNow, Machines: []timelineMachine{{ID: "m1", Name: "M1", Slots: 2}}, Open: []jobsdata.Job{
		running(1, "m1", 1), running(2, "m1", 1),
		queuedJob(3, "m1", "free_slot"), queuedJob(4, "m1", "free_slot"), queuedJob(5, "m1", "no_runner_with_labels"),
		queuedJob(6, "m1", "load_over_budget"),
	}, Forecasts: map[int64]forecast.Forecast{1: fcOf(300), 2: fcOf(900), 3: fcOf(120), 4: fcOf(120), 5: fcOf(60), 6: fcOf(60)}}
	tl := buildTimeline(in)
	got := barsByJob(tl)
	// job 1 on row 1 has 240 s left; job 3 follows it there, job 4 follows job 3 (starts 240+120 s).
	if g := got[3]; g.State != "forecast" || g.RowId != "m1:1" || !g.Start.Equal(testNow.Add(240*time.Second)) || !g.End.Equal(testNow.Add(360*time.Second)) {
		t.Fatalf("job 3 %+v", g)
	}
	if g := got[3]; g.EndLo == nil || g.EndHi == nil || !g.EndLo.Before(*g.End) || !g.EndHi.After(*g.End) || g.WaitFrom == nil || !g.WaitFrom.Equal(ago(2*time.Minute)) {
		t.Fatalf("range %+v", g)
	}
	if g := got[4]; g.RowId != "m1:1" || !g.Start.Equal(testNow.Add(360*time.Second)) {
		t.Fatalf("job 4 %+v", g)
	}
	if got[5].State != "no_runner_fits" || got[5].RowId != "m1:waiting" || got[5].End != nil || got[5].WaitFrom == nil {
		t.Fatalf("job 5 %+v", got[5])
	}
	if got[6].State != "waiting_room" || got[6].RowId != "m1:waiting" {
		t.Fatalf("job 6 %+v", got[6])
	}
	if !tl.To.Equal(testNow.Add(480 * time.Second)) { // job 4 ends: 240+120+120
		t.Fatalf("to %v", tl.To.Sub(testNow))
	}
	if d := *got[3].Detail; d != "waits for room on M1, starts in about 4 min, runs about 2 min" {
		t.Fatalf("detail %q", d)
	}
	for _, b := range tl.Bars {
		if b.State == "reserved" {
			t.Fatal("reserved is never emitted")
		}
	}
	in.Before = &testNow
	if tl = buildTimeline(in); len(tl.Bars) != 0 || !tl.To.Equal(testNow) {
		t.Fatalf("a paged call has no running or forecast bars: %+v", tl.Bars)
	}
}

func TestTimelineForecastClipsAtTwentyFourHours(t *testing.T) {
	hour := 3600.0
	in := timelineInput{Now: testNow, Machines: []timelineMachine{{ID: "m1", Name: "M1", Slots: 1}}, Open: []jobsdata.Job{
		queuedJob(1, "m1", "free_slot"), queuedJob(2, "m1", "free_slot"), queuedJob(3, "m1", "free_slot"),
	}, Forecasts: map[int64]forecast.Forecast{1: fcOf(23 * hour), 2: fcOf(2 * hour), 3: fcOf(hour)}}
	tl := buildTimeline(in)
	horizon := testNow.Add(24 * time.Hour)
	got := barsByJob(tl)
	if g := got[2]; g.State != "forecast" || !g.End.Equal(horizon) || g.EndHi.After(horizon) || g.EndLo.After(horizon) {
		t.Fatalf("clipped bar %+v", g)
	}
	if g := got[3]; g.State != "waiting_runner" || g.RowId != "m1:waiting" || *g.Detail != "starts more than 24 hours from now" {
		t.Fatalf("beyond 24 h %+v", g)
	}
	if !tl.To.Equal(horizon) {
		t.Fatalf("to %v", tl.To)
	}
}

func TestTimelineJobWithoutHistoryAndPoolJobs(t *testing.T) {
	in := timelineInput{Now: testNow, Machines: twoMachines, Open: []jobsdata.Job{
		queuedJob(1, "m1", "free_slot"), queuedJob(2, "", "free_slot"), queuedJob(3, "gone", "free_slot"),
	}, Forecasts: map[int64]forecast.Forecast{1: {Basis: forecast.BasisNone}, 2: fcOf(60), 3: fcOf(60)}}
	got := barsByJob(buildTimeline(in))
	if g := got[1]; g.State != "waiting_runner" || g.RowId != "m1:waiting" || *g.Detail != "not enough history yet to place it" {
		t.Fatalf("no history %+v", g)
	}
	// a job no machine took yet goes to the first pool slot, which is M1 slot 1
	if g := got[2]; g.State != "forecast" || g.RowId != "m1:1" || !g.Start.Equal(testNow) {
		t.Fatalf("pool job %+v", g)
	}
	if _, ok := got[3]; ok {
		t.Fatal("a job of a machine that is gone has no row")
	}
	if tl := buildTimeline(timelineInput{Now: testNow, Open: in.Open, Forecasts: in.Forecasts}); len(tl.Rows) != 0 || len(tl.Bars) != 0 {
		t.Fatalf("no machines: %+v", tl)
	}
}

func TestHumanSpan(t *testing.T) {
	cases := map[time.Duration]string{
		10 * time.Second: "less than a minute", 50 * time.Second: "about 1 min", 12 * time.Minute: "about 12 min",
		89 * time.Minute: "about 89 min", 2 * time.Hour: "about 2 h", 125 * time.Minute: "about 2 h 5 min",
	}
	for d, want := range cases {
		if got := humanSpan(d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
	if startWords(10*time.Second) != "now" || startWords(5*time.Minute) != "in about 5 min" {
		t.Fatal("startWords")
	}
}

func TestGetTimelineHandler(t *testing.T) {
	fd := &fakeData{open: []jobsdata.Job{running(5, "m1", 3), queuedJob(6, "m1", "free_slot")},
		byKey: map[string][]jobsdata.Sample{}}
	fd.jobs = []jobsdata.Job{finished(2, "m1", 40, 30, jobsdata.Failure), finished(1, "m1", 90, 80, jobsdata.Success)}
	fd.byKey[fd.open[0].JobKey] = samplesOf(8, 600)
	fd.byKey[fd.open[1].JobKey] = samplesOf(8, 120)
	mux := dataMux(fd, auth.RoleViewer, fakeMachines{slots: map[string]int{"m1": 1}})
	tl := decode[gen.Timeline](t, get(t, mux, "/api/v1/timeline"))
	got := barsByJob(tl)
	if len(tl.Rows) != 1 || tl.Rows[0].Key != "m1:1" || len(got) != 4 || !tl.Now.Equal(testNow) {
		t.Fatalf("rows %+v bars %+v", tl.Rows, tl.Bars)
	}
	if got[2].State != "failed" || got[5].State != "running" || got[6].State != "forecast" || got[6].RowId != "m1:1" {
		t.Fatalf("states %+v", got)
	}
	if !tl.To.After(testNow) || tl.MoreBefore {
		t.Fatalf("to %v more %v", tl.To, tl.MoreBefore)
	}
	if rec := get(t, mux, "/api/v1/timeline?machine_id=other&hours=3&before=2026-11-18T11:00:00Z"); rec.Code != 200 {
		t.Fatalf("paged %d %s", rec.Code, rec.Body)
	}
	if rec := get(t, mux, "/api/v1/timeline?hours=99"); rec.Code != 200 { // more than 24 is 24
		t.Fatalf("hours 99: %d", rec.Code)
	}
	if rec := get(t, dataMux(fd, "", nil), "/api/v1/timeline"); rec.Code != 401 {
		t.Fatalf("anonymous %d", rec.Code)
	}
	if rec := get(t, dataMux(fd, auth.RoleViewer, nil), "/api/v1/timeline"); rec.Code != 501 {
		t.Fatalf("no machines %d", rec.Code)
	}
}
