package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/insights"
	"github.com/luis-mc/ci-runners-shared-pool/internal/insights/actions"
	"github.com/luis-mc/ci-runners-shared-pool/internal/insights/capacity"
	"github.com/luis-mc/ci-runners-shared-pool/internal/insights/timeline"
	"github.com/luis-mc/ci-runners-shared-pool/internal/insights/waits"
	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsize"
)

type fakeFindings struct {
	got   insights.Filter
	items []insights.StoredFinding
}

func (f *fakeFindings) Sync(context.Context, []insights.Kind, []insights.Finding, time.Time) (insights.SyncResult, error) {
	return insights.SyncResult{}, nil
}

func (f *fakeFindings) List(_ context.Context, fl insights.Filter) ([]insights.StoredFinding, int, string, error) {
	f.got = fl
	return f.items, len(f.items), "", nil
}

func (f *fakeFindings) Get(_ context.Context, id int64) (insights.StoredFinding, error) {
	for _, it := range f.items {
		if it.ID == id {
			return it, nil
		}
	}
	return insights.StoredFinding{}, ErrInsightNotFound
}

func (f *fakeFindings) Summary(context.Context) (insights.Summary, error) {
	return insights.Summary{Open: 2, MinutesSavedWeek: 90, EnoughData: true}, nil
}

type fakePlans struct {
	err   error
	state actions.State
	by    string
}

func (p *fakePlans) Plan(_ context.Context, id int64, by string) (actions.Plan, error) {
	if p.err != nil {
		return actions.Plan{}, p.err
	}
	p.by = by
	return actions.Plan{ID: "p1", FindingID: id, Action: insights.ActionMachineSlots, State: actions.Draft,
		Changes: []actions.Change{{Target: "m1", Field: "job_slots", From: 2, To: 3, Words: "Job slots of m1: 2 to 3"}}}, nil
}

func (p *fakePlans) Get(context.Context, string) (actions.Plan, error) {
	return actions.Plan{ID: "p1", State: p.state}, nil
}

func (p *fakePlans) Start(_ context.Context, id, by string) (actions.Plan, error) {
	if p.state != actions.Draft {
		return actions.Plan{}, ErrPlanNotDraft
	}
	p.by = by
	return actions.Plan{ID: id, State: actions.Running}, nil
}

type fakeWaits struct{ cap time.Duration }

func (w *fakeWaits) SplitBy(_ context.Context, _ int, _ waits.Group, _ int64, c time.Duration) ([]waits.Row, error) {
	w.cap = c
	return []waits.Row{{Key: "2026-11-19", Jobs: 4, Wait: waits.Stats{Known: 4, Total: time.Minute, P50: 10 * time.Second, P95: 20 * time.Second},
		HoldReasons: map[waits.HoldReason]int{waits.HoldImagePull: 1}}}, nil
}

type fakeTimeline struct{ got insights.Window }

func (f *fakeTimeline) Timeline(_ context.Context, w insights.Window) ([]timeline.Lane, error) {
	f.got = w
	return []timeline.Lane{{RunnerName: "box-1", Origin: insights.OriginUnknown}}, nil
}

func insightsMux(role auth.Role, a *InsightsArea) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Authn: asRole(role), Insights: a})
	return mux
}

func insightBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return m
}

