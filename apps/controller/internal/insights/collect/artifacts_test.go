package collect

import (
	"context"
	"strings"
	"testing"
)

const (
	junitXML   = `<?xml version="1.0"?><testsuites><testsuite name="a" tests="1"><testcase name="t" time="0.1"/></testsuite></testsuites>`
	guardJSON  = `{"mode":"enforce","results":{"G1":{"state":"pass","reason":"","count":0,"breaks":[]}}}`
	playwright = `{"config":{},"suites":[{"title":"a"}],"stats":{}}`
	goTestJSON = `{"Time":"2026-11-20T10:00:00Z","Action":"run","Package":"p","Test":"TestA"}` + "\n"
)

// R5.3: artifacts are chosen by the content of their files, never their names; raw files are stored redacted
// and indexed; the guard report artifact is read first; what is not a report leaves no trace.
func TestReportArtifactsByContentWithinCaps(t *testing.T) {
	r := newRig(t)
	r.gh.artifacts = []Artifact{
		{ID: 10, Name: "build-output", Size: 100},
		{ID: 11, Name: "results", Size: 100},
		{ID: 12, Name: "test-guardrails-report", Size: 100},
		{ID: 13, Name: "old", Size: 100, Expired: true},
		{ID: 14, Name: "huge", Size: MaxReportBytes + 1},
	}
	r.gh.zips[10] = zipOf(t, map[string]string{"dist/app.js": "console.log(1)", "notes.xml": "<notes/>"})
	r.gh.zips[11] = zipOf(t, map[string]string{"reports/junit.xml": junitXML, "pw/report.json": playwright, "go.out": goTestJSON})
	r.gh.zips[12] = zipOf(t, map[string]string{"guard.json": guardJSON})
	r.svc.RunCompleted(context.Background(), 3, 900, 1)
	r.drain(t)

	rows, err := r.db.CIReportFilesForRun(context.Background(), 3, 900)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range rows {
		got[f.Member] = f.Format + "/" + f.State
		if f.State == "parsed" && (!strings.HasPrefix(f.Path, "ci-data/3/reports/") || f.Bytes <= 0) {
			t.Errorf("row = %+v", f)
		}
	}
	want := map[string]string{
		"guard.json": "guardrails/parsed", "reports/junit.xml": "junit/parsed", "pw/report.json": "playwright/parsed",
		"go.out": "gotest/parsed", "": "/too_large",
	}
	if len(got) != len(want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("member %q = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
	if r.gh.zipCalls != 3 {
		t.Errorf("downloads = %d, want 3 (expired and over-cap artifacts are not downloaded)", r.gh.zipCalls)
	}
	if len(r.sink.got) != 4 || r.sink.got[0].Format != FormatGuardrails {
		t.Errorf("the guard report must be parsed first: %+v", r.sink.got)
	}
	if text := r.readLog(t, r.sink.got[0].Path); !strings.Contains(text, `"G1"`) {
		t.Errorf("stored report = %q", text)
	}

	// a second RunCompleted downloads the artifacts again but no member twice
	r.svc.RunCompleted(context.Background(), 3, 900, 1)
	r.drain(t)
	if len(r.sink.got) != 4 {
		t.Errorf("parsed again: %d", len(r.sink.got))
	}
}

// A parser that fails marks the file broken, never stops the others (R6.8); a run over 200 MiB of artifacts
// is cut off.
func TestBrokenReportAndRunCap(t *testing.T) {
	r := newRig(t)
	r.sink.fail = true
	r.gh.artifacts = []Artifact{{ID: 20, Name: "a", Size: 10}, {ID: 21, Name: "b", Size: MaxReportBytes}, {ID: 22, Name: "c", Size: MaxReportBytes},
		{ID: 23, Name: "d", Size: MaxReportBytes}, {ID: 24, Name: "e", Size: MaxReportBytes}, {ID: 25, Name: "f", Size: MaxReportBytes}}
	r.gh.zips[20] = zipOf(t, map[string]string{"junit.xml": junitXML})
	for id := int64(21); id <= 25; id++ {
		r.gh.zips[id] = zipOf(t, map[string]string{"x.txt": "nothing"})
	}
	r.svc.RunCompleted(context.Background(), 3, 901, 1)
	handled, _, err := r.svc.Step(context.Background())
	if !handled || err == nil || !strings.Contains(err.Error(), "parse report") {
		t.Fatalf("step = %v %v", handled, err)
	}
	f, ok, _ := r.db.CIReportFileOf(context.Background(), 3, 20, "junit.xml")
	if !ok || f.State != "broken" || f.Reason == "" {
		t.Fatalf("row = %+v", f)
	}
	// 10 bytes + 3 x 50 MiB fit in 200 MiB; the fourth 50 MiB artifact does not
	if r.gh.zipCalls != 4 {
		t.Errorf("downloads = %d, want 4", r.gh.zipCalls)
	}
	over, ok, _ := r.db.CIReportFileOf(context.Background(), 3, 24, "")
	if !ok || over.State != "too_large" || !strings.Contains(over.Reason, "200 MiB") {
		t.Fatalf("over the run cap = %+v", over)
	}
}

func TestClassifyByContent(t *testing.T) {
	ds := DefaultDetectors()
	cases := map[string]string{
		junitXML:                     FormatJUnit,
		"\xef\xbb\xbf  " + guardJSON: FormatGuardrails,
		playwright:                   FormatPlaywright,
		`{"numTotalTestSuites":3}`:   FormatVitest,
		goTestJSON:                   FormatGoTest,
		`{ "type": "suite", "event": "started" }`:                                   FormatLibtest,
		`<TestRun xmlns="http://microsoft.com/schemas/VisualStudio/TeamTest/2010">`: FormatTRX,
		`<html></html>`:     "",
		`{"hello":"world"}`: "",
		"":                  "",
	}
	for in, want := range cases {
		if got := Classify([]byte(in), ds); got != want {
			t.Errorf("Classify(%.40q) = %q, want %q", in, got, want)
		}
	}
}
