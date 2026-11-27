package tests

import (
	"os"
	"strings"
	"testing"
	"time"
)

func parseFixture(t *testing.T, file string) (Format, []Case) {
	t.Helper()
	f, err := os.Open("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var cases []Case
	format, err := ParseAny(f, func(_ Format, c Case) error { cases = append(cases, c); return nil })
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	return format, cases
}

// TestEachFormatFromFixture: R5.33, every format is found by content and read.
func TestEachFormatFromFixture(t *testing.T) {
	tests := []struct {
		file   string
		format Format
		n      int
		check  func(t *testing.T, c []Case)
	}{
		{"junit.xml", JUnit, 5, func(t *testing.T, c []Case) {
			if c[0].Assertions != 3 || c[0].Duration != 250*time.Millisecond || c[0].Suite != "pkg.Suite" {
				t.Errorf("adds = %+v", c[0])
			}
			if c[2].Result != Failed || c[3].Retries != 1 || c[3].Result != Passed || c[4].Result != Skipped {
				t.Errorf("results = %+v", c)
			}
		}},
		{"playwright.json", Playwright, 2, func(t *testing.T, c []Case) {
			if c[0].Result != Passed || c[0].Retries != 1 || c[0].Suite != "Login" || c[0].File != "a.spec.ts" || c[0].Assertions != -1 {
				t.Errorf("signs in = %+v", c[0])
			}
			if c[1].Result != Skipped {
				t.Errorf("skips = %+v", c[1])
			}
		}},
		{"vitest.json", Vitest, 3, func(t *testing.T, c []Case) {
			if c[0].Suite != "Button" || c[0].File != "/w/src/a.test.ts" || c[1].Result != Failed || c[2].Result != Skipped {
				t.Errorf("cases = %+v", c)
			}
		}},
		{"gotest.jsonl", GoTest, 3, func(t *testing.T, c []Case) {
			if c[0].Name != "TestA" || c[0].Duration != 20*time.Millisecond || c[1].Name != "TestB/sub" || c[1].Result != Failed {
				t.Errorf("cases = %+v", c)
			}
		}},
		{"libtest.jsonl", Libtest, 3, func(t *testing.T, c []Case) {
			if c[0].Suite != "store::tests" || c[0].Name != "opens" || c[1].Result != Failed || c[2].Result != Skipped {
				t.Errorf("cases = %+v", c)
			}
		}},
		{"report.trx", TRX, 3, func(t *testing.T, c []Case) {
			if c[0].Suite != "App.Tests.CalcTests" || c[0].Name != "Adds" || c[1].Duration != 2500*time.Millisecond || c[2].Result != Skipped {
				t.Errorf("cases = %+v", c)
			}
		}},
		{"ci-run-times.json", CIRunTimes, 2, func(t *testing.T, c []Case) {
			if c[0].Name != "unit" || c[0].Duration != 120*time.Second || c[1].Result != Failed {
				t.Errorf("cases = %+v", c)
			}
		}},
		{"ci-timings.json", CITimings, 2, func(t *testing.T, c []Case) {
			if c[0].File != "apps/web/src/a.test.ts" || c[1].Duration != 40*time.Second {
				t.Errorf("cases = %+v", c)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			format, cases := parseFixture(t, tc.file)
			if format != tc.format || len(cases) != tc.n {
				t.Fatalf("format %s, %d cases; want %s, %d", format, len(cases), tc.format, tc.n)
			}
			tc.check(t, cases)
		})
	}
}

func TestPlainCITimingsMap(t *testing.T) {
	var got []Case
	f, err := ParseAny(strings.NewReader(`{"a/b.test.ts": 12.3, "c.test.ts": 4}`), func(_ Format, c Case) error { got = append(got, c); return nil })
	if err != nil || f != CITimings || len(got) != 2 || got[0].Duration != 12300*time.Millisecond {
		t.Fatalf("%v %v %+v", f, err, got)
	}
}

func TestUnknownContentIsAnError(t *testing.T) {
	for _, in := range []string{"", "hello", `{"a": "b"}`, "<html></html>"} {
		if _, err := ParseAny(strings.NewReader(in), func(Format, Case) error { return nil }); err == nil {
			t.Errorf("%q parsed", in)
		}
	}
}

func TestBrokenFileIsAnError(t *testing.T) {
	for _, in := range []string{`<testsuite><testcase name="a"`, `{"testResults":[{"name":`} {
		if _, err := ParseAny(strings.NewReader(in), func(Format, Case) error { return nil }); err == nil {
			t.Errorf("%q parsed", in)
		}
	}
}

func TestGoTestFromJobLogSkipsNoise(t *testing.T) {
	log := "2026-11-20T10:00:00Z ##[group]run\n{\"Action\":\"pass\",\"Package\":\"p\",\"Test\":\"TestX\",\"Elapsed\":0.5}\nplain line\n"
	var got []Case
	_, err := ParseAny(strings.NewReader(log[strings.Index(log, "{"):]), func(_ Format, c Case) error { got = append(got, c); return nil })
	if err != nil || len(got) != 1 || got[0].Name != "TestX" {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestNormaliserStripsDurationsAndSeeds(t *testing.T) {
	n := DefaultNormaliser{}
	a := n.TestID(GoTest, Case{Suite: "p", Name: "TestX (0.12s) seed=9f2a"})
	b := n.TestID(GoTest, Case{Suite: "p", Name: "TestX [35ms]"})
	if a != b || a != "gotest|p|TestX" {
		t.Fatalf("%q %q", a, b)
	}
	if n.Kind("e2e (chromium)", "") != E2E || n.Kind("Integration tests", "") != Integration || n.Kind("unit", "a_test.go") != Unit {
		t.Fatal("kinds")
	}
	if Limit(Unit) != 10*time.Second || Limit(Integration) != time.Minute || Limit(E2E) != 2*time.Minute {
		t.Fatal("limits")
	}
}
