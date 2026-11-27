package tests

import "testing"

// TestZeroOnlyWhenReported: R5.37, a format that does not report assertions is never zero.
func TestZeroOnlyWhenReported(t *testing.T) {
	zero := row("empty", "unit", 1, 1, "s", Passed, 0)
	zero.Assertions = 0
	notReported := row("unknown", "unit", 1, 1, "s", Passed, 0) // Assertions -1
	checked := row("ok", "unit", 1, 1, "s", Passed, 0)
	checked.Assertions = 2
	failedZero := row("failed", "unit", 1, 1, "s", Failed, 0)
	failedZero.Assertions = 0
	got := FindNoAssertions([]Row{zero, notReported, checked, failedZero})
	if len(got) != 1 || got[0].TestID != "empty" {
		t.Fatalf("%+v", got)
	}
}

func TestParsersNeverReportZeroWhenUnknown(t *testing.T) {
	for _, f := range []string{"playwright.json", "vitest.json", "gotest.jsonl", "libtest.jsonl", "report.trx", "ci-timings.json"} {
		_, cases := parseFixture(t, f)
		for _, c := range cases {
			if c.Assertions != -1 {
				t.Errorf("%s: %s has assertions %d", f, c.Name, c.Assertions)
			}
		}
	}
	_, cases := parseFixture(t, "junit.xml")
	zeros := 0
	for _, c := range cases {
		if c.Assertions == 0 {
			zeros++
		}
	}
	if zeros != 1 {
		t.Errorf("junit zeros = %d", zeros)
	}
}
