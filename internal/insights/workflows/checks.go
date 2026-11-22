package workflows

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/luis-mc/ci-runners-shared-pool/internal/insights"
)

// MaxTimeoutMinutes is the guideline for a job's timeout-minutes: a safety net only, because crsp's own job and run timers end a job first (scheduling R5.103).
const MaxTimeoutMinutes = 60

// AllChecks lists every check ID, in the order Check reports them.
var AllChecks = []CheckID{CheckTimeout, CheckRetry, CheckMatrixAxis, CheckStepOrder, CheckCache, CheckSlowTestsOnPR}

// guardrailOf maps a check to its guardrail (R5.29).
func guardrailOf(c CheckID) insights.Guardrail {
	switch c {
	case CheckTimeout:
		return "G2"
	case CheckRetry:
		return "G3"
	case CheckSlowTestsOnPR:
		return "G6"
	}
	return ""
}

// checker is the pure Checker.
type checker struct{}

// NewChecker returns the Checker of R5.29. It does no I/O.
func NewChecker() Checker { return checker{} }

var _ Checker = checker{}

// Check runs every check on f. A file that cannot be read or parsed gives one Skip per check with the reason.
func (checker) Check(f File, previous *File) []Result {
	wf, reason := parseFile(f)
	if wf == nil {
		return skipAll(reason)
	}
	var prev *workflow
	if previous != nil {
		prev, _ = parseFile(*previous)
	}
	var out []Result
	out = append(out, checkTimeout(wf)...)
	out = append(out, checkRetry(wf)...)
	out = append(out, checkMatrixAxis(wf, prev)...)
	out = append(out, checkStepOrder(wf)...)
	out = append(out, checkCache(wf)...)
	out = append(out, checkSlowOnPR(wf)...)
	return out
}

func skipAll(reason string) []Result {
	out := make([]Result, 0, len(AllChecks))
	for _, c := range AllChecks {
		out = append(out, Result{Check: c, Status: Skip, Detail: reason, Guardrail: guardrailOf(c)})
	}
	return out
}

// ---- parsing ----

type step struct {
	line int
	name string
	uses string
	run  string
	with map[string]string
	node *yaml.Node
}

type job struct {
	id       string
	name     string
	line     int
	timeout  *yaml.Node // nil when absent
	needs    []string
	ifExpr   string
	reusable bool // job-level uses: (a called workflow cannot have timeout-minutes)
	steps    []step
	axes     map[string][]string // matrix axes
	node     *yaml.Node
}

type workflow struct {
	events   map[string]*yaml.Node // trigger name -> its config (may be nil)
	jobs     []job
	jobOrder map[string]int
}

