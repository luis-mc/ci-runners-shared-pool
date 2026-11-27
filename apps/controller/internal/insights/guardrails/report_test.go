package guardrails

import (
	"os"
	"strings"
	"testing"
)

func TestParseGuardJSON(t *testing.T) {
	b, err := os.ReadFile("testdata/guard-report.json")
	if err != nil {
		t.Fatal(err)
	}
	p := ReportParser{}
	if !p.Detect(b[:min(len(b), 4096)]) {
		t.Fatal("not detected by content")
	}
	rep, err := p.Parse(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Mode != "block" || len(rep.Entries) != 5 || rep.KnownCount != 31 {
		t.Fatalf("report = %+v", rep)
	}
	g := map[string]Entry{}
	for _, e := range rep.Entries {
		g[string(e.Guardrail)] = e
	}
	if g["G1"].State != Pass || g["G1"].Count != 0 {
		t.Errorf("G1 = %+v", g["G1"])
	}
	if e := g["G2"]; e.State != Broken || e.Count != 2 || len(e.Breaks) != 2 || e.Breaks[0].Line != 12 || e.Breaks[0].File != ".github/workflows/ci.yml" {
		t.Errorf("G2 = %+v", e)
	}
	if e := g["G3"]; e.State != Off || e.Reason != "flaky vendor test, tracked in T-12" {
		t.Errorf("G3 = %+v", e)
	}
	if _, ok := g["G6"]; ok {
		t.Error("a guardrail the report does not hold stays absent")
	}
}

func TestKnownCountForms(t *testing.T) {
	cases := map[string]int{
		`{"mode":"warn","results":{"G4":{"state":"pass"}}}`:                                                       -1,
		`{"mode":"warn","results":{"G4":{"state":"pass"}},"known":["a","b"]}`:                                     2,
		`{"mode":"warn","results":{"G4":{"state":"pass","known_count":3},"G5":{"state":"pass","known_count":4}}}`: 7,
	}
	for in, want := range cases {
		rep, err := ReportParser{}.Parse(strings.NewReader(in))
		if err != nil || rep.KnownCount != want {
			t.Errorf("%s: %d, %v; want %d", in, rep.KnownCount, err, want)
		}
	}
}

func TestParseRejectsOtherJSON(t *testing.T) {
	p := ReportParser{}
	for _, in := range []string{`{"testResults":[]}`, `[]`, `<testsuite/>`, ``} {
		if p.Detect([]byte(in)) {
			t.Errorf("%q detected", in)
		}
	}
	for _, in := range []string{`{"results":{"G9":{"state":"pass"}}}`, `{"results":{"G1":{"state":"maybe"}}}`, `{"mode":"x"}`, `{"results":`} {
		if _, err := p.Parse(strings.NewReader(in)); err == nil {
			t.Errorf("%q parsed", in)
		}
	}
}
