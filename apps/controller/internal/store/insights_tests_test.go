package store

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
	instests "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/tests"
)

func openInsightTests(t *testing.T) *InsightTests {
	t.Helper()
	s, err := OpenForTests(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s.InsightTests()
}

func testRow(repo, run, report int64, name string, res instests.Result, at time.Time) instests.Row {
	return instests.Row{RepositoryID: repo, RunID: run, RunAttempt: 1, JobID: 7, JobIdent: "o/r ci.yml unit", HeadSHA: "abc",
		ReportID: report, Format: instests.JUnit, TestID: "junit|s|" + name, Kind: instests.Unit, FinishedAt: at,
		Case: instests.Case{Suite: "s", Name: name, File: "a_test.go", Result: res, Duration: 1500 * time.Millisecond, Retries: 1, Assertions: -1}}
}

func TestIntegrationInsightTestsReplaceAndRead(t *testing.T) {
	ctx := context.Background()
	st := openInsightTests(t)
	at := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	if err := st.ReplaceReport(ctx, 5, []instests.Row{testRow(1, 10, 5, "a", instests.Passed, at), testRow(1, 10, 5, "b", instests.Failed, at)}); err != nil {
		t.Fatal(err)
	}
	// the same report again replaces its rows; another report and another repository stay
	if err := st.ReplaceReport(ctx, 6, []instests.Row{testRow(1, 11, 6, "c", instests.Passed, at), testRow(2, 11, 6, "z", instests.Passed, at)}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceReport(ctx, 5, []instests.Row{testRow(1, 10, 5, "a2", instests.Skipped, at)}); err != nil {
		t.Fatal(err)
	}
	w := insights.Window{From: at.Add(-time.Hour), To: at.Add(time.Hour)}
	rows, err := st.Between(ctx, 1, w, 10, 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("%v %+v", err, rows)
	}
	got := rows[1] // ids run in insert order: c came first, then a2
	if got.Name != "a2" || got.Result != instests.Skipped || got.Duration != 1500*time.Millisecond || got.Retries != 1 ||
		got.Assertions != -1 || !got.FinishedAt.Equal(at) || got.HeadSHA != "abc" || got.Format != instests.JUnit || got.ReportID != 5 {
		t.Fatalf("row = %+v", got)
	}
	page, err := st.Between(ctx, 1, w, 1, 1)
	if err != nil || len(page) != 1 || page[0].Name != "a2" {
		t.Fatalf("page %v %+v", err, page)
	}
	out, err := st.Between(ctx, 1, insights.Window{From: at.Add(time.Hour), To: at.Add(2 * time.Hour)}, 10, 0)
	if err != nil || len(out) != 0 {
		t.Fatalf("outside the window: %v %+v", err, out)
	}
}

func TestIntegrationInsightTestsJobLogReplacedPerJob(t *testing.T) {
	ctx := context.Background()
	st := openInsightTests(t)
	at := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	log := func(job int64, name string) instests.Row {
		r := testRow(1, 10, 0, name, instests.Passed, at)
		r.JobID, r.Format = job, instests.GoTest
		return r
	}
	for _, rows := range [][]instests.Row{{log(1, "old"), log(2, "other job")}, {log(1, "new")}} {
		if err := st.ReplaceReport(ctx, 0, rows); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := st.Between(ctx, 1, insights.Window{From: at.Add(-time.Hour), To: at.Add(time.Hour)}, 10, 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("%v %+v", err, rows)
	}
	names := rows[0].Name + "," + rows[1].Name
	if names != "other job,new" {
		t.Fatalf("names = %s", names)
	}
}