func parseFile(f File) (*workflow, string) {
	if f.Content == nil {
		reason := f.Reason
		if reason == "" {
			reason = "The file could not be read, so it was not checked."
		}
		return nil, reason
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(f.Content, &doc); err != nil || len(doc.Content) == 0 {
		return nil, "The file is not valid YAML, so it was not checked. Fix the YAML first."
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, "The file is not a workflow (no top-level mapping), so it was not checked."
	}
	wf := &workflow{events: map[string]*yaml.Node{}, jobOrder: map[string]int{}}
	if on := mapGet(root, "on"); on != nil {
		switch on.Kind {
		case yaml.ScalarNode:
			wf.events[on.Value] = nil
		case yaml.SequenceNode:
			for _, e := range on.Content {
				wf.events[e.Value] = nil
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(on.Content); i += 2 {
				wf.events[on.Content[i].Value] = on.Content[i+1]
			}
		}
	}
	jobs := mapGet(root, "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return wf, ""
	}
	for i := 0; i+1 < len(jobs.Content); i += 2 {
		k, v := jobs.Content[i], jobs.Content[i+1]
		if v.Kind != yaml.MappingNode {
			continue
		}
		j := job{id: k.Value, line: k.Line, node: v, timeout: mapGet(v, "timeout-minutes")}
		j.name = scalar(mapGet(v, "name"))
		j.ifExpr = scalar(mapGet(v, "if"))
		j.reusable = mapGet(v, "uses") != nil
		j.needs = stringList(mapGet(v, "needs"))
		if s := mapGet(v, "strategy"); s != nil {
			j.axes = matrixAxes(mapGet(s, "matrix"))
		}
		if st := mapGet(v, "steps"); st != nil && st.Kind == yaml.SequenceNode {
			for _, sn := range st.Content {
				j.steps = append(j.steps, parseStep(sn))
			}
		}
		wf.jobOrder[j.id] = len(wf.jobs)
		wf.jobs = append(wf.jobs, j)
	}
	return wf, ""
}

func parseStep(n *yaml.Node) step {
	s := step{line: n.Line, node: n, with: map[string]string{}}
	s.name = scalar(mapGet(n, "name"))
	s.uses = scalar(mapGet(n, "uses"))
	s.run = scalar(mapGet(n, "run"))
	if w := mapGet(n, "with"); w != nil && w.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(w.Content); i += 2 {
			s.with[w.Content[i].Value] = scalar(w.Content[i+1])
		}
	}
	return s
}

func matrixAxes(m *yaml.Node) map[string][]string {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	axes := map[string][]string{}
	for i := 0; i+1 < len(m.Content); i += 2 {
		name := m.Content[i].Value
		if name == "include" || name == "exclude" {
			continue
		}
		v := m.Content[i+1]
		if v.Kind == yaml.SequenceNode {
			for _, e := range v.Content {
				axes[name] = append(axes[name], nodeText(e))
			}
		} else {
			axes[name] = []string{"(expression)"}
		}
	}
	return axes
}

func nodeText(n *yaml.Node) string {
	if n.Kind == yaml.ScalarNode {
		return n.Value
	}
	b, _ := yaml.Marshal(n)
	return strings.TrimSpace(string(b))
}

func mapGet(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func scalar(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}

func stringList(n *yaml.Node) []string {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.ScalarNode {
		return []string{n.Value}
	}
	var out []string
	for _, e := range n.Content {
		out = append(out, e.Value)
	}
	return out
}

func (j job) title() string {
	if j.name != "" {
		return j.id + " (" + j.name + ")"
	}
	return j.id
}

func result(c CheckID, j string, ok bool, line int, detail string) Result {
	st := OK
	if !ok {
		st = Fail
	}
	return Result{Check: c, Job: j, Status: st, Line: line, Detail: detail, Guardrail: guardrailOf(c)}
}

// ---- workflow.timeout (G2) ----

func checkTimeout(wf *workflow) []Result {
	var out []Result
	for _, j := range wf.jobs {
		if j.reusable {
			continue // a called workflow sets its own timeouts
		}
		switch j.timeout {
		case nil:
			out = append(out, result(CheckTimeout, j.id, false, j.line,
				fmt.Sprintf("Job %s has no timeout-minutes, so a hung job holds its slot for 6 hours. Fix: add `timeout-minutes: %d` or less to the job.", j.title(), MaxTimeoutMinutes)))
		default:
			n, err := strconv.ParseFloat(j.timeout.Value, 64)
			if err == nil && n > MaxTimeoutMinutes {
				out = append(out, result(CheckTimeout, j.id, false, j.timeout.Line,
					fmt.Sprintf("Job %s has timeout-minutes %s, over the limit of %d. Fix: lower it to %d or less, and split the job if it needs more.", j.title(), j.timeout.Value, MaxTimeoutMinutes, MaxTimeoutMinutes)))
				continue
			}
			out = append(out, result(CheckTimeout, j.id, true, j.timeout.Line, ""))
		}
	}
	return out
}

// ---- workflow.retry (G3) ----

var (
	retryUses  = regexp.MustCompile(`(?i)(nick-fields/retry|wandalen/wretry\.action|nick-invision/retry|retry-action)`)
	retryFlag  = regexp.MustCompile(`(?i)(--retries|--reruns|--retry|--retry-times|--rerun-failures|-retries|-reruns)(?:[= ]+(\d+))?`)
	rerunWords = regexp.MustCompile(`(?i)rerun-failed|--lf\b.*--ff|flaky-?retry`)
)

func checkRetry(wf *workflow) []Result {
	var out []Result
	for _, j := range wf.jobs {
		line, what := 0, ""
		for _, s := range j.steps {
			if line != 0 {
				break
			}
			if retryUses.MatchString(s.uses) {
				line, what = s.line, "the retry action "+s.uses
				break
			}
			if l, w := retryInRun(s); l != 0 {
				line, what = l, w
				break
			}
			if n := retryKey(s.node); n != nil {
				line, what = n.Line, "`retry:` set to "+n.Value
			}
		}
		if line == 0 {
			out = append(out, result(CheckRetry, j.id, true, j.line, ""))
			continue
		}
		out = append(out, result(CheckRetry, j.id, false, line,
			fmt.Sprintf("Job %s retries tests with %s, which hides flaky tests. Fix: remove the retry and fix or quarantine the flaky test.", j.title(), what)))
	}
	return out
}

func retryInRun(s step) (int, string) {
	if s.run == "" {
		return 0, ""
	}
	for _, m := range retryFlag.FindAllStringSubmatch(s.run, -1) {
		if m[2] != "" {
			if n, _ := strconv.Atoi(m[2]); n == 0 {
				continue
			}
		}
		return s.line, "`" + strings.TrimSpace(m[0]) + "`"
	}
	if m := rerunWords.FindString(s.run); m != "" {
		return s.line, "`" + m + "`"
	}
	return 0, ""
}

// retryKey finds a `retry:` key over 0 inside a step (with, env or the step itself).
func retryKey(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Value == "retry" && v.Kind == yaml.ScalarNode {
				if x, err := strconv.Atoi(v.Value); err == nil && x > 0 {
					return v
				}
			}
			if r := retryKey(v); r != nil {
				return r
			}
		}
	}
	return nil
}

