package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

// commitData adds the commit reader to the fake of the jobs tests.
type commitData struct {
	*fakeData
	runs    []jobsdata.RunDetail
	gotSHA  string
	gotRepo int64
}

func (c *commitData) RunsOfCommit(_ context.Context, repo int64, sha string) ([]jobsdata.RunDetail, error) {
	c.gotSHA, c.gotRepo = sha, repo
	return c.runs, nil
}

func commitFixture() *commitData {
	t0 := testNow.Add(-time.Hour)
	run := func(id int64, state jobsdata.State, created, completed time.Duration, jobs ...jobsdata.Job) jobsdata.RunDetail {
		r := jobsdata.RunDetail{Jobs: jobs}
		r.RunID, r.Attempt, r.Repository, r.HeadSHA, r.Workflow = id, 1, "o/r", "abc1234def5678abc1234def5678abc1234def56", "ci.yml"
		r.CreatedAt, r.State, r.JobsTotal = t0.Add(created), state, len(jobs)
		if state == jobsdata.Done {
			r.CompletedAt, r.Conclusion = t0.Add(completed), jobsdata.Success
		}
		return r
	}
	job := func(id int64, name string, from, to time.Duration) jobsdata.Job {
		return jobsdata.Job{ID: id, Name: name, RunID: 1, StartedAt: t0.Add(from), CompletedAt: t0.Add(to), State: jobsdata.Done}
	}
	step := func(n int, name string, from, to time.Duration) jobsdata.Step {
		return jobsdata.Step{Number: n, Name: name, StartedAt: t0.Add(from), CompletedAt: t0.Add(to)}
	}
	m := time.Minute
	fd := &fakeData{detail: map[int64]jobsdata.JobDetail{
		11: {Steps: []jobsdata.Step{
			step(1, "Set up job", 0, 4*m), step(2, "checkout", 4*m, 5*m), step(3, "build", 5*m, 9*m),
			step(4, "test", 9*m, 12*m), step(5, "Post checkout", 12*m, 13*m), step(6, "Complete job", 13*m, 14*m),
		}},
	}}
	return &commitData{fakeData: fd, runs: []jobsdata.RunDetail{
		run(1, jobsdata.Done, 0, 20*m, job(10, "lint", 1*m, 3*m), job(11, "test", 2*m, 16*m)),
		run(2, jobsdata.Running, 10*m, 0, job(20, "docs", 11*m, 12*m)),
	}}
}

// R5.13: the commit view over the API. A run still going means the commit is not done; the slowest job comes with
// its share of the wall time and its two slowest steps, bookkeeping left out.
func TestGetCommitView(t *testing.T) {
	cd := commitFixture()
	mux := dataMux(cd, auth.RoleViewer, nil)
	rec := get(t, mux, "/api/v1/commits/ABC1234?repository_id=7")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if cd.gotSHA != "abc1234" || cd.gotRepo != 7 {
		t.Errorf("asked for %q in %d", cd.gotSHA, cd.gotRepo)
	}
	v := decode[gen.CommitView](t, rec)
	if v.AllRunsDone || v.RunsTotal != 2 || v.RunsDone != 1 || len(v.Runs) != 2 || v.Repository == nil || *v.Repository != "o/r" {
		t.Fatalf("view %+v", v)
	}
	if v.WallSeconds != 3600 { // the open run holds the commit open to now, one hour after the first run
		t.Errorf("wall %v", v.WallSeconds)
	}
	sj := v.SlowestJob
	if sj == nil || sj.JobId != 11 || sj.Name != "test" || sj.Seconds != 840 || sj.SharePercent != 23 {
		t.Fatalf("slowest %+v", sj)
	}
	if len(sj.SlowestSteps) != 2 || sj.SlowestSteps[0].Name != "build" || sj.SlowestSteps[0].Seconds != 240 || sj.SlowestSteps[1].Name != "test" {
		t.Errorf("steps %+v", sj.SlowestSteps)
	}

	cd.runs[1] = jobsdata.RunDetail{RunRow: jobsdata.RunRow{Run: jobsdata.Run{RunID: 2, Attempt: 1, Repository: "o/r", HeadSHA: "abc1234", CreatedAt: testNow.Add(-50 * time.Minute),
		CompletedAt: testNow.Add(-40 * time.Minute), Conclusion: jobsdata.Success}, State: jobsdata.Done}}
	v = decode[gen.CommitView](t, get(t, mux, "/api/v1/commits/abc1234"))
	if !v.AllRunsDone || v.RunsDone != 2 {
		t.Errorf("every run finished: %+v", v)
	}
}

func TestGetCommitViewRefusals(t *testing.T) {
	cd := commitFixture()
	for _, tc := range []struct {
		name   string
		mux    *http.ServeMux
		target string
		want   int
	}{
		{"signed out", dataMux(cd, "", nil), "/api/v1/commits/abc1234", http.StatusUnauthorized},
		{"not a commit", dataMux(cd, auth.RoleViewer, nil), "/api/v1/commits/not-a-sha", http.StatusBadRequest},
		{"too short", dataMux(cd, auth.RoleViewer, nil), "/api/v1/commits/abc12", http.StatusBadRequest},
		{"unknown commit", dataMux(&commitData{fakeData: &fakeData{}}, auth.RoleViewer, nil), "/api/v1/commits/abc1234", http.StatusNotFound},
		{"reader without commits", dataMux(&fakeData{}, auth.RoleViewer, nil), "/api/v1/commits/abc1234", http.StatusNotImplemented},
	} {
		if rec := get(t, tc.mux, tc.target); rec.Code != tc.want {
			t.Errorf("%s: status %d, want %d: %s", tc.name, rec.Code, tc.want, rec.Body)
		}
	}
}
