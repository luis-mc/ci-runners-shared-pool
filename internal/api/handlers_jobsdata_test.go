package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/forecast"
	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
)

var testNow = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

// fakeData is a jobsdata.Reader over slices; it keeps what it was asked.
type fakeData struct {
	jobs     []jobsdata.Job // newest first
	runs     []jobsdata.RunRow
	runInfo  map[int64]jobsdata.RunDetail
	detail   map[int64]jobsdata.JobDetail
	open     []jobsdata.Job
	hist     []jobsdata.HistoryPoint
	byKey    map[string][]jobsdata.Sample
	byStep   map[string][]jobsdata.Sample
	groups   []jobsdata.GroupRow
	stats    jobsdata.Stats
	sums     []jobsdata.DailySummary
	jobQ     []jobsdata.JobFilter
	runQ     []jobsdata.RunFilter
	statDays int
	statF    jobsdata.JobFilter
	by       jobsdata.GroupBy
}

func (f *fakeData) ListRuns(_ context.Context, q jobsdata.RunFilter) ([]jobsdata.RunRow, int, error) {
	f.runQ = append(f.runQ, q)
	var out []jobsdata.RunRow
	for _, r := range f.runs {
		if q.After != 0 && r.SortKey >= q.After {
			continue
		}
		if len(out) < q.Limit+1 {
			out = append(out, r)
		}
	}
	return out, len(f.runs), nil
}

func (f *fakeData) GetRun(_ context.Context, runID int64, attempt int) (jobsdata.RunDetail, bool, error) {
	d, ok := f.runInfo[runID]
	if ok && attempt != 0 && attempt != d.Attempt {
		return jobsdata.RunDetail{}, false, nil
	}
	return d, ok, nil
}

func (f *fakeData) ListJobs(_ context.Context, q jobsdata.JobFilter) ([]jobsdata.Job, int, error) {
	f.jobQ = append(f.jobQ, q)
	var out []jobsdata.Job
	for _, j := range f.jobs {
		if q.After != 0 && j.ID >= q.After {
			continue
		}
		if q.JobKey != "" && j.JobKey != q.JobKey {
			continue
		}
		if q.State != "" && j.State != q.State {
			continue
		}
		if len(out) < q.Limit+1 {
			out = append(out, j)
		}
	}
	return out, len(f.jobs), nil
}

func (f *fakeData) GetJob(_ context.Context, id int64) (jobsdata.JobDetail, bool, error) {
	d, ok := f.detail[id]
	return d, ok, nil
}

func (f *fakeData) History(context.Context, int64, string, int) ([]jobsdata.HistoryPoint, error) {
	return f.hist, nil
}

func (f *fakeData) Samples(_ context.Context, q jobsdata.SampleQuery) ([]jobsdata.Sample, error) {
	if q.StepName != "" {
		return f.byStep[q.StepName], nil
	}
	if q.JobKey != "" {
		return f.byKey[q.JobKey], nil
	}
	return nil, nil
}

func (f *fakeData) OpenJobs(context.Context) ([]jobsdata.Job, error) { return f.open, nil }

func (f *fakeData) Groups(_ context.Context, by jobsdata.GroupBy) ([]jobsdata.GroupRow, error) {
	f.by = by
	return f.groups, nil
}

func (f *fakeData) Stats(_ context.Context, days int, q jobsdata.JobFilter) (jobsdata.Stats, error) {
	f.statDays, f.statF = days, q
	return f.stats, nil
}

func (f *fakeData) Summaries(context.Context, time.Time, time.Time) ([]jobsdata.DailySummary, error) {
	return f.sums, nil
}

// fakeMachines gives the slot counts; the rest of the interface is never called.
type fakeMachines struct {
	MachinesService
	slots map[string]int
}

func (f fakeMachines) List(context.Context) ([]machines.View, error) {
	var out []machines.View
	for id, n := range f.slots {
		out = append(out, machines.View{Machine: machines.Machine{ID: id}, Settings: machines.Settings{JobSlots: n}})
	}
	return out, nil
}

func dataMux(d jobsdata.Reader, role auth.Role, m MachinesService) *http.ServeMux {
	mux := http.NewServeMux()
	opts := Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), JobsData: d, Machines: m,
		Now: func() time.Time { return testNow }}
	if role != "" {
		opts.Authn = asRole(role)
	}
	Register(mux, opts)
	return mux
}

func get(t *testing.T, mux *http.ServeMux, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil))
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("body %s: %v", rec.Body, err)
	}
	return out
}

