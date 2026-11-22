package workflows

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/insights"
)

func file(y string) File {
	return File{RepositoryID: 1, HeadSHA: "abc", Path: ".github/workflows/ci.yml", Content: []byte(y)}
}

func find(rs []Result, c CheckID, job string) (Result, bool) {
	for _, r := range rs {
		if r.Check == c && r.Job == job {
			return r, true
		}
	}
	return Result{}, false
}

// TestEachCheck: one table, each case one check on one small file (R5.29).
func TestEachCheck(t *testing.T) {
	const on = "on: [pull_request]\n"
	cases := []struct {
		name  string
		yaml  string
		check CheckID
		job   string
		want  Status
		line  int    // 0: not checked
		text  string // the detail names this
	}{
		{"timeout missing", on + "jobs:\n  a:\n    runs-on: x\n    steps:\n      - run: echo\n", CheckTimeout, "a", Fail, 3, "timeout-minutes: 60"},
		{"timeout 90", on + "jobs:\n  a:\n    timeout-minutes: 90\n    steps: []\n", CheckTimeout, "a", Fail, 4, "over the limit of 60"},
		{"timeout 60", on + "jobs:\n  a:\n    timeout-minutes: 60\n    steps: []\n", CheckTimeout, "a", OK, 0, ""},
		{"timeout expression", on + "jobs:\n  a:\n    timeout-minutes: ${{ inputs.t }}\n    steps: []\n", CheckTimeout, "a", OK, 0, ""},
		{"retry action", on + "jobs:\n  a:\n    steps:\n      - uses: nick-fields/retry@v3\n", CheckRetry, "a", Fail, 5, "retry action"},
		{"retry flag", on + "jobs:\n  a:\n    steps:\n      - run: npx playwright test --retries=2\n", CheckRetry, "a", Fail, 5, "--retries=2"},
		{"retry zero", on + "jobs:\n  a:\n    steps:\n      - run: npx playwright test --retries=0\n", CheckRetry, "a", OK, 0, ""},
		{"retry key", on + "jobs:\n  a:\n    steps:\n      - uses: x/y@v1\n        with:\n          retry: 3\n", CheckRetry, "a", Fail, 7, "`retry:` set to 3"},
		{"rerun failed", on + "jobs:\n  a:\n    steps:\n      - run: pytest --reruns 2\n", CheckRetry, "a", Fail, 5, "--reruns"},
		{"slow mutation on pr", on + "jobs:\n  m:\n    steps:\n      - run: npx stryker run\n", CheckSlowTestsOnPR, "m", Fail, 5, "mutation"},
		{"slow mutation on schedule", on + "jobs:\n  m:\n    if: github.event_name == 'schedule'\n    steps:\n      - run: npx stryker run\n", CheckSlowTestsOnPR, "m", OK, 0, ""},
		{"slow mutation only main", "on:\n  push:\n    branches: [main]\njobs:\n  m:\n    steps:\n      - run: cargo mutants\n", CheckSlowTestsOnPR, "m", OK, 0, ""},
		{"slow property runs", on + "jobs:\n  p:\n    steps:\n      - run: go test\n        env:\n          PROPTEST_CASES: 100000\n", CheckSlowTestsOnPR, "p", Fail, 5, "100000 runs"},
		{"cache node missing", on + "jobs:\n  a:\n    steps:\n      - uses: actions/setup-node@v4\n        with:\n          node-version: 24\n", CheckCache, "a", Fail, 5, "cache: pnpm"},
		{"cache node set", on + "jobs:\n  a:\n    steps:\n      - uses: actions/setup-node@v4\n        with:\n          cache: pnpm\n", CheckCache, "a", OK, 0, ""},
		{"cache go false", on + "jobs:\n  a:\n    steps:\n      - uses: actions/setup-go@v5\n        with:\n          cache: false\n", CheckCache, "a", Fail, 5, "cache: false"},
		{"cache cargo", on + "jobs:\n  a:\n    steps:\n      - run: cargo test\n", CheckCache, "a", Fail, 5, "cargo"},
		{"cache cargo with cache step", on + "jobs:\n  a:\n    steps:\n      - uses: actions/cache@v4\n      - run: cargo test\n", CheckCache, "a", OK, 0, ""},
		{"order e2e", on + "jobs:\n  lint:\n    steps: []\n  e2e:\n    steps: []\n", CheckStepOrder, "e2e", Fail, 5, "does not need lint"},
		{"order e2e needs lint through build", on + "jobs:\n  lint:\n    steps: []\n  build:\n    needs: lint\n    steps: []\n  e2e:\n    needs: [build]\n    steps: []\n", CheckStepOrder, "e2e", OK, 0, ""},
	}
	if len(cases) > 20 {
		t.Fatal("table over 20 cases")
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, ok := find(NewChecker().Check(file(c.yaml), nil), c.check, c.job)
			if !ok {
				t.Fatalf("no %s result for %s", c.check, c.job)
			}
			if r.Status != c.want {
				t.Fatalf("status %s, want %s: %+v", r.Status, c.want, r)
			}
			if c.want == Fail {
				if r.Line != c.line {
					t.Errorf("line %d, want %d", r.Line, c.line)
				}
				if !strings.Contains(r.Detail, c.text) || !strings.Contains(r.Detail, "Fix:") {
					t.Errorf("detail must name the fix and %q: %q", c.text, r.Detail)
				}
			}
		})
	}
}

