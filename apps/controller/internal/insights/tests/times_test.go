package tests

import (
	"testing"
	"time"
)

// TestOverG1Limit: R5.34, a test is marked when its p95 is over the limit of its kind.
func TestOverG1Limit(t *testing.T) {
	sec := time.Second
	var rows []Row
	for i := 0; i < 20; i++ {
		d := sec
		if i == 19 {
			d = 11 * sec // the 20th run is the p95 (nearest rank 19) only when over: p95 of 20 = 19th value
		}
		rows = append(rows, row("unit-ok", "unit (a)", int64(i), 1, "s", Passed, d))
	}
	rows = append(rows, row("slow-unit", "unit", 1, 1, "s", Passed, 12*sec), row("slow-unit", "unit", 2, 1, "s", Failed, 14*sec))
	e2e := row("e2e-ok", "e2e", 1, 1, "s", Passed, 100*sec)
	e2e.Kind = E2E
	e2eSlow := row("e2e-slow", "e2e", 1, 1, "s", Passed, 130*sec)
	e2eSlow.Kind = E2E
	rows = append(rows, e2e, e2eSlow, row("skipped", "unit", 1, 1, "s", Skipped, 0))
	got := map[string]Insight{}
	for _, in := range Times(rows, "o/r") {
		got[in.TestID] = in
	}
	if got["unit-ok"].OverLimit != 0 || got["unit-ok"].Runs != 20 || got["unit-ok"].P95 != sec {
		t.Errorf("unit-ok = %+v", got["unit-ok"])
	}
	if su := got["slow-unit"]; su.OverLimit != 10*sec || su.Failures != 1 || su.P50 != 13*sec || su.P95 != 14*sec {
		t.Errorf("slow-unit = %+v", su)
	}
	if got["e2e-ok"].OverLimit != 0 || got["e2e-slow"].OverLimit != 2*time.Minute {
		t.Errorf("e2e: %+v %+v", got["e2e-ok"], got["e2e-slow"])
	}
	if _, ok := got["skipped"]; ok {
		t.Error("a skipped test has no time")
	}
	if n := OverLimitCount(rows); n != 2 {
		t.Errorf("over limit = %d, want 2", n)
	}
}

func TestMatrixValuesShareOneJobKey(t *testing.T) {
	rows := []Row{row("a", "build (linux, 20)", 1, 1, "s", Passed, time.Second), row("a", "build (windows, 20)", 2, 1, "s", Passed, time.Second)}
	in := Times(rows, "o/r")
	if len(in) != 1 || in[0].Runs != 2 || in[0].JobIdents[0] != "build" {
		t.Fatalf("%+v", in)
	}
}

func TestSlowTestsNeedFiveMinutesAWeek(t *testing.T) {
	small := []Row{row("a", "j", 1, 1, "s", Passed, 4*time.Minute)}
	if got := SlowTests(small, 5); got != nil {
		t.Fatalf("under 5 min: %+v", got)
	}
	rows := []Row{row("a", "j", 1, 1, "s", Passed, 4*time.Minute), row("b", "j", 1, 1, "s", Passed, 2*time.Minute), row("c", "j", 1, 1, "s", Passed, time.Second)}
	got := SlowTests(rows, 2)
	if len(got) != 2 || got[0].TestID != "a" || got[1].TestID != "b" {
		t.Fatalf("%+v", got)
	}
	if got[0].Excess != 4*time.Minute-10*time.Second {
		t.Errorf("excess = %s", got[0].Excess)
	}
}

func TestPercentileAndMedian(t *testing.T) {
	ds := []time.Duration{4, 1, 3, 2}
	if Median(ds) != 2 /* (2+3)/2 in integer ns */ {
		t.Errorf("median = %d", Median(ds))
	}
	if Percentile(ds, 0.95) != 4 || Percentile(nil, 0.5) != 0 || Median(nil) != 0 {
		t.Error("percentile")
	}
}