func samplesOf(n int, seconds float64) []jobsdata.Sample {
	out := make([]jobsdata.Sample, n)
	for i := range out {
		out[i] = jobsdata.Sample{At: testNow.Add(-time.Duration(i+1) * time.Hour), Seconds: seconds}
	}
	return out
}

func aJob(id int64, name string, state jobsdata.State) jobsdata.Job {
	jt, matrix := jobsdata.SplitName(name)
	return jobsdata.Job{ID: id, RepositoryID: 7, Repository: "o/r", RunID: 500, Attempt: 1, Workflow: ".github/workflows/ci.yml", Name: name, JobType: jt, Matrix: matrix,
		JobKey: jobsdata.MakeKey("o/r", ".github/workflows/ci.yml", name), Branch: "OPS34-x", State: state, CreatedAt: testNow.Add(-time.Hour)}
}

func TestListJobsPagesWithTotalAndForecast(t *testing.T) {
	fd := &fakeData{byKey: map[string][]jobsdata.Sample{}}
	for id := int64(30); id >= 1; id-- {
		j := aJob(id, "test", jobsdata.Done)
		j.Result = jobsdata.Success
		fd.jobs = append(fd.jobs, j)
	}
	fd.byKey[fd.jobs[0].JobKey] = samplesOf(8, 120)
	mux := dataMux(fd, auth.RoleViewer, nil)
	page := decode[gen.JobPage](t, get(t, mux, "/api/v1/jobs"))
	if len(page.Items) != 25 || page.Total == nil || *page.Total != 30 || page.NextCursor == nil || *page.NextCursor != "j6" {
		t.Fatalf("page %d total %v cursor %v", len(page.Items), page.Total, page.NextCursor)
	}
	if fd.jobQ[0].Limit != 25 {
		t.Fatalf("default limit %d", fd.jobQ[0].Limit)
	}
	fc := page.Items[0].Forecast
	if fc == nil || fc.Basis != gen.ForecastBasis(forecast.BasisJobKey) || fc.P50 != 120 || fc.Samples != 8 || fc.Lo > fc.P50 || fc.Hi < fc.P50 {
		t.Fatalf("forecast %+v", fc)
	}
	if page.Items[0].JobIdent == nil || *page.Items[0].JobIdent != fd.jobs[0].JobKey || page.Items[0].NodeId == nil || *page.Items[0].NodeId != "test" {
		t.Fatalf("job %+v", page.Items[0])
	}
	next := decode[gen.JobPage](t, get(t, mux, "/api/v1/jobs?cursor=j6&limit=100"))
	if len(next.Items) != 5 || next.NextCursor != nil || fd.jobQ[1].After != 6 || fd.jobQ[1].Limit != 100 {
		t.Fatalf("second page %d %v %+v", len(next.Items), next.NextCursor, fd.jobQ[1])
	}
}

func TestListJobsWithoutHistoryHasNoNumberToTrust(t *testing.T) {
	fd := &fakeData{jobs: []jobsdata.Job{aJob(1, "build", jobsdata.Done)}}
	page := decode[gen.JobPage](t, get(t, dataMux(fd, auth.RoleViewer, nil), "/api/v1/jobs"))
	if fc := page.Items[0].Forecast; fc == nil || fc.Basis != gen.ForecastBasis(forecast.BasisNone) || fc.P25 != nil {
		t.Fatalf("forecast %+v", fc)
	}
}

func TestListJobsPassesTheFilters(t *testing.T) {
	fd := &fakeData{}
	rec := get(t, dataMux(fd, auth.RoleViewer, nil), "/api/v1/jobs?state=done&result=failure&repository_id=7&machine_id=m1&backend=docker&runner=r1&runner_kind=ephemeral"+
		"&job_type=test&job_ident=k&branch=main&run_id=9&from=2026-11-01T00:00:00Z&to=2026-11-02T00:00:00Z")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	f := fd.jobQ[0]
	want := jobsdata.JobFilter{State: jobsdata.Done, Result: jobsdata.Failure, RepositoryID: 7, MachineID: "m1", Backend: "docker", RunnerName: "r1",
		RunnerKind: jobsdata.Ephemeral, JobType: "test", JobKey: "k", Branch: "main", RunID: 9, From: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		To: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), Limit: 25}
	if !f.From.Equal(want.From) || !f.To.Equal(want.To) {
		t.Fatalf("times %+v", f)
	}
	f.From, f.To, want.From, want.To = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	if f.State != want.State || f.Result != want.Result || f.RepositoryID != 7 || f.MachineID != "m1" || f.Backend != "docker" || f.RunnerName != "r1" ||
		f.RunnerKind != want.RunnerKind || f.JobType != "test" || f.JobKey != "k" || f.Branch != "main" || f.RunID != 9 || f.Limit != 25 {
		t.Fatalf("filter %+v", f)
	}
}

