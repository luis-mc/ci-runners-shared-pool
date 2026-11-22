package runson

import (
	"reflect"
	"testing"
)

const wf = `name: ci
on: push
jobs:
  plain:
    runs-on: ubuntu-latest
    steps: []
  flow:
    runs-on: [self-hosted, linux, x64]
  block:
    runs-on:
      - self-hosted
      - windows
  var:
    runs-on: ${{ fromJSON(vars.RUNS_ON_LINUX) }}
  withdefault:
    runs-on: ${{ fromJSON(vars.RUNS_ON_WINDOWS || '["self-hosted","windows"]') }}
  defaultlabel:
    runs-on: ${{ fromJSON(vars.RUNS_ON_MACOS) || 'macos-latest' }}
  matrix:
    runs-on: ${{ matrix.os }}
  mapping:
    runs-on:
      group: big
      labels: [self-hosted, linux]
  reusable:
    uses: ./.github/workflows/other.yml
`

func byJob(t *testing.T) map[string]Job {
	t.Helper()
	jobs, err := Parse(".github/workflows/ci.yml", []byte(wf))
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]Job{}
	for _, j := range jobs {
		m[j.Job] = j
	}
	return m
}

func TestParse(t *testing.T) {
	m := byJob(t)
	if _, ok := m["reusable"]; ok {
		t.Error("a job without runs-on must be left out")
	}
	cases := map[string]struct {
		kind   Kind
		labels []string
		v      string
		line   int
	}{
		"plain":        {Literal, []string{"ubuntu-latest"}, "", 5},
		"flow":         {Literal, []string{"self-hosted", "linux", "x64"}, "", 8},
		"block":        {Literal, []string{"self-hosted", "windows"}, "", 11},
		"var":          {Variable, nil, "RUNS_ON_LINUX", 14},
		"withdefault":  {Variable, []string{"self-hosted", "windows"}, "RUNS_ON_WINDOWS", 16},
		"defaultlabel": {Variable, []string{"macos-latest"}, "RUNS_ON_MACOS", 18},
		"mapping":      {Literal, []string{"self-hosted", "linux"}, "", 24},
	}
	for name, want := range cases {
		j, ok := m[name]
		if !ok {
			t.Errorf("%s missing", name)
			continue
		}
		if j.Kind != want.kind || !reflect.DeepEqual(j.Labels, want.labels) || j.Var != want.v {
			t.Errorf("%s = %+v", name, j)
		}
		if j.Line != want.line {
			t.Errorf("%s line = %d, want %d", name, j.Line, want.line)
		}
	}
	if j := m["matrix"]; j.Kind != Unresolved || j.Expr != "${{ matrix.os }}" {
		t.Errorf("matrix = %+v", j)
	}
}

func TestResolve(t *testing.T) {
	m := byJob(t)
	vars := map[string]string{"RUNS_ON_LINUX": `["self-hosted","linux","crsp"]`, "RUNS_ON_MACOS": `not json`}
	if l, ok, _ := m["var"].Resolve(vars); !ok || len(l) != 3 {
		t.Errorf("var: %v %v", l, ok)
	}
	// Unset with a default uses the default; unset without one names the variable.
	if l, ok, _ := m["withdefault"].Resolve(vars); !ok || l[1] != "windows" {
		t.Errorf("withdefault: %v %v", l, ok)
	}
	if _, ok, miss := m["var"].Resolve(nil); ok || miss != "RUNS_ON_LINUX" {
		t.Errorf("unset: ok=%v missing=%q", ok, miss)
	}
	// A variable that is set but not a JSON list cannot be resolved.
	if _, ok, miss := m["defaultlabel"].Resolve(vars); ok || miss != "" {
		t.Errorf("bad json: ok=%v missing=%q", ok, miss)
	}
	if _, ok, _ := m["matrix"].Resolve(vars); ok {
		t.Error("matrix must not resolve")
	}
}

func TestHostedAndCarries(t *testing.T) {
	if !IsHosted([]string{"ubuntu-latest"}) || IsHosted([]string{"self-hosted", "linux"}) || IsHosted(nil) {
		t.Error("IsHosted")
	}
	runner := []string{"self-hosted", "Linux", "X64", "crsp"}
	if !Carries(runner, []string{"self-hosted", "linux"}) || Carries(runner, []string{"self-hosted", "arm64"}) {
		t.Error("Carries is case-insensitive and needs every label")
	}
}

func TestVariableNames(t *testing.T) {
	if OSOfVariable("RUNS_ON_MACOS") != "macos" || OSOfVariable("OTHER") != "" || VariableOfOS("windows") != "RUNS_ON_WINDOWS" {
		t.Error("variable names")
	}
}

func TestBadYAMLIsAnError(t *testing.T) {
	if _, err := Parse("x.yml", []byte("jobs: [unclosed")); err == nil {
		t.Error("want error")
	}
	if jobs, err := Parse("x.yml", nil); err != nil || jobs != nil {
		t.Errorf("empty file: %v %v", jobs, err)
	}
}

// R5.18: a fallback chain of variables with a JSON-string default resolves to
// the first variable that is set, then the default.
func TestParseFallbackChain(t *testing.T) {
	src := "jobs:\n  e2e:\n    runs-on: ${{ fromJSON(vars.CRSP_E2E || vars.RUNS_ON_LINUX || '\"ubuntu-latest\"') }}\n"
	jobs, err := Parse("w.yml", []byte(src))
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs %+v err %v", jobs, err)
	}
	j := jobs[0]
	if j.Kind != Variable || j.Var != "CRSP_E2E" || len(j.Vars) != 2 || j.Vars[1] != "RUNS_ON_LINUX" || len(j.Labels) != 1 || j.Labels[0] != "ubuntu-latest" {
		t.Fatalf("job %+v", j)
	}
	if l, ok, _ := j.Resolve(map[string]string{"RUNS_ON_LINUX": `["self-hosted","Linux"]`, "CRSP_E2E": `["self-hosted","Linux","crsp-e2e-7f3a"]`}); !ok || len(l) != 3 {
		t.Fatalf("first set variable must win: %v %v", l, ok)
	}
	if l, ok, _ := j.Resolve(map[string]string{"RUNS_ON_LINUX": `["self-hosted","Linux"]`}); !ok || len(l) != 2 {
		t.Fatalf("pool variable next: %v %v", l, ok)
	}
	if l, ok, _ := j.Resolve(nil); !ok || l[0] != "ubuntu-latest" {
		t.Fatalf("default last: %v %v", l, ok)
	}
	// Not a chain: another call, or an operand that is not a variable, stays unresolved.
	bad, _ := Parse("w.yml", []byte("jobs:\n  a:\n    runs-on: ${{ fromJSON(inputs.x || vars.A) }}\n"))
	if bad[0].Kind != Unresolved {
		t.Fatalf("%+v", bad[0])
	}
}
