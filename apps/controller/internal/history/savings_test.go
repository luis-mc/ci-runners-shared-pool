package history

import (
	"context"
	"testing"
	"time"
)

type fakeSource []Job

func (f fakeSource) FinishedPoolJobs(context.Context, time.Time, time.Time) ([]Job, error) {
	return f, nil
}

type fakeRates struct{ t Table }

func (f fakeRates) Get(context.Context) (Table, error) { return f.t, nil }

func svc(jobs ...Job) *Service {
	return &Service{
		Source: fakeSource(jobs), Rates: fakeRates{Shipped()}, Loc: time.UTC,
		Now: func() time.Time { return time.Date(2026, 11, 25, 15, 0, 0, 0, time.UTC) },
	}
}

func TestRoundUpMinutes(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		want int
	}{{0, 0}, {time.Second, 1}, {time.Minute, 1}, {61 * time.Second, 2}} {
		if got := RoundUpMinutes(c.d); got != c.want {
			t.Errorf("%v: got %d want %d", c.d, got, c.want)
		}
	}
}

func TestMoneyUsesMatchingRate(t *testing.T) {
	rep, err := svc(
		Job{Repository: "o/a", OS: "linux", Machine: "m1", Run: 90 * time.Second},   // 2 min * 0.008
		Job{Repository: "o/a", OS: "windows", Machine: "m2", Run: 10 * time.Minute}, // 10 * 0.016
		Job{Repository: "o/b", OS: "macos", Machine: "m3", Run: time.Minute},        // 1 * 0.08
	).Report(context.Background(), RangeToday, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total.Minutes != 13 || rep.Total.Money != 0.256 {
		t.Fatalf("total %+v", rep.Total)
	}
	if len(rep.ByOS) != 3 || rep.ByOS[0].Key != "windows" || rep.ByOS[0].Money != 0.16 {
		t.Fatalf("by os %+v", rep.ByOS)
	}
	if rep.SourceURL == "" || rep.CopiedOn == "" {
		t.Fatal("the report must carry the rate source and date")
	}
}

func TestMachineSizeChangesTheRate(t *testing.T) {
	s := svc(Job{Repository: "o/a", OS: "linux", Machine: "arm1", Run: time.Minute})
	tab := Shipped()
	tab.MachineSizes = map[string]string{"arm1": "arm"}
	s.Rates = fakeRates{tab}
	rep, _ := s.Report(context.Background(), RangeToday, time.Time{}, time.Time{})
	if rep.Total.Money != 0.005 {
		t.Fatalf("money %v", rep.Total.Money)
	}
}

func TestPublicRepoSavesNoMoney(t *testing.T) {
	rep, _ := svc(
		Job{Repository: "o/pub", Public: true, OS: "linux", Machine: "m1", Run: 5 * time.Minute},
		Job{Repository: "o/priv", OS: "linux", Machine: "m1", Run: time.Minute},
	).Report(context.Background(), RangeToday, time.Time{}, time.Time{})
	if rep.Total.Minutes != 6 || rep.Total.PublicMinutes != 5 || rep.Total.Money != 0.008 {
		t.Fatalf("total %+v", rep.Total)
	}
	if rep.ByRepository[0].Key != "o/pub" || rep.ByRepository[0].Money != 0 {
		t.Fatalf("public row %+v", rep.ByRepository[0])
	}
	if rep.Note == "" {
		t.Fatal("the report must say public repositories save no money")
	}
}

func TestReportRangesAndGroups(t *testing.T) {
	s := svc()
	for name, days := range map[string]int{RangeToday: 0, Range7Days: 6, Range30Days: 29} {
		f, to, err := s.Window(name, time.Time{}, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		want := time.Date(2026, 11, 25-days, 0, 0, 0, 0, time.UTC)
		if !f.Equal(want) || !to.Equal(s.Now()) {
			t.Errorf("%s: %v to %v", name, f, to)
		}
	}
	f, to, err := s.Window(RangeCustom, time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC), time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC))
	if err != nil || !f.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) || !to.Equal(time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("custom %v %v %v", f, to, err)
	}
	for _, bad := range []struct{ a, b time.Time }{{}, {to, f}, {f, f.AddDate(2, 0, 0)}} {
		if _, _, err := s.Window(RangeCustom, bad.a, bad.b); err == nil {
			t.Errorf("custom %v to %v must be refused", bad.a, bad.b)
		}
	}
	if _, _, err := s.Window("year", time.Time{}, time.Time{}); err == nil {
		t.Error("an unknown range must be refused")
	}
}

func TestUnpricedOSAddsNoMoney(t *testing.T) {
	s := svc(Job{Repository: "o/a", OS: "freebsd", Machine: "m", Run: time.Minute})
	rep, _ := s.Report(context.Background(), RangeToday, time.Time{}, time.Time{})
	if rep.NotPriced != 1 || rep.Total.Money != 0 || rep.Total.Minutes != 1 {
		t.Fatalf("%+v", rep)
	}
}

func TestGroupedJobsCountLikeSingleJobs(t *testing.T) {
	rep, err := svc(Job{Repository: "o/a", OS: "linux", Machine: "m1", Run: 2 * time.Minute, Count: 1000}).
		Report(context.Background(), RangeToday, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total.Jobs != 1000 || rep.Total.Minutes != 2000 || rep.Total.Money != 16 || rep.ByRepository[0].Jobs != 1000 {
		t.Fatalf("%+v", rep.Total)
	}
}