func TestJobsRoutesRefuseBadInput(t *testing.T) {
	mux := dataMux(&fakeData{}, auth.RoleViewer, nil)
	for _, target := range []string{
		"/api/v1/jobs?limit=0", "/api/v1/jobs?limit=101", "/api/v1/jobs?state=nope", "/api/v1/jobs?runner_kind=x", "/api/v1/jobs?cursor=zz",
		"/api/v1/runs?limit=101", "/api/v1/runs?state=nope", "/api/v1/runs?cursor=r0",
		"/api/v1/jobs/groups", "/api/v1/jobs/groups?by=nope", "/api/v1/jobs/stats?days=9", "/api/v1/jobs/stats?state=nope",
		"/api/v1/forecast", "/api/v1/forecast?job_ident=k", "/api/v1/forecast?repository_id=7",
	} {
		if rec := get(t, mux, target); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (%s)", target, rec.Code, rec.Body)
		}
	}
}

func TestQueuePositionAndEtaFromTheSimulation(t *testing.T) {
	running := aJob(1, "build", jobsdata.Running)
	running.MachineID, running.StartedAt = "m1", testNow.Add(-10*time.Second)
	running.Forecast = &jobsdata.ForecastRecord{LoMS: 60000, MidMS: 60000, HiMS: 60000, Basis: "job_key", Samples: 10}
	first, second := aJob(2, "test", jobsdata.Queued), aJob(3, "test (b)", jobsdata.Queued)
	first.MachineID, second.MachineID = "m1", "m1"
	first.QueuedAt, second.QueuedAt = testNow.Add(-5*time.Minute), testNow.Add(-4*time.Minute)
	fd := &fakeData{jobs: []jobsdata.Job{second, first, running}, open: []jobsdata.Job{second, running, first},
		byKey: map[string][]jobsdata.Sample{first.JobKey: samplesOf(6, 100), second.JobKey: samplesOf(6, 100)}}
	mux := dataMux(fd, auth.RoleViewer, fakeMachines{slots: map[string]int{"m1": 1}})
	page := decode[gen.JobPage](t, get(t, mux, "/api/v1/jobs?state=queued"))
	if len(page.Items) != 2 {
		t.Fatalf("items %d", len(page.Items))
	}
	byID := map[int64]gen.Job{}
	for _, j := range page.Items {
		byID[j.Id] = j
	}
	a, b := byID[2], byID[3]
	if a.QueuePosition == nil || *a.QueuePosition != 1 || b.QueuePosition == nil || *b.QueuePosition != 2 {
		t.Fatalf("positions %v %v", a.QueuePosition, b.QueuePosition)
	}
	// The running job has 50 s left of 60; the first queued job takes the slot then for 100 s.
	if a.QueueEta == nil || a.QueueEta.P50 != 50 || b.QueueEta == nil || b.QueueEta.P50 != 150 || b.QueueEta.Lo != 140 || b.QueueEta.Hi != 160 {
		t.Fatalf("etas %+v %+v", a.QueueEta, b.QueueEta)
	}
}

func TestQueueEtaIsUnknownWithoutSlotCounts(t *testing.T) {
	q := aJob(2, "test", jobsdata.Queued)
	q.MachineID = "m1"
	fd := &fakeData{jobs: []jobsdata.Job{q}, open: []jobsdata.Job{q}, byKey: map[string][]jobsdata.Sample{q.JobKey: samplesOf(6, 100)}}
	page := decode[gen.JobPage](t, get(t, dataMux(fd, auth.RoleViewer, nil), "/api/v1/jobs"))
	if page.Items[0].QueuePosition == nil || *page.Items[0].QueuePosition != 1 || page.Items[0].QueueEta != nil {
		t.Fatalf("job %+v", page.Items[0])
	}
}

