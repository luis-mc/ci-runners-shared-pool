package repos

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

const wf = `name: ci
on: push
jobs:
  literal:
    runs-on: [self-hosted, Linux, legacy-proj]
  scalar:
    runs-on: ubuntu-latest
  viavar:
    runs-on: ${{ fromJSON(vars.RUNS_ON_LINUX) }}
  withdefault:
    runs-on: ${{ fromJSON(vars.RUNS_ON_MACOS || '["macos-latest"]') }}
  block:
    runs-on:
      - self-hosted
      - Windows
  matrix:
    runs-on: ${{ matrix.os }}
  group:
    runs-on:
      group: big
      labels: [self-hosted, Linux]
  nolabels:
    steps: []
`

func TestParseRunsOnForms(t *testing.T) { // R5.30
	jobs := ParseRunsOn([]byte(wf))
	by := map[string]RunsOnJob{}
	for _, j := range jobs {
		by[j.Job] = j
	}
	if len(jobs) != 7 {
		t.Fatalf("jobs with runs-on: %d", len(jobs))
	}
	if !reflect.DeepEqual(by["literal"].Literal, []string{"self-hosted", "Linux", "legacy-proj"}) || by["literal"].Line != 5 {
		t.Fatalf("%+v", by["literal"])
	}
	if !reflect.DeepEqual(by["scalar"].Literal, []string{"ubuntu-latest"}) {
		t.Fatalf("%+v", by["scalar"])
	}
	if by["viavar"].Var != "RUNS_ON_LINUX" || len(by["viavar"].Literal) != 0 {
		t.Fatalf("%+v", by["viavar"])
	}
	if by["withdefault"].Var != "RUNS_ON_MACOS" || !reflect.DeepEqual(by["withdefault"].Default, []string{"macos-latest"}) {
		t.Fatalf("%+v", by["withdefault"])
	}
	if !reflect.DeepEqual(by["block"].Literal, []string{"self-hosted", "Windows"}) {
		t.Fatalf("%+v", by["block"])
	}
	if !by["matrix"].Unresolved {
		t.Fatalf("%+v", by["matrix"])
	}
	if !reflect.DeepEqual(by["group"].Literal, []string{"self-hosted", "Linux"}) {
		t.Fatalf("%+v", by["group"])
	}
}

func TestParseRunsOnBadFiles(t *testing.T) {
	for _, in := range []string{"", ":::not yaml", "- a\n- b\n", "jobs: 3\n", "jobs:\n  a: nope\n"} {
		if got := ParseRunsOn([]byte(in)); len(got) != 0 {
			t.Errorf("%q: %+v", in, got)
		}
	}
}

var poolSets = [][]string{{"self-hosted", "Linux", "X64", "docker", "crsp-home"}}

func TestCheckFindings(t *testing.T) { // R5.30
	vars := map[string]string{"RUNS_ON_LINUX": `["self-hosted","Linux","X64"]`}
	got := Check(".github/workflows/ci.yml", ParseRunsOn([]byte(wf)), vars, poolSets)
	kinds := map[string]Finding{}
	for _, f := range got {
		kinds[f.Text[:min(len(f.Text), 14)]] = f
	}
	var byJob = map[string]Finding{}
	for _, f := range got {
		for _, j := range []string{"literal", "block", "withdefault", "viavar"} {
			if len(f.Text) > 4 && f.Text[4:4+len(j)] == j {
				byJob[j] = f
			}
		}
	}
	// literal: carries legacy-proj, which no runner has: fail.
	if f := byJob["literal"]; f.Kind != KindNoRunner || f.Severity() != SeverityFail || f.Line != 5 || f.Path == "" {
		t.Fatalf("literal: %+v", f)
	}
	// block: Windows is not served: fail.
	if f := byJob["block"]; f.Kind != KindNoRunner {
		t.Fatalf("block: %+v", f)
	}
	// viavar: resolved and carried: no finding.
	if _, ok := byJob["viavar"]; ok {
		t.Fatalf("viavar must be clean: %+v", byJob["viavar"])
	}
	// withdefault: variable unset, default is GitHub-hosted: skipped, and the macOS variable is not "read but unset" because a default exists.
	if _, ok := byJob["withdefault"]; ok {
		t.Fatalf("a default resolves it: %+v", byJob["withdefault"])
	}
	// scalar, matrix, hosted: nothing.
	for _, f := range got {
		if f.Kind == KindHosted {
			t.Fatal("hosted jobs are not findings")
		}
	}
	if f := byJob["group"]; f.Kind != "" {
		t.Fatalf("group is not in the list above: %+v", f)
	}
	if len(got) != 3 || got[2].Kind != KindNotGeneric || got[2].Line != 20 {
		t.Fatalf("literal fail, block fail, and the group job (carried but written out) warns: %d %+v", len(got), got)
	}
}

