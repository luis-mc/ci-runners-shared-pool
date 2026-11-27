package guardrails

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/tests"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/workflows"
)

var now = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

type fakeStore struct {
	status []Status
	own    map[insights.Guardrail]Entry
}

func (f *fakeStore) SaveReport(context.Context, int64, int64, string, Report, time.Time) error {
	return nil
}
func (f *fakeStore) SaveOwn(_ context.Context, _ int64, e Entry, _ time.Time) error {
	f.own[e.Guardrail] = e
	return nil
}
func (f *fakeStore) Status(context.Context, int64) ([]Status, error) { return f.status, nil }

type fakeTests struct{ rows []tests.Row }

func (f fakeTests) ReplaceReport(context.Context, int64, []tests.Row) error { return nil }
func (f fakeTests) Between(_ context.Context, _ int64, _ insights.Window, limit, off int) ([]tests.Row, error) {
	if off >= len(f.rows) {
		return nil, nil
	}
	return f.rows[off:min(off+limit, len(f.rows))], nil
}

type fakeWorkflows struct{ results []workflows.Result }

func (f fakeWorkflows) Save(context.Context, workflows.File, []workflows.Result) error { return nil }
func (f fakeWorkflows) Latest(context.Context, int64) (string, []workflows.Result, error) {
	return "abc", f.results, nil
}
func (f fakeWorkflows) ForCommit(context.Context, int64, string) ([]workflows.Result, error) {
	return f.results, nil
}

type fakeJobs struct{}

func (fakeJobs) JobsBetween(context.Context, insights.Window, int64) ([]insights.JobFact, error) {
	return []insights.JobFact{{RepositoryID: 1, Repository: "o/r"}}, nil
}

func trow(id string, res tests.Result, d time.Duration, retries int) tests.Row {
	return tests.Row{RepositoryID: 1, TestID: id, Kind: tests.Unit, FinishedAt: now,
		Case: tests.Case{Name: id, File: id + "_test.go", Result: res, Duration: d, Retries: retries, Assertions: -1}}
}

func service(f *fakeStore, rows []tests.Row, checks []workflows.Result) *Service {
	return &Service{Store: f, Tests: fakeTests{rows}, Workflows: fakeWorkflows{checks}, Jobs: fakeJobs{}, Now: func() time.Time { return now }}
}

var window = insights.Window{From: now.Add(-7 * 24 * time.Hour), To: now}

// TestOwnChecksAndSources: R5.32, crsp fills what the guard report does not cover; each value has its source.
func TestOwnChecksAndSources(t *testing.T) {
	f := &fakeStore{own: map[insights.Guardrail]Entry{}, status: []Status{{RepositoryID: 1, Guardrails: []StatusEntry{
		{Entry: Entry{Guardrail: "G2", State: Broken, Count: 1, Breaks: []Break{{File: "ci.yml", Line: 4, Message: "no timeout"}}}, Source: FromGuardReport, ReportedAt: now.Add(-time.Hour)},
		{Entry: Entry{Guardrail: "G4", State: Pass}, Source: FromGuardReport, ReportedAt: now.Add(-time.Hour)},
		{Entry: Entry{Guardrail: "G5", State: Pass}, Source: FromGuardReport, ReportedAt: now.Add(-30 * 24 * time.Hour)}, // too old
	}}}}
	rows := []tests.Row{trow("slow", tests.Passed, 12*time.Second, 0), trow("fast", tests.Passed, time.Second, 2)}
	checks := []workflows.Result{
		{Check: workflows.CheckRetry, Status: workflows.OK, Guardrail: "G3"},
		{Check: workflows.CheckSlowTestsOnPR, Status: workflows.Fail, Guardrail: "G6", Job: "mutation", Line: 9, Detail: "mutation runs on pull requests"},
		{Check: workflows.CheckTimeout, Status: workflows.Fail, Guardrail: "G2", Detail: "not used: the report covers G2"},
	}
	got, err := service(f, rows, checks).Analyse(context.Background(), window)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.own["G2"]; ok {
		t.Error("G2 is covered by a fresh guard report: no own state")
	}
	if _, ok := f.own["G4"]; ok {
		t.Error("G4 is covered by a fresh guard report: no own state")
	}
	want := map[insights.Guardrail]State{"G1": Broken, "G3": Broken, "G5": Unknown, "G6": Broken, "G7": Pass}
	for g, st := range want {
		if f.own[g].State != st {
			t.Errorf("%s = %s, want %s (%+v)", g, f.own[g].State, st, f.own[g])
		}
	}
	if e := f.own["G3"]; e.Count != 1 || !strings.Contains(e.Breaks[0].Message, "2 retries") {
		t.Errorf("G3 = %+v", e)
	}
	if e := f.own["G6"]; e.Breaks[0].Line != 9 || e.Breaks[0].File != "mutation" {
		t.Errorf("G6 = %+v", e)
	}
	kinds := map[insights.Kind]insights.Finding{}
	for _, fi := range got {
		kinds[fi.Kind] = fi
	}
	for _, k := range []insights.Kind{insights.KindGuardrailG1, insights.KindGuardrailG2, insights.KindGuardrailG3, insights.KindGuardrailG6} {
		fi, ok := kinds[k]
		if !ok || fi.Area != insights.AreaGuardrails || fi.Subject != "o/r" || fi.Repository != "o/r" || fi.Fix == "" || fi.Guardrail == "" {
			t.Errorf("finding %s = %+v", k, fi)
		}
	}
	if len(got) != 4 {
		t.Errorf("%d findings, want 4 (G4 passes, G5 and G7 do not break)", len(got))
	}
	if !strings.Contains(string(kinds[insights.KindGuardrailG2].Evidence), `"source":"guard_report"`) ||
		!strings.Contains(string(kinds[insights.KindGuardrailG1].Evidence), `"source":"crsp"`) {
		t.Errorf("sources: %s / %s", kinds[insights.KindGuardrailG2].Evidence, kinds[insights.KindGuardrailG1].Evidence)
	}
}

func TestWithoutDataOwnStatesAreUnknownOrG7Breaks(t *testing.T) {
	f := &fakeStore{own: map[insights.Guardrail]Entry{}}
	got, err := service(f, nil, nil).Analyse(context.Background(), window)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range []insights.Guardrail{"G1", "G2", "G3", "G4", "G5", "G6"} {
		if f.own[g].State != Unknown {
			t.Errorf("%s = %s, want unknown", g, f.own[g].State)
		}
	}
	if f.own["G7"].State != Broken || len(got) != 1 || got[0].Kind != insights.KindGuardrailG7 {
		t.Errorf("G7 = %+v, findings %+v", f.own["G7"], got)
	}
}

func TestSkippedChecksStayUnknown(t *testing.T) {
	e := OwnCheck("G2", nil, []workflows.Result{{Guardrail: "G2", Status: workflows.Skip, Detail: "file unreadable"}})
	if e.State != Unknown {
		t.Fatalf("%+v", e)
	}
}

func TestBreaksAreCappedButCounted(t *testing.T) {
	var rows []tests.Row
	for i := 0; i < 25; i++ {
		rows = append(rows, trow(string(rune('a'+i)), tests.Passed, time.Minute, 0))
	}
	e := OwnCheck("G1", rows, nil)
	if e.Count != 25 || len(e.Breaks) != maxBreaks || e.State != Broken {
		t.Fatalf("count %d, shown %d, %s", e.Count, len(e.Breaks), e.State)
	}
}