func TestQueueETAsPureOrderAndPool(t *testing.T) {
	a, b := aJob(1, "x", jobsdata.Queued), aJob(2, "y", jobsdata.Queued)
	a.QueuedAt, b.QueuedAt = testNow, testNow.Add(-time.Minute) // b took its ticket first
	fc := map[int64]forecast.Forecast{1: forecast.FromSeconds([]float64{10, 10, 10, 10, 10}, forecast.BasisJobKey, ""), 2: forecast.FromSeconds([]float64{20, 20, 20, 20, 20}, forecast.BasisJobKey, "")}
	got := queueETAs([]jobsdata.Job{a, b}, fc, map[string]int{"": 1}, testNow)
	if got[2].Position != 1 || got[1].Position != 2 || got[2].Eta.P50 != 0 || got[1].Eta.P50 != 20 || !got[1].Known {
		t.Fatalf("%+v", got)
	}
}

func fileRun() jobsdata.RunDetail {
	mk := func(id int64, name string, start, end int) jobsdata.Job {
		j := aJob(id, name, jobsdata.Done)
		j.Result = jobsdata.Success
		j.StartedAt, j.CompletedAt = testNow.Add(time.Duration(start)*time.Second), testNow.Add(time.Duration(end)*time.Second)
		return j
	}
	d := jobsdata.RunDetail{Attempts: []int{1}}
	d.Run = jobsdata.Run{RepositoryID: 7, Repository: "o/r", RunID: 500, Attempt: 1, Workflow: ".github/workflows/ci.yml", Branch: "OPS34-x", PullRequest: 12,
		TaskID: "OPS34", HTMLURL: "https://github.com/o/r/actions/runs/500", Event: "push", CreatedAt: testNow.Add(-time.Hour), Conclusion: jobsdata.Success}
	d.State = jobsdata.Done
	d.Jobs = []jobsdata.Job{mk(1, "build", 0, 10), mk(2, "test (ubuntu)", 10, 40), mk(3, "test (mac)", 10, 25), mk(4, "deploy", 40, 50)}
	d.Graph = jobsdata.Graph{OK: true, Needs: map[string][]string{"test": {"build"}, "deploy": {"test"}}, Dynamic: map[string]bool{"test": true}}
	return d
}

func TestGetRunEdgesFromTheWorkflowFile(t *testing.T) {
	d := fileRun()
	fd := &fakeData{runInfo: map[int64]jobsdata.RunDetail{500: d}}
	run := decode[gen.RunItem](t, get(t, dataMux(fd, auth.RoleViewer, nil), "/api/v1/runs/500"))
	want := [][]string{{"build", "test (mac)"}, {"build", "test (ubuntu)"}, {"test (mac)", "deploy"}, {"test (ubuntu)", "deploy"}}
	if len(run.Edges) != len(want) {
		t.Fatalf("edges %v", run.Edges)
	}
	for i := range want {
		if run.Edges[i][0] != want[i][0] || run.Edges[i][1] != want[i][1] {
			t.Fatalf("edges %v", run.Edges)
		}
	}
	if run.Graph.Source != "workflow_file" || run.Graph.Note == nil || !strings.Contains(*run.Graph.Note, "expand at run time") || len(run.Graph.DynamicNodes) != 2 {
		t.Fatalf("graph %+v", run.Graph)
	}
	// A finished run's critical path is the one that took the time.
	if strings.Join(run.CriticalPath, ">") != "build>test (ubuntu)>deploy" || run.Eta != nil {
		t.Fatalf("path %v eta %v", run.CriticalPath, run.Eta)
	}
	if run.Attempts[0] != 1 || run.PullRequest == nil || *run.PullRequest != 12 || run.TaskId == nil || *run.TaskId != "OPS34" || len(run.Jobs) != 4 {
		t.Fatalf("run %+v", run)
	}
	for _, j := range run.Jobs {
		if j.JobName == "deploy" && (j.Needs == nil || len(*j.Needs) != 2) {
			t.Fatalf("deploy needs %v", j.Needs)
		}
	}
}

func TestGetRunFallsBackToTimingOverlap(t *testing.T) {
	d := fileRun()
	d.Graph = jobsdata.Graph{}
	fd := &fakeData{runInfo: map[int64]jobsdata.RunDetail{500: d}}
	run := decode[gen.RunItem](t, get(t, dataMux(fd, auth.RoleViewer, nil), "/api/v1/runs/500?attempt=1"))
	if run.Graph.Source != "timing_overlap" || run.Graph.Note == nil {
		t.Fatalf("graph %+v", run.Graph)
	}
	// build to deploy is dropped: build reaches deploy through test.
	for _, e := range run.Edges {
		if e[0] == "build" && e[1] == "deploy" {
			t.Fatalf("edge not reduced: %v", run.Edges)
		}
	}
	if len(run.Edges) != 4 {
		t.Fatalf("edges %v", run.Edges)
	}
}