// ---- workflow.matrix_axis ----

func checkMatrixAxis(wf, prev *workflow) []Result {
	var out []Result
	for _, j := range wf.jobs {
		if len(j.axes) == 0 {
			continue
		}
		if prev == nil {
			out = append(out, result(CheckMatrixAxis, j.id, true, j.line, ""))
			continue
		}
		pi, ok := prev.jobOrder[j.id]
		if !ok {
			out = append(out, result(CheckMatrixAxis, j.id, true, j.line, ""))
			continue
		}
		pj := prev.jobs[pi]
		grown := growth(pj.axes, j.axes)
		if len(grown) == 0 {
			out = append(out, result(CheckMatrixAxis, j.id, true, j.line, ""))
			continue
		}
		out = append(out, result(CheckMatrixAxis, j.id, false, j.line,
			fmt.Sprintf("The matrix of job %s grew (%s): %d jobs before, %d now. Fix: keep the new axis or values only where they find bugs, run them on a schedule, or drop the old ones.",
				j.title(), strings.Join(grown, ", "), matrixSize(pj.axes), matrixSize(j.axes))))
	}
	return out
}

func growth(before, after map[string][]string) []string {
	var out []string
	names := make([]string, 0, len(after))
	for n := range after {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		old, ok := before[n]
		if !ok {
			out = append(out, "new axis "+n)
			continue
		}
		seen := map[string]bool{}
		for _, v := range old {
			seen[v] = true
		}
		var added []string
		for _, v := range after[n] {
			if !seen[v] {
				added = append(added, v)
			}
		}
		if len(added) > 0 {
			out = append(out, fmt.Sprintf("axis %s gained %s", n, strings.Join(added, ", ")))
		}
	}
	return out
}

func matrixSize(axes map[string][]string) int {
	n := 1
	for _, v := range axes {
		if len(v) > 0 {
			n *= len(v)
		}
	}
	return n
}

// ---- workflow.step_order ----