// R5.42: viewers read, only admins change, and the answers have the contract's shapes.
func TestRolesAndShapes(t *testing.T) {
	fixed := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	ff := &fakeFindings{items: []insights.StoredFinding{{ID: 7, Finding: insights.Finding{Kind: insights.KindPoolSize, Scope: insights.ScopePool,
		Area: insights.AreaPool, Title: "t", Fix: "f", MinutesSavedWeek: 45, Evidence: json.RawMessage(`{"p95":540}`), Action: insights.ActionMachineSlots}, FirstSeen: fixed, LastSeen: fixed}}}
	pl := &fakePlans{state: actions.Draft}
	area := &InsightsArea{Findings: ff, Plans: pl, Waits: &fakeWaits{}, Timeline: &fakeTimeline{}, Now: func() time.Time { return fixed }}

	rec := do(insightsMux(auth.RoleViewer, area), "GET", "/api/v1/insights/findings?area=pool&state=resolved&limit=10", "")
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	if ff.got.Area != insights.AreaPool || !ff.got.Resolved || ff.got.Limit != 10 {
		t.Errorf("filter not passed: %+v", ff.got)
	}
	items := insightBody(t, rec)["items"].([]any)
	f := items[0].(map[string]any)
	if f["kind"] != "pool.size" || f["action_kind"] != "machine_slots" || f["evidence"].(map[string]any)["p95"] != float64(540) {
		t.Errorf("finding shape: %v", f)
	}
	if rec := do(insightsMux(auth.RoleViewer, area), "GET", "/api/v1/insights/findings/99", ""); rec.Code != 404 {
		t.Errorf("unknown finding: %d", rec.Code)
	}
	if rec := do(insightsMux(auth.RoleViewer, area), "GET", "/api/v1/insights/summary", ""); rec.Code != 200 || insightBody(t, rec)["minutes_saved_week"] != float64(90) {
		t.Errorf("summary: %d %s", rec.Code, rec.Body)
	}

	if rec := do(insightsMux(auth.RoleViewer, area), "POST", "/api/v1/insights/findings/7/plans", ""); rec.Code != 403 {
		t.Errorf("viewer creates plan: %d", rec.Code)
	}
	if rec := do(insightsMux(auth.RoleAdmin, area), "POST", "/api/v1/insights/findings/7/plans", ""); rec.Code != 201 || pl.by != "tester" {
		t.Errorf("admin creates plan: %d by %q", rec.Code, pl.by)
	}
	if rec := do(insightsMux(auth.RoleViewer, area), "POST", "/api/v1/insights/plans/p1/start", ""); rec.Code != 403 {
		t.Errorf("viewer starts plan: %d", rec.Code)
	}
	if rec := do(insightsMux(auth.RoleAdmin, area), "POST", "/api/v1/insights/plans/p1/start", ""); rec.Code != 202 {
		t.Errorf("admin starts plan: %d", rec.Code)
	}
	pl.state = actions.Running
	if rec := do(insightsMux(auth.RoleAdmin, area), "POST", "/api/v1/insights/plans/p1/start", ""); rec.Code != 409 {
		t.Errorf("start twice: %d", rec.Code)
	}
	pl.err = actions.ErrNoAction
	if rec := do(insightsMux(auth.RoleAdmin, area), "POST", "/api/v1/insights/findings/7/plans", ""); rec.Code != 409 {
		t.Errorf("no action: %d", rec.Code)
	}
}