func TestGetRunOpenRunHasEtaAndUnknownRunIs404(t *testing.T) {
	d := fileRun()
	d.State = jobsdata.Running
	d.Conclusion = ""
	d.Jobs[3].State, d.Jobs[3].StartedAt, d.Jobs[3].CompletedAt, d.Jobs[3].Result = jobsdata.Queued, time.Time{}, time.Time{}, ""
	d.Jobs[3].Forecast = nil
	fd := &fakeData{runInfo: map[int64]jobsdata.RunDetail{500: d}, byKey: map[string][]jobsdata.Sample{d.Jobs[3].JobKey: samplesOf(6, 30)}}
	mux := dataMux(fd, auth.RoleViewer, nil)
	run := decode[gen.RunItem](t, get(t, mux, "/api/v1/runs/500"))
	if run.Eta == nil || run.Eta.P50 != 30 {
		t.Fatalf("eta %+v", run.Eta)
	}
	// Without history for the open job the ETA is not made up.
	fd.byKey = nil
	if run := decode[gen.RunItem](t, get(t, mux, "/api/v1/runs/500")); run.Eta != nil {
		t.Fatalf("eta %+v", run.Eta)
	}
	if rec := get(t, mux, "/api/v1/runs/999"); rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
	if rec := get(t, mux, "/api/v1/runs/500?attempt=3"); rec.Code != http.StatusNotFound {
		t.Fatalf("attempt: status %d", rec.Code)
	}
}

func TestGraphEdgesPure(t *testing.T) {
	d := fileRun()
	edges, needs, g := graphEdges(d)
	if len(edges) != 4 || len(needs["deploy"]) != 2 || g.Source != "workflow_file" {
		t.Fatalf("%v %v %+v", edges, needs, g)
	}
	// Equal times in both directions never make a cycle.
	a, b := aJob(1, "a", jobsdata.Done), aJob(2, "b", jobsdata.Done)
	a.StartedAt, a.CompletedAt, b.StartedAt, b.CompletedAt = testNow, testNow, testNow, testNow
	if e := timingEdges([]jobsdata.Job{a, b}); len(e) != 0 {
		t.Fatalf("edges %v", e)
	}
}

func TestListRunsPagesFiltersAndEta(t *testing.T) {
	fd := &fakeData{runInfo: map[int64]jobsdata.RunDetail{}}
	for i := int64(30); i >= 1; i-- {
		r := jobsdata.RunRow{Run: jobsdata.Run{RunID: 100 + i, Attempt: 1, Repository: "o/r", Branch: "main", CreatedAt: testNow.Add(-time.Hour)}, State: jobsdata.Done,
			JobsTotal: 3, JobsDone: 3, TotalMS: 90000, SortKey: i * 10}
		fd.runs = append(fd.runs, r)
	}
	fd.runs[0].State, fd.runs[0].TotalMS = jobsdata.Running, 0
	open := fileRun()
	open.RunRow = fd.runs[0]
	open.Jobs = open.Jobs[:1]
	open.Jobs[0].State, open.Jobs[0].StartedAt, open.Jobs[0].CompletedAt = jobsdata.Running, testNow.Add(-20*time.Second), time.Time{}
	open.Jobs[0].Forecast = &jobsdata.ForecastRecord{LoMS: 60000, MidMS: 60000, HiMS: 60000, Basis: "job_key", Samples: 9}
	open.Graph = jobsdata.Graph{}
	fd.runInfo[fd.runs[0].RunID] = open
	mux := dataMux(fd, auth.RoleViewer, nil)
	page := decode[gen.RunPage](t, get(t, mux, "/api/v1/runs?state=running&result=failure&repository_id=7&branch=main&from=2026-11-01T00:00:00Z"))
	q := fd.runQ[0]
	if q.State != jobsdata.Running || q.Result != jobsdata.Failure || q.RepositoryID != 7 || q.Branch != "main" || q.Limit != 25 || q.From.IsZero() {
		t.Fatalf("filter %+v", q)
	}
	if len(page.Items) != 25 || page.Total != 30 || page.NextCursor == nil || *page.NextCursor != "r60" {
		t.Fatalf("page %d %d %v", len(page.Items), page.Total, page.NextCursor)
	}
	first := page.Items[0]
	if first.Eta == nil || first.Eta.P50 != 40 || first.TotalSeconds == nil || *first.TotalSeconds != 3600 {
		t.Fatalf("open run %+v", first)
	}
	if page.Items[1].Eta != nil || *page.Items[1].TotalSeconds != 90 {
		t.Fatalf("done run %+v", page.Items[1])
	}
	next := decode[gen.RunPage](t, get(t, mux, "/api/v1/runs?cursor=r60&limit=100"))
	if len(next.Items) != 5 || fd.runQ[1].After != 60 {
		t.Fatalf("next %d %+v", len(next.Items), fd.runQ[1])
	}
}