var (
	e2eWords  = regexp.MustCompile(`(?i)e2e|end-to-end|playwright|cypress`)
	lintWords = regexp.MustCompile(`(?i)\blint|typecheck|type-check|\btsc\b|go vet|\bmypy\b|\bpyright\b|\bruff\b|\beslint\b|golangci`)
	testWords = regexp.MustCompile(`(?i)\btest\b|\btests\b|vitest|pytest|\bjest\b|go test|playwright|cypress|cargo test|\bmocha\b`)
)

func checkStepOrder(wf *workflow) []Result {
	var lintJobs []string
	for _, j := range wf.jobs {
		if lintWords.MatchString(j.id) || lintWords.MatchString(j.name) {
			lintJobs = append(lintJobs, j.id)
		}
	}
	var out []Result
	for _, j := range wf.jobs {
		isE2E := e2eWords.MatchString(j.id) || e2eWords.MatchString(j.name)
		var problems []string
		line := j.line
		if isE2E && len(lintJobs) > 0 && !needsAny(wf, j.id, lintJobs) {
			problems = append(problems, fmt.Sprintf("it does not need %s (the lint or type check job of this workflow), so it can run for a branch that fails them", strings.Join(lintJobs, " or ")))
		}
		if s, t := lintAfterTest(j); s != 0 {
			problems = append(problems, fmt.Sprintf("a lint or type step (line %d) comes after a test step (line %d)", s, t))
			line = s
		}
		if len(problems) == 0 {
			out = append(out, result(CheckStepOrder, j.id, true, j.line, ""))
			continue
		}
		out = append(out, result(CheckStepOrder, j.id, false, line,
			fmt.Sprintf("Job %s: %s. Fix: make the end-to-end job `needs:` the lint and type check job, and put lint and type steps before the tests.", j.title(), strings.Join(problems, "; "))))
	}
	return out
}

// needsAny says whether job id needs one of targets, directly or through other jobs.
func needsAny(wf *workflow, id string, targets []string) bool {
	want := map[string]bool{}
	for _, t := range targets {
		want[t] = true
	}
	seen := map[string]bool{}
	var walk func(string) bool
	walk = func(cur string) bool {
		if seen[cur] {
			return false
		}
		seen[cur] = true
		i, ok := wf.jobOrder[cur]
		if !ok {
			return false
		}
		for _, n := range wf.jobs[i].needs {
			if want[n] || walk(n) {
				return true
			}
		}
		return false
	}
	return walk(id)
}

func lintAfterTest(j job) (lintLine, testLine int) {
	for _, s := range j.steps {
		text := s.name + "\n" + s.run
		isLint := lintWords.MatchString(text)
		if !isLint && testLine == 0 && testWords.MatchString(text) {
			testLine = s.line
		}
		if isLint && testLine != 0 {
			return s.line, testLine
		}
	}
	return 0, 0
}

// ---- workflow.cache ----

func checkCache(wf *workflow) []Result {
	var out []Result
	for _, j := range wf.jobs {
		line, what, fix := 0, "", ""
		hasCacheStep := false
		for _, s := range j.steps {
			if strings.HasPrefix(s.uses, "actions/cache") || strings.HasPrefix(strings.ToLower(s.uses), "swatinem/rust-cache") {
				hasCacheStep = true
			}
			if strings.HasPrefix(s.uses, "actions/setup-python") && s.with["cache"] != "" {
				hasCacheStep = true
			}
		}
		for _, s := range j.steps {
			switch {
			case strings.HasPrefix(s.uses, "actions/setup-node") && s.with["cache"] == "":
				line, what, fix = s.line, "setup-node without `cache`", "add `cache: pnpm` (or npm, yarn) to the setup-node step"
			case strings.HasPrefix(s.uses, "actions/setup-go") && strings.EqualFold(s.with["cache"], "false"):
				line, what, fix = s.line, "setup-go with `cache: false`", "remove `cache: false` so the Go caches are kept"
			}
			if line != 0 {
				break
			}
		}
		if line == 0 && !hasCacheStep {
			for _, s := range j.steps {
				if cargoRun.MatchString(s.run) {
					line, what, fix = s.line, "cargo without a cache", "add `Swatinem/rust-cache` or an `actions/cache` step for ~/.cargo and target"
					break
				}
				if pipRun.MatchString(s.run) {
					line, what, fix = s.line, "pip without a cache", "add an `actions/cache` step for ~/.cache/pip, or `cache: pip` on setup-python"
					break
				}
			}
		}
		if line == 0 {
			out = append(out, result(CheckCache, j.id, true, j.line, ""))
			continue
		}
		out = append(out, result(CheckCache, j.id, false, line,
			fmt.Sprintf("Job %s sets up a package manager with %s, so every run downloads everything again. Fix: %s.", j.title(), what, fix)))
	}
	return out
}