// R5.14: the hold cap comes from the setting, the default 120 s; unknown is never counted as zero.
func TestWaitSplitHoldCapAndRanges(t *testing.T) {
	w := &fakeWaits{}
	area := &InsightsArea{Waits: w}
	mux := insightsMux(auth.RoleViewer, area)
	rec := do(mux, "GET", "/api/v1/insights/waits?days=14&group=day", "")
	if rec.Code != 200 || w.cap != 120*time.Second {
		t.Fatalf("waits: %d cap %v", rec.Code, w.cap)
	}
	row := insightBody(t, rec)["rows"].([]any)[0].(map[string]any)
	if row["hold"].(map[string]any)["known"] != float64(0) || row["hold"].(map[string]any)["p50_seconds"] != nil {
		t.Errorf("unknown hold shown as a number: %v", row["hold"])
	}
	area.HoldCap = func(context.Context) time.Duration { return 5 * time.Minute }
	do(mux, "GET", "/api/v1/insights/waits", "")
	if w.cap != 5*time.Minute {
		t.Errorf("cap %v", w.cap)
	}
	for _, q := range []string{"days=3", "group=week"} {
		if rec := do(mux, "GET", "/api/v1/insights/waits?"+q, ""); rec.Code != 400 {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
}

// R5.21: at most 24 hours; the last 24 hours without from and to.
func TestTimelineSpan(t *testing.T) {
	fixed := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	tl := &fakeTimeline{}
	mux := insightsMux(auth.RoleViewer, &InsightsArea{Timeline: tl, Now: func() time.Time { return fixed }})
	rec := do(mux, "GET", "/api/v1/insights/timeline", "")
	if rec.Code != 200 || !tl.got.To.Equal(fixed) || tl.got.To.Sub(tl.got.From) != 24*time.Hour {
		t.Fatalf("default window: %d %+v", rec.Code, tl.got)
	}
	if lane := insightBody(t, rec)["lanes"].([]any)[0].(map[string]any); lane["origin"] != "other" {
		t.Errorf("unknown origin shown as %v", lane["origin"])
	}
	for _, q := range []string{"from=2026-11-18T00:00:00Z&to=2026-11-20T00:00:00Z", "from=2026-11-20T00:00:00Z&to=2026-11-19T00:00:00Z"} {
		if rec := do(mux, "GET", "/api/v1/insights/timeline?"+q, ""); rec.Code != 400 {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
}

// A service that is not wired answers 501, like an unbuilt operation.
func TestUnwiredServiceAnswers501(t *testing.T) {
	for _, a := range []*InsightsArea{nil, {}} {
		mux := insightsMux(auth.RoleViewer, a)
		for _, p := range []string{"/api/v1/insights/findings", "/api/v1/insights/summary", "/api/v1/insights/digests", "/api/v1/insights/pool-sizing", "/api/v1/jobs/1/resources"} {
			if rec := do(mux, "GET", p, ""); rec.Code != http.StatusNotImplemented {
				t.Errorf("%s: %d", p, rec.Code)
			}
		}
	}
}

// R5.39, D7: the job fields come from the runner info; a job run elsewhere is told apart.
func TestRunnerInfoOnJob(t *testing.T) {
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	j := withRunnerInfo(toJobItem(jobsdataJob(5)), JobRunnerInfo{Origin: "other", Group: "ops", Labels: []string{"gpu"}, StepsSource: "rest", CheckedAt: at})
	if j.RunnerOrigin == nil || string(*j.RunnerOrigin) != "other" || *j.RunnerGroup != "ops" || (*j.RunnerLabels)[0] != "gpu" ||
		string(*j.StepsSource) != "rest" || !j.CheckedAt.Equal(at) {
		t.Errorf("fields: %+v", j)
	}
	if j := withRunnerInfo(toJobItem(jobsdataJob(6)), JobRunnerInfo{}); j.RunnerOrigin != nil || j.StepsSource != nil || j.CheckedAt != nil {
		t.Errorf("unknown must stay absent: %+v", j)
	}
	if (JobRunnerInfo{Origin: "crsp"}).elsewhere() || (JobRunnerInfo{}).elsewhere() || !(JobRunnerInfo{Origin: "hosted"}).elsewhere() {
		t.Error("elsewhere is other or hosted only")
	}
}

func jobsdataJob(id int64) jobsdata.Job {
	return jobsdata.Job{ID: id, Repository: "o/r", RunID: 1, Attempt: 1}
}

type fakeDismiss struct {
	err error
	id  int64
	at  time.Time
}

func (d *fakeDismiss) Dismiss(_ context.Context, id int64, now time.Time) (time.Time, error) {
	d.id, d.at = id, now
	return now.Add(insights.DismissFor), d.err
}

// D17: dismissing is an admin change, hides for 7 days and answers the finding with when it shows again.
func TestDismissRoute(t *testing.T) {
	fixed := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	ff := &fakeFindings{items: []insights.StoredFinding{{ID: 7, Finding: insights.Finding{Kind: insights.KindWaitHold, Scope: insights.ScopePool,
		Area: insights.AreaWaits, Title: "t", Fix: "f"}, FirstSeen: fixed, LastSeen: fixed, DismissedUntil: fixed.Add(insights.DismissFor)}}}
	dm := &fakeDismiss{}
	area := &InsightsArea{Findings: ff, Dismiss: dm, Now: func() time.Time { return fixed }}
	if rec := do(insightsMux(auth.RoleViewer, area), "POST", "/api/v1/insights/findings/7/dismiss", ""); rec.Code != 403 {
		t.Errorf("a viewer dismissed: %d", rec.Code)
	}
	rec := do(insightsMux(auth.RoleAdmin, area), "POST", "/api/v1/insights/findings/7/dismiss", "")
	if rec.Code != 200 || dm.id != 7 || !dm.at.Equal(fixed) {
		t.Fatalf("admin dismiss: %d %s (%d %v)", rec.Code, rec.Body, dm.id, dm.at)
	}
	if got := insightBody(t, rec)["dismissed_until"]; got != "2026-11-27T12:00:00Z" {
		t.Errorf("dismissed_until = %v", got)
	}
	dm.err = insights.ErrNotFound
	if rec := do(insightsMux(auth.RoleAdmin, area), "POST", "/api/v1/insights/findings/8/dismiss", ""); rec.Code != 404 {
		t.Errorf("unknown: %d", rec.Code)
	}
	dm.err = insights.ErrResolved
	if rec := do(insightsMux(auth.RoleAdmin, area), "POST", "/api/v1/insights/findings/7/dismiss", ""); rec.Code != 409 {
		t.Errorf("resolved: %d", rec.Code)
	}
	if rec := do(insightsMux(auth.RoleAdmin, &InsightsArea{}), "POST", "/api/v1/insights/findings/7/dismiss", ""); rec.Code != 501 {
		t.Errorf("not wired: %d", rec.Code)
	}
}

type fakeResources struct {
	run   capacity.RunPeak
	key   jobsize.Summary
	found bool
}

func (f *fakeResources) Add(context.Context, int64, string, string, []capacity.ResourceSample, bool, string) error {
	return nil
}

func (f *fakeResources) Job(context.Context, int64) ([]capacity.ResourceSample, capacity.ResourceSummary, error) {
	return []capacity.ResourceSample{{At: time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC), CPUMillis: 1500, MemoryBytes: 1 << 20, Known: true}},
		capacity.ResourceSummary{JobID: 5, Samples: 1, CPUP95Millis: 1500, CPUPeakMillis: 1500, MemP95: 1 << 20, MemPeak: 1 << 20}, nil
}

func (f *fakeResources) Peaks(context.Context, int64) (capacity.RunPeak, jobsize.Summary, bool, error) {
	return f.run, f.key, f.found, nil
}

// OPS66 R5.60, R5.63: getJobResources carries the measured peaks, the flags and the summary of the job key;
// below 5 good runs measured is false and the p90 are still the numbers of the runs there are; a job with no
// final batch has no measured object; a controller with no peak reader leaves the field out.
func TestJobResourcesMeasured(t *testing.T) {
	f := &fakeResources{found: true,
		run: capacity.RunPeak{JobID: 5, Runner: "box-1", Source: "cgroup", PeakCPUMillis: 2500, PeakMemoryBytes: 3 << 30, Samples: 40, DurationMS: 41000, OOMKilled: true, Good: false},
		key: jobsize.Summary{Runs: 3, CPUP90Millis: 2000, MemoryP90Bytes: 2 << 30}}
	area := &InsightsArea{Resources: f, Peaks: f}
	mux := insightsMux(auth.RoleViewer, area)
	rec := do(mux, "GET", "/api/v1/jobs/5/resources", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	m, ok := insightBody(t, rec)["measured"].(map[string]any)
	if !ok {
		t.Fatalf("no measured: %s", rec.Body)
	}
	key := m["key"].(map[string]any)
	if m["peak_cpu_cores"] != 2.5 || m["peak_memory_bytes"] != float64(3<<30) || m["sample_count"] != float64(40) || m["duration_seconds"] != 41.0 ||
		m["oom_killed"] != true || m["cut_short"] != false || m["good"] != false || m["source"] != "cgroup" || m["runner"] != "box-1" {
		t.Errorf("measured %v", m)
	}
	if key["runs"] != float64(3) || key["measured"] != false || key["cpu_p90_cores"] != 2.0 || key["memory_p90_bytes"] != float64(2<<30) {
		t.Errorf("key %v", key)
	}
	f.key.Runs, f.key.Measured = 5, true
	key = insightBody(t, do(mux, "GET", "/api/v1/jobs/5/resources", ""))["measured"].(map[string]any)["key"].(map[string]any)
	if key["measured"] != true {
		t.Errorf("measured from 5 runs: %v", key)
	}
	f.found = false
	if _, has := insightBody(t, do(mux, "GET", "/api/v1/jobs/5/resources", ""))["measured"]; has {
		t.Error("a running job has no measured object")
	}
	area.Peaks = nil
	f.found = true
	if _, has := insightBody(t, do(mux, "GET", "/api/v1/jobs/5/resources", ""))["measured"]; has {
		t.Error("no peak reader, no measured field")
	}
}