func TestGetJobDetail(t *testing.T) {
	j := aJob(5, "test", jobsdata.Running)
	j.StartedAt = testNow.Add(-30 * time.Second)
	run := fileRun()
	run.State = jobsdata.Running
	run.Jobs = []jobsdata.Job{j}
	run.Graph = jobsdata.Graph{}
	fd := &fakeData{
		detail: map[int64]jobsdata.JobDetail{5: {Job: j, Needs: []string{"build"}, Attempts: []jobsdata.JobAttempt{{Attempt: 1, JobID: 5, State: jobsdata.Running}},
			Steps: []jobsdata.Step{{JobID: 5, Number: 1, Name: "Set up", State: "done", Result: "success", StartedAt: testNow.Add(-30 * time.Second), CompletedAt: testNow.Add(-20 * time.Second)},
				{JobID: 5, Number: 2, Name: "Run tests", State: "running", StartedAt: testNow.Add(-20 * time.Second)}}}},
		runInfo: map[int64]jobsdata.RunDetail{500: run},
		hist:    []jobsdata.HistoryPoint{{At: testNow.Add(-time.Hour), Seconds: 61, Result: jobsdata.Success, Branch: "main", Machine: "m1"}},
		byKey:   map[string][]jobsdata.Sample{j.JobKey: samplesOf(6, 60)},
		byStep:  map[string][]jobsdata.Sample{"Set up": samplesOf(6, 9)},
	}
	mux := dataMux(fd, auth.RoleViewer, nil)
	d := decode[gen.JobDetail](t, get(t, mux, "/api/v1/jobs/5"))
	if len(d.Steps) != 2 || d.Steps[0].Forecast == nil || d.Steps[0].Forecast.P50 != 9 || d.Steps[1].Forecast != nil || *d.Steps[0].RunSeconds != 10 {
		t.Fatalf("steps %+v", d.Steps)
	}
	if len(d.History) != 1 || d.History[0].Seconds != 61 || len(d.Attempts) != 1 || d.Forecast == nil || d.Forecast.P50 != 60 {
		t.Fatalf("detail %+v", d)
	}
	if d.RunEta == nil || d.RunEta.P50 != 30 || d.Needs == nil || (*d.Needs)[0] != "build" {
		t.Fatalf("run eta %+v needs %v", d.RunEta, d.Needs)
	}
	if rec := get(t, mux, "/api/v1/jobs/404"); rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestGroupsStatsSummariesAndForecast(t *testing.T) {
	light := true
	fd := &fakeData{
		groups: []jobsdata.GroupRow{{Name: "test", Filter: "job_type=test", Running: 1, Done: 4, WaitP50: 2, Trend: []float64{1, 2}, Light: &light}},
		stats: jobsdata.Stats{Days: []jobsdata.Day{{Day: "2026-11-17", Jobs: 3, Failed: 1, RunP50: 9}}, Hours: []jobsdata.HourCount{{Weekday: 2, Hour: 9, Count: 4}},
			ForecastPoints: []jobsdata.ForecastPoint{{JobID: 1, ForecastS: 10, ActualS: 12, LoS: 9, HiS: 14, Basis: "job_key"}}},
		sums:   []jobsdata.DailySummary{{Day: "2026-11-17", Repository: "o/r", JobType: "test", Jobs: 3, RunP50MS: 9000}},
		jobs:   []jobsdata.Job{aJob(5, "test", jobsdata.Done)},
		detail: map[int64]jobsdata.JobDetail{5: {Job: aJob(5, "test", jobsdata.Done), Steps: []jobsdata.Step{{Number: 1, Name: "Set up"}}}},
		hist:   []jobsdata.HistoryPoint{{At: testNow, Seconds: 5, Branch: "main"}},
		byKey:  map[string][]jobsdata.Sample{aJob(5, "test", jobsdata.Done).JobKey: samplesOf(7, 50)},
		byStep: map[string][]jobsdata.Sample{"Set up": samplesOf(5, 4)},
	}
	mux := dataMux(fd, auth.RoleViewer, nil)
	groups := decode[gen.JobGroupList](t, get(t, mux, "/api/v1/jobs/groups?by=runner-type"))
	if fd.by != jobsdata.ByRunnerType || len(groups.Items) != 1 || groups.Items[0].Light == nil || len(groups.Items[0].Trend) != 2 {
		t.Fatalf("groups %+v by %s", groups, fd.by)
	}
	stats := decode[gen.JobStats](t, get(t, mux, "/api/v1/jobs/stats?runner=r1&branch=main"))
	if fd.statDays != 14 || fd.statF.RunnerName != "r1" || fd.statF.Branch != "main" || len(stats.Days) != 1 || len(stats.Hours) != 1 ||
		stats.ForecastPoints[0].Note == nil || stats.ForecastPoints[0].ActualSeconds != 12 {
		t.Fatalf("stats %+v days %d %+v", stats, fd.statDays, fd.statF)
	}
	if rec := get(t, mux, "/api/v1/jobs/stats?days=30"); rec.Code != 200 || fd.statDays != 30 {
		t.Fatalf("days %d", fd.statDays)
	}
	sums := decode[gen.JobSummaryList](t, get(t, mux, "/api/v1/jobs/summaries?from=2026-11-01T00:00:00Z"))
	if len(sums.Items) != 1 || sums.Items[0].RunP50Ms != 9000 {
		t.Fatalf("summaries %+v", sums)
	}
	ans := decode[gen.ForecastAnswer](t, get(t, mux, "/api/v1/forecast?repository_id=7&job_ident="+strings.ReplaceAll(aJob(5, "test", jobsdata.Done).JobKey, " ", "%20")))
	if want := gen.ForecastBasisJobKey; ans.Forecast.Basis != want {
		t.Fatalf("forecast basis %q, want %q", ans.Forecast.Basis, want)
	}
	if ans.Forecast.P50 != 50 || len(ans.Steps) != 1 || ans.Steps[0].Forecast.P50 != 4 || len(ans.History) != 1 {
		t.Fatalf("forecast %+v", ans)
	}
}

func TestJobsExportWithTheNewFilters(t *testing.T) {
	j := aJob(1, "=cmd", jobsdata.Done)
	j.RunnerKind, j.RunnerName = jobsdata.Ephemeral, "r1"
	fd := &fakeData{jobs: []jobsdata.Job{j}}
	rec := get(t, dataMux(fd, auth.RoleViewer, nil), "/api/v1/exports/jobs?runner=r1&job_type=test&branch=main")
	rows, err := csv.NewReader(rec.Body).ReadAll()
	if rec.Code != 200 || err != nil || len(rows) != 2 {
		t.Fatalf("status %d rows %v (%v)", rec.Code, rows, err)
	}
	if fd.jobQ[0].RunnerName != "r1" || fd.jobQ[0].JobType != "test" || fd.jobQ[0].Branch != "main" || fd.jobQ[0].Limit != exportPage {
		t.Fatalf("filter %+v", fd.jobQ[0])
	}
	col := map[string]int{}
	for i, c := range rows[0] {
		col[c] = i
	}
	if rows[1][col["job_name"]] != "'=cmd" || rows[1][col["runner_kind"]] != "ephemeral" || rows[1][col["branch"]] != "OPS34-x" {
		t.Fatalf("row %v", rows[1])
	}
	if rec := get(t, dataMux(fd, auth.RoleViewer, nil), "/api/v1/exports/jobs?runner_kind=x"); rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestExportJobSummariesCSV(t *testing.T) {
	fd := &fakeData{sums: []jobsdata.DailySummary{
		{Day: "2026-11-17", Repository: "o/r", JobType: "=HYPERLINK(\"x\")", Jobs: 3, Failed: 1, WaitP50MS: 10, WaitP90MS: 20, RunP50MS: 30, RunP90MS: 40},
		{Day: "2026-11-16", Repository: "+o/r", JobType: "build", Jobs: 1},
	}}
	rec := get(t, dataMux(fd, auth.RoleViewer, nil), "/api/v1/exports/job-summaries")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/csv" {
		t.Fatalf("status %d headers %v", rec.Code, rec.Header())
	}
	if cd := rec.Header().Get("Content-Disposition"); !regexp.MustCompile(`^attachment; filename="crsp-job-summaries-\d{4}-\d{2}-\d{2}-\d{4}\.csv"$`).MatchString(cd) {
		t.Fatalf("content disposition %q", cd)
	}
	rows, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil || len(rows) != 3 || rows[0][0] != "day" || rows[1][2] != `'=HYPERLINK("x")` || rows[2][1] != "'+o/r" || rows[1][8] != "40" {
		t.Fatalf("rows %v (%v)", rows, err)
	}
}

func TestJobsDataRoutesNeedASignedInUserAndAWiredStore(t *testing.T) {
	routes := []string{"/api/v1/runs", "/api/v1/runs/1", "/api/v1/jobs/groups?by=repository", "/api/v1/jobs/stats", "/api/v1/jobs/summaries",
		"/api/v1/forecast?job_ident=k&repository_id=1", "/api/v1/exports/job-summaries", "/api/v1/jobs", "/api/v1/jobs/1"}
	for _, r := range routes {
		if rec := get(t, dataMux(&fakeData{}, "", nil), r); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymous: %d", r, rec.Code)
		}
		if rec := get(t, dataMux(&fakeData{}, auth.RoleViewer, nil), r); rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden || rec.Code == http.StatusNotImplemented {
			t.Errorf("%s viewer: %d", r, rec.Code)
		}
	}
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Authn: asRole(auth.RoleViewer)})
	for _, r := range routes[:7] {
		if rec := get(t, mux, r); rec.Code != http.StatusNotImplemented {
			t.Errorf("%s unwired: %d", r, rec.Code)
		}
	}
}