func TestStepOrderLintAfterTest(t *testing.T) {
	y := "on: [push]\njobs:\n  a:\n    steps:\n      - run: pnpm test\n      - run: pnpm lint\n"
	r, _ := find(NewChecker().Check(file(y), nil), CheckStepOrder, "a")
	if r.Status != Fail || r.Line != 6 || !strings.Contains(r.Detail, "comes after a test step (line 5)") {
		t.Fatalf("%+v", r)
	}
}

func TestMatrixAxisGrowth(t *testing.T) {
	old := "on: [push]\njobs:\n  a:\n    strategy:\n      matrix:\n        os: [linux, windows]\n    steps: []\n"
	grown := "on: [push]\njobs:\n  a:\n    strategy:\n      matrix:\n        os: [linux, windows, macos]\n        node: [22, 24]\n    steps: []\n"
	prev := file(old)
	r, ok := find(NewChecker().Check(file(grown), &prev), CheckMatrixAxis, "a")
	if !ok || r.Status != Fail || !strings.Contains(r.Detail, "2 jobs before, 6 now") || !strings.Contains(r.Detail, "new axis node") {
		t.Fatalf("%+v", r)
	}
	if r, _ = find(NewChecker().Check(file(old), &prev), CheckMatrixAxis, "a"); r.Status != OK {
		t.Fatalf("same matrix: %+v", r)
	}
	if r, _ = find(NewChecker().Check(file(grown), nil), CheckMatrixAxis, "a"); r.Status != OK {
		t.Fatalf("no previous: %+v", r)
	}
}

func TestGuardrailsAndSkips(t *testing.T) {
	rs := NewChecker().Check(file("on: [push]\njobs:\n  a:\n    steps: []\n"), nil)
	if r, _ := find(rs, CheckTimeout, "a"); r.Guardrail != "G2" {
		t.Errorf("G2: %+v", r)
	}
	for _, f := range []File{{Content: nil, Reason: "gone"}, file("jobs: [unclosed")} {
		rs := NewChecker().Check(f, nil)
		if len(rs) != len(AllChecks) {
			t.Fatalf("want one skip per check: %+v", rs)
		}
		for _, r := range rs {
			if r.Status != Skip || r.Detail == "" {
				t.Errorf("%+v", r)
			}
		}
	}
}

type memStore struct {
	sha string
	res []Result
}

func (m *memStore) Save(context.Context, File, []Result) error { return nil }
func (m *memStore) Latest(_ context.Context, id int64) (string, []Result, error) {
	if id != 1 {
		return "", nil, nil
	}
	return m.sha, m.res, nil
}
func (m *memStore) ForCommit(context.Context, int64, string) ([]Result, error) { return m.res, nil }

type oneRepo struct{}

func (oneRepo) Repositories(context.Context) ([]Repo, error) {
	return []Repo{{ID: 1, Name: "o/r"}, {ID: 2, Name: "o/none"}}, nil
}

type oneJobs struct{ jobs []insights.JobFact }

func (o oneJobs) JobsBetween(context.Context, insights.Window, int64) ([]insights.JobFact, error) {
	return o.jobs, nil
}

func TestAnalyserFindings(t *testing.T) {
	st := &memStore{sha: "abc", res: []Result{
		{Check: CheckTimeout, Job: JoinJob(".github/workflows/ci.yml", "a"), Status: Fail, Line: 3, Detail: "no timeout. Fix: add it.", Guardrail: "G2"},
		{Check: CheckTimeout, Job: JoinJob(".github/workflows/ci.yml", "b"), Status: OK},
		{Check: CheckCache, Job: JoinJob(".github/workflows/ci.yml", "a"), Status: Skip},
	}}
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	jobs := oneJobs{jobs: []insights.JobFact{{Workflow: ".github/workflows/ci.yml", NodeID: "a", StartedAt: t0, CompletedAt: t0.Add(75 * time.Minute)}}}
	a := Analyser{Repos: oneRepo{}, Store: st, Jobs: jobs}
	got, err := a.Analyse(context.Background(), insights.Window{})
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %+v", err, got)
	}
	f := got[0]
	if f.Kind != insights.KindWorkflowTimeout || f.Area != insights.AreaWorkflows || f.Guardrail != "G2" || f.MinutesSavedWeek != 15 || f.Repository != "o/r" {
		t.Fatalf("%+v", f)
	}
	var ev struct{ Checks []evidenceItem }
	if err := json.Unmarshal(f.Evidence, &ev); err != nil || len(ev.Checks) != 1 || ev.Checks[0].Job != "a" || ev.Checks[0].Line != 3 || ev.Checks[0].Workflow != ".github/workflows/ci.yml" {
		t.Fatalf("%s %v", f.Evidence, err)
	}
	if !strings.Contains(f.Fix, "Fix:") {
		t.Errorf("fix: %q", f.Fix)
	}
	if a.Name() != "workflows" || len(a.Kinds()) != 6 {
		t.Error("name or kinds")
	}
}