var (
	cargoRun = regexp.MustCompile(`(?m)(^|[\s;&|])cargo\s+(build|test|check|clippy|nextest|install|fetch)\b`)
	pipRun   = regexp.MustCompile(`(?m)(^|[\s;&|])pip3?\s+install\b`)
)

// ---- workflow.slow_tests_on_pr (G6) ----

var (
	mutationWords = regexp.MustCompile(`(?i)stryker|mutmut|go-mutesting|cargo[ -]mutants|gremlins|--mutants|mutation[ _-]?test|\bmutation\b|mutants\.py`)
	manyRunsEnv   = regexp.MustCompile(`(?i)(PROPTEST_CASES|QUICKCHECK_TESTS|HYPOTHESIS_MAX_EXAMPLES|FC_NUM_RUNS|numRuns)\s*[:=]\s*['"]?(\d+)`)
)

const manyRuns = 1000

func checkSlowOnPR(wf *workflow) []Result {
	var out []Result
	prRuns := runsOnPR(wf)
	for _, j := range wf.jobs {
		line, what := 0, ""
		for _, s := range j.steps {
			text := s.name + "\n" + s.uses + "\n" + s.run
			if m := mutationWords.FindString(text); m != "" {
				line, what = s.line, "mutation testing (`"+m+"`)"
				break
			}
			if m := manyRunsEnv.FindStringSubmatch(nodeDump(s.node)); m != nil {
				if n, _ := strconv.Atoi(m[2]); n >= manyRuns {
					line, what = s.line, "a property test with "+m[2]+" runs"
					break
				}
			}
		}
		if line == 0 {
			out = append(out, result(CheckSlowTestsOnPR, j.id, true, j.line, ""))
			continue
		}
		if !prRuns || offPR(j.ifExpr) {
			out = append(out, result(CheckSlowTestsOnPR, j.id, true, line, ""))
			continue
		}
		out = append(out, result(CheckSlowTestsOnPR, j.id, false, line,
			fmt.Sprintf("Job %s runs %s on pull requests, which makes every pull request slow. Fix: run it on `schedule` or on the default branch only (`if: github.event_name == 'schedule'`).", j.title(), what)))
	}
	return out
}

func nodeDump(n *yaml.Node) string {
	b, _ := yaml.Marshal(n)
	return string(b)
}

// runsOnPR says whether the workflow runs on pull_request or on a push that is not only the default branch.
func runsOnPR(wf *workflow) bool {
	for _, e := range []string{"pull_request", "pull_request_target"} {
		if _, ok := wf.events[e]; ok {
			return true
		}
	}
	push, ok := wf.events["push"]
	if !ok {
		return false
	}
	br := mapGet(push, "branches")
	if br == nil {
		return true // every branch
	}
	for _, b := range stringList(br) {
		if b != "main" && b != "master" {
			return true
		}
	}
	return false
}

var offPRIf = regexp.MustCompile(`(?i)schedule|workflow_dispatch|refs/heads/(main|master)|ref_name\s*==\s*'(main|master)'`)

func offPR(ifExpr string) bool { return offPRIf.MatchString(ifExpr) }