// jobDetailBody must carry the same fields as the generated gen.JobDetail.
func TestJobDetailBodyMatchesTheContract(t *testing.T) {
	b, err := json.Marshal(jobDetailBody{Job: toJobItem(aJob(1, "x", jobsdata.Done)), Steps: []gen.StepItem{}, History: []gen.HistoryPoint{}, Attempts: []gen.AttemptRef{}})
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var d gen.JobDetail
	if err := dec.Decode(&d); err != nil {
		t.Fatal(err)
	}
}

func TestRunDetailAndListCarryTheFollowUps(t *testing.T) {
	d := fileRun()
	d.FollowUps, d.FollowUpsOpen = 1, 1
	d.StartedNext = []jobsdata.FollowUp{{RunID: 900, Attempt: 1, WorkflowName: "Installers", State: jobsdata.Running,
		StartedAt: testNow.Add(-time.Minute), HTMLURL: "https://github.com/o/r/actions/runs/900"}}
	fd := &fakeData{runInfo: map[int64]jobsdata.RunDetail{500: d}, runs: []jobsdata.RunRow{d.RunRow}}
	mux := dataMux(fd, auth.RoleViewer, nil)
	run := decode[gen.RunItem](t, get(t, mux, "/api/v1/runs/500"))
	// the run's own state and result are unchanged; the overall state says a follow-up still runs
	if run.State != gen.RunItemStateDone || run.Result == nil || *run.Result != gen.JobResultSuccess || run.OverallState != gen.RunItemOverallStateFollowUpRunning {
		t.Fatalf("states: %s %v %s", run.State, run.Result, run.OverallState)
	}
	if len(run.StartedNext) != 1 || run.StartedNext[0].WorkflowName != "Installers" || run.StartedNext[0].RunId != 900 ||
		run.StartedNext[0].State != gen.RunFollowUpStateRunning || run.StartedNext[0].CompletedAt != nil || run.StartedNext[0].Link == "" {
		t.Fatalf("started next: %+v", run.StartedNext)
	}
	page := decode[gen.RunPage](t, get(t, mux, "/api/v1/runs"))
	if got := page.Items[0]; got.OverallState != gen.RunSummaryOverallStateFollowUpRunning || got.State != gen.RunSummaryStateDone || *got.FollowUpsOpen != 1 {
		t.Fatalf("summary: %+v", got)
	}
	// finished follow-up: overall done again
	d.FollowUpsOpen = 0
	d.StartedNext = []jobsdata.FollowUp{{RunID: 900, WorkflowName: "Installers", State: jobsdata.Done, CompletedAt: testNow, HTMLURL: "https://github.com/o/r/actions/runs/900"}}
	fd.runInfo[500], fd.runs[0] = d, d.RunRow
	if run = decode[gen.RunItem](t, get(t, mux, "/api/v1/runs/500")); run.OverallState != gen.RunItemOverallStateDone {
		t.Fatalf("overall after follow-up: %s", run.OverallState)
	}
}
