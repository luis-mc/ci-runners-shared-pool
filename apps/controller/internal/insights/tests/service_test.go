package tests

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

func newService(rows []Row) *Service {
	return &Service{Store: &fakeStore{rows: rows}, Now: func() time.Time { return t0.Add(time.Hour) },
		Jobs: fakeJobs{jobs: []insights.JobFact{{RepositoryID: 1, Repository: "o/r"}}}}
}

func TestAnalyseFindingsPerKind(t *testing.T) {
	noAssert := row("empty", "unit", 5, 1, "x", Passed, time.Second)
	noAssert.Assertions = 0
	rows := []Row{
		row("flaky", "unit", 1, 1, "abc", Failed, 2*time.Minute), row("flaky", "unit", 1, 2, "abc", Passed, 2*time.Minute),
		row("dup", "unit", 2, 1, "s", Passed, 3*time.Minute), row("dup", "cov", 2, 1, "s", Passed, 4*time.Minute),
		noAssert,
	}
	got, err := newService(rows).Analyse(context.Background(), insights.Window{From: t0.Add(-time.Hour), To: t0.Add(2 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	by := map[insights.Kind]insights.Finding{}
	for _, f := range got {
		by[f.Kind] = f
		if f.Area != insights.AreaTests || f.Repository != "o/r" || f.RepositoryID != 1 || f.Fix == "" {
			t.Errorf("finding = %+v", f)
		}
	}
	if by[insights.KindTestFlaky].MinutesSavedWeek != 2 || by[insights.KindTestFlaky].Subject != "flaky" {
		t.Errorf("flaky = %+v", by[insights.KindTestFlaky])
	}
	if by[insights.KindTestDuplicate].MinutesSavedWeek != 3 {
		t.Errorf("duplicate = %+v", by[insights.KindTestDuplicate])
	}
	if f := by[insights.KindTestNoAssertions]; f.Subject != "empty" || f.MinutesSavedWeek != 0 {
		t.Errorf("no assertions = %+v", f)
	}
	if f := by[insights.KindTestSlow]; f.Kind == "" {
		t.Error("slow: 17 minutes of tests in the week need a slow finding")
	}
}

func TestReaderPagesEachView(t *testing.T) {
	var rows []Row
	for i := 0; i < 5; i++ {
		rows = append(rows, row("t"+strconv.Itoa(i), "unit", int64(i), 1, "s", Passed, time.Duration(i+1)*time.Second))
	}
	s := newService(rows)
	items, total, next, found, err := s.Tests(context.Background(), Slowest, 1, 7, 2, "")
	if err != nil || total != 5 || next != "2" || !found || len(items) != 2 || items[0].TestID != "t4" {
		t.Fatalf("%v %d %q %v %+v", err, total, next, found, items)
	}
	items, _, next, _, err = s.Tests(context.Background(), Slowest, 1, 7, 2, "4")
	if err != nil || next != "" || len(items) != 1 {
		t.Fatalf("%v %q %+v", err, next, items)
	}
	if _, _, _, _, err = s.Tests(context.Background(), Slowest, 1, 7, 2, "x"); err == nil {
		t.Error("bad cursor")
	}
	_, _, _, found, _ = s.Tests(context.Background(), Flaky, 99, 7, 10, "")
	if found {
		t.Error("no reports for repository 99")
	}
}

// D21: the flaky window is 14 days; the other kinds keep 7.
func TestFlakyWindowIs14Days(t *testing.T) {
	now := t0.Add(time.Hour)
	old := func(r Row, days int) Row { r.FinishedAt = now.AddDate(0, 0, -days); return r }
	rows := []Row{
		old(row("f10", "unit", 1, 1, "abc", Failed, time.Minute), 10), old(row("f10", "unit", 1, 2, "abc", Passed, time.Minute), 10),
		old(row("f20", "unit", 2, 1, "def", Failed, time.Minute), 20), old(row("f20", "unit", 2, 2, "def", Passed, time.Minute), 20),
		old(row("slow10", "unit", 3, 1, "x", Passed, 20*time.Minute), 10),
	}
	svc := newService(rows)
	got, err := svc.Analyse(context.Background(), insights.Window{From: now.AddDate(0, 0, -7), To: now})
	if err != nil {
		t.Fatal(err)
	}
	var flaky []string
	for _, f := range got {
		if f.Kind == insights.KindTestFlaky {
			flaky = append(flaky, f.Subject)
		}
		if f.Kind == insights.KindTestSlow {
			t.Errorf("a test seen 10 days ago is outside the 7 day window: %+v", f)
		}
	}
	if len(flaky) != 1 || flaky[0] != "f10" {
		t.Errorf("flaky = %v, want only f10 (10 days old is inside 14, 20 is outside)", flaky)
	}
	items, _, _, _, err := svc.Tests(context.Background(), Flaky, 0, 7, 50, "")
	if err != nil || len(items) != 1 || items[0].TestID != "f10" {
		t.Errorf("flaky view asked for 7 days = %+v, %v; want f10 from the 14 day window", items, err)
	}
}