func TestCheckLiteralInsteadOfVariableWarns(t *testing.T) { // R5.30
	jobs := ParseRunsOn([]byte("jobs:\n  a:\n    runs-on: [self-hosted, Linux, docker]\n"))
	got := Check("w.yml", jobs, nil, poolSets)
	if len(got) != 1 || got[0].Kind != KindNotGeneric || got[0].Severity() != SeverityWarning ||
		!contains(got[0].Text, "fromJSON(vars.RUNS_ON_LINUX)") {
		t.Fatalf("%+v", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestCheckVariableNotSetFails(t *testing.T) { // R5.30
	jobs := ParseRunsOn([]byte("jobs:\n  a:\n    runs-on: ${{ fromJSON(vars.RUNS_ON_WINDOWS) }}\n  b:\n    runs-on: ${{ fromJSON(vars.OTHER_VAR) }}\n"))
	got := Check("w.yml", jobs, map[string]string{}, poolSets)
	if len(got) != 1 || got[0].Kind != KindUsesVar || got[0].Line != 3 || got[0].Severity() != SeverityFail {
		t.Fatalf("an unset CRSP variable fails; an unrelated variable is not ours: %+v", got)
	}
	bad := Check("w.yml", jobs[:1], map[string]string{"RUNS_ON_WINDOWS": "not json"}, poolSets)
	if len(bad) != 1 || bad[0].Kind != KindUsesVar {
		t.Fatalf("%+v", bad)
	}
}

func TestOfflineCaseInsensitiveLabels(t *testing.T) {
	jobs := ParseRunsOn([]byte("jobs:\n  a:\n    runs-on: [Self-Hosted, linux, x64]\n"))
	got := Check("w.yml", jobs, nil, poolSets)
	if len(got) != 1 || got[0].Kind != KindNotGeneric {
		t.Fatalf("label match ignores case: %+v", got)
	}
}

func TestVariablesRead(t *testing.T) { // R5.29
	files := map[string][]byte{
		"a.yml": []byte("jobs:\n  a:\n    runs-on: ${{ fromJSON(vars.RUNS_ON_LINUX) }}\n"),
		"b.yml": []byte("jobs:\n  b:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo ${{ vars.RUNS_ON_WINDOWS }}\n"),
		"c.yml": []byte("jobs:\n  c:\n    runs-on: ubuntu-latest\n"),
	}
	got := VariablesRead(files)
	if !reflect.DeepEqual(got["RUNS_ON_LINUX"], []string{"a.yml"}) || !reflect.DeepEqual(got["RUNS_ON_WINDOWS"], []string{"b.yml"}) || len(got) != 2 {
		t.Fatalf("%v", got)
	}
}

func TestCheckRunsOnEndToEndWithETags(t *testing.T) { // R5.30, R6.6
	e := newEnv(t)
	e.world()
	ctx := context.Background()
	e.fake.SetWorkflow("alice/secret", ".github/workflows/ci.yml", wf)
	e.turnOn(101) // runs the check
	fs, err := e.svc.Findings(ctx, 101)
	if err != nil || len(fs) == 0 {
		t.Fatalf("%v %v", fs, err)
	}
	for _, f := range fs {
		if f.RepositoryID != 101 || f.Path != ".github/workflows/ci.yml" || f.Line == 0 {
			t.Fatalf("file and line: %+v", f)
		}
	}
	v, _ := e.svc.Get(ctx, adminP, 101)
	if v.FindingsFail == 0 {
		t.Fatalf("the row shows the check: %+v", v)
	}
	before := e.fake.Count("GET", "/repos/alice/secret/contents/.github/workflows/ci.yml")
	if err := e.svc.CheckRunsOn(ctx, 101); err != nil {
		t.Fatal(err)
	}
	fs2, _ := e.svc.Findings(ctx, 101)
	if len(fs2) != len(fs) {
		t.Fatalf("a 304 must still give the same findings: %d != %d", len(fs2), len(fs))
	}
	found304 := false
	for _, r := range e.fake.Requests() {
		if r.Path == "/repos/alice/secret/contents/.github/workflows/ci.yml" && r.Header.Get("If-None-Match") != "" {
			found304 = true
		}
	}
	if !found304 || e.fake.Count("GET", "/repos/alice/secret/contents/.github/workflows/ci.yml") != before+1 {
		t.Fatal("the second check sends the ETag")
	}
	// A fix in the workflow clears findings.
	e.fake.SetWorkflow("alice/secret", ".github/workflows/ci.yml", "jobs:\n  a:\n    runs-on: ${{ fromJSON(vars.RUNS_ON_LINUX) }}\n")
	_ = e.svc.CheckRunsOn(ctx, 101)
	if fs, _ := e.svc.Findings(ctx, 101); len(fs) != 0 {
		t.Fatalf("%+v", fs)
	}
}

func TestCheckAllRunsOnCoversEnabledRepositories(t *testing.T) { // R5.30
	e := newEnv(t)
	e.world()
	ctx := context.Background()
	e.fake.SetWorkflow("alice/secret", ".github/workflows/ci.yml", wf)
	e.turnOn(101)
	// Findings are cleared by hand to show that the hourly pass writes them back.
	e.fake.SetWorkflow("alice/secret", ".github/workflows/ci.yml", "jobs:\n  a:\n    runs-on: ${{ fromJSON(vars.RUNS_ON_LINUX) }}\n")
	if err := e.svc.CheckAllRunsOn(ctx); err != nil {
		t.Fatal(err)
	}
	if fs, _ := e.svc.Findings(ctx, 101); len(fs) != 0 {
		t.Fatalf("the pass reads the fixed file: %+v", fs)
	}
	e.fake.SetWorkflow("alice/secret", ".github/workflows/ci.yml", wf)
	if err := e.svc.CheckAllRunsOn(ctx); err != nil {
		t.Fatal(err)
	}
	if fs, _ := e.svc.Findings(ctx, 101); len(fs) == 0 {
		t.Fatal("the pass found nothing in a file with findings")
	}
}

// R5.30, R5.105: the per-job form with a fallback chain is read; the routing
// label on the runners crsp chose is not a finding; a job whose own variable is
// not set yet falls back to the pool variable and the hosted label.
func TestCheckPerJobFallbackChain(t *testing.T) {
	src := []byte("jobs:\n  e2e:\n    runs-on: ${{ fromJSON(vars.CRSP_E2E || vars.RUNS_ON_LINUX || '\"ubuntu-latest\"') }}\n  gone:\n    runs-on: ${{ fromJSON(vars.CRSP_GONE || vars.RUNS_ON_WINDOWS) }}\n")
	jobs := ParseRunsOn(src)
	if len(jobs) != 2 || jobs[0].Var != "CRSP_E2E" || len(jobs[0].Vars) != 2 || !reflect.DeepEqual(jobs[0].Default, []string{"ubuntu-latest"}) {
		t.Fatalf("parsed %+v", jobs)
	}
	pool := [][]string{{"self-hosted", "Linux", "X64"}}
	// The per-job variable is set; its routing label is on the chosen runners, not in the pool label sets.
	vars := map[string]string{"CRSP_E2E": `["self-hosted","Linux","crsp-e2e-7f3a"]`, "RUNS_ON_LINUX": `["self-hosted","Linux"]`}
	for _, f := range Check("w.yml", jobs[:1], vars, pool) {
		t.Errorf("per-job variable gave a finding: %+v", f)
	}
	// Not set: the pool variable answers; a hosted default is skipped.
	if got := Check("w.yml", jobs[:1], map[string]string{"RUNS_ON_LINUX": `["self-hosted","Linux"]`}, pool); len(got) != 0 {
		t.Fatalf("fallback to the pool variable: %+v", got)
	}
	if got := Check("w.yml", jobs[:1], nil, pool); len(got) != 0 {
		t.Fatalf("hosted default is not checked: %+v", got)
	}
	// Neither variable nor default: the pool variable is named.
	got := Check("w.yml", jobs[1:], nil, pool)
	if len(got) != 1 || got[0].Kind != KindUsesVar || !strings.Contains(got[0].Text, "RUNS_ON_WINDOWS") {
		t.Fatalf("missing pool variable: %+v", got)
	}
}
