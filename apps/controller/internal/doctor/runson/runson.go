// Package runson reads the runs-on of every job in a workflow file and
// resolves it to labels (doctor R5.18, R5.19).
//
// It understands a string, a flow list, a block list, a mapping with labels,
// and `${{ fromJSON(vars.NAME) }}` with an optional `|| '<default>'`. Anything
// else that holds an expression (a matrix value) is reported as unresolved,
// never guessed.
//
// Ported from dev-standards runner_health.py (parse_runs_on, resolve_labels,
// runner_carries, is_hosted) and delivery_doctor.py workflow_jobs; tests
// follow test_dl318_doctor_locked_runner.py Workflows and the runner_health
// rows of tools/test_delivery_gate.py.
package runson

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Kind says how the labels of a job were found.
type Kind string

// Kinds.
const (
	// Literal labels written in the workflow.
	Literal Kind = "literal"
	// Variable labels read through fromJSON(vars.NAME).
	Variable Kind = "variable"
	// Unresolved holds an expression the doctor cannot resolve.
	Unresolved Kind = "unresolved"
)

// Job is the runs-on of one job.
type Job struct {
	Path string // workflow file path, such as .github/workflows/ci.yml
	Line int    // line of the runs-on value, 1-based
	Job  string // job key
	Kind Kind
	// Labels are the literal labels; for Variable they are the default after
	// `||`, when one is given.
	Labels []string
	// Var is the variable name for Variable; with a fallback chain
	// (fromJSON(vars.A || vars.B || '...')) it is the first variable and Vars
	// holds all of them in order.
	Var  string
	Vars []string
	// Expr is the raw text of an Unresolved expression.
	Expr string
}

var (
	// fromJSON(vars.NAME) with an optional `|| default` after the call.
	reVar = regexp.MustCompile(`^\$\{\{\s*fromJSON\(\s*vars\.([A-Za-z_][A-Za-z0-9_]*)\s*\)\s*(?:\|\|\s*(.+?))?\s*\}\}$`)
	// fromJSON(vars.NAME || 'default'): the usual way to write a default.
	reVarIn = regexp.MustCompile(`^\$\{\{\s*fromJSON\(\s*vars\.([A-Za-z_][A-Za-z0-9_]*)\s*\|\|\s*(.+?)\s*\)\s*\}\}$`)
	reExpr  = regexp.MustCompile(`\$\{\{`)
	reJSON  = regexp.MustCompile(`^fromJSON\(\s*'(.*)'\s*\)$`)
)

// Parse reads the runs-on of every job of a workflow file. A file that is not
// valid YAML returns an error; a job without runs-on (a reusable workflow
// call) is left out.
func Parse(path string, content []byte) ([]Job, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	root := doc.Content[0]
	jobs := mapValue(root, "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return nil, nil
	}
	var out []Job
	for i := 0; i+1 < len(jobs.Content); i += 2 {
		key, val := jobs.Content[i], jobs.Content[i+1]
		ro := mapValue(val, "runs-on")
		if ro == nil {
			continue
		}
		out = append(out, parseNode(path, key.Value, ro))
	}
	return out, nil
}

func mapValue(n *yaml.Node, key string) *yaml.Node {
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

func parseNode(path, job string, n *yaml.Node) Job {
	j := Job{Path: path, Line: n.Line, Job: job, Kind: Literal}
	unresolved := func(raw string) Job {
		j.Kind, j.Expr, j.Labels, j.Var = Unresolved, raw, nil, ""
		return j
	}
	switch n.Kind {
	case yaml.ScalarNode:
		return parseScalar(j, n.Value)
	case yaml.SequenceNode:
		for _, c := range n.Content {
			if c.Kind != yaml.ScalarNode || reExpr.MatchString(c.Value) {
				return unresolved(strings.TrimSpace(c.Value))
			}
			j.Labels = append(j.Labels, c.Value)
		}
		return j
	case yaml.MappingNode:
		// runs-on: {group: x, labels: [..]} selects by group and labels.
		if l := mapValue(n, "labels"); l != nil {
			sub := parseNode(path, job, l)
			sub.Line = l.Line
			return sub
		}
		return unresolved("group")
	}
	return unresolved("")
}

func parseScalar(j Job, v string) Job {
	v = strings.TrimSpace(v)
	if vars, def, ok := ParseChain(v); ok {
		j.Kind, j.Var, j.Vars = Variable, vars[0], vars
		if def != "" {
			labels, ok := ParseDefault(def)
			if !ok {
				j.Kind, j.Var, j.Vars, j.Expr = Unresolved, "", nil, v
				return j
			}
			j.Labels = labels
		}
		return j
	}
	m := reVar.FindStringSubmatch(v)
	if m == nil {
		m = reVarIn.FindStringSubmatch(v)
	}
	if m != nil {
		j.Kind, j.Var, j.Vars = Variable, m[1], []string{m[1]}
		if m[2] != "" {
			def, ok := ParseDefault(m[2])
			if !ok {
				j.Kind, j.Var, j.Expr = Unresolved, "", v
				return j
			}
			j.Labels = def
		}
		return j
	}
	if reExpr.MatchString(v) {
		j.Kind, j.Expr = Unresolved, v
		return j
	}
	j.Labels = []string{v}
	return j
}

// ParseDefault reads what follows `||`: 'label', '["a","b"]' or fromJSON('["a"]').
func ParseDefault(s string) ([]string, bool) {
	s = strings.TrimSpace(s)
	if m := reJSON.FindStringSubmatch(s); m != nil {
		return ParseLabelList(m[1])
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		inner := s[1 : len(s)-1]
		if strings.HasPrefix(strings.TrimSpace(inner), "[") {
			return ParseLabelList(inner)
		}
		var one string // '"ubuntu-latest"': a JSON string
		if json.Unmarshal([]byte(inner), &one) == nil && one != "" {
			return []string{one}, true
		}
		return []string{inner}, inner != ""
	}
	return nil, false
}

// ParseLabelList reads a JSON list of strings, the form of a CRSP_RUNS_ON_* variable.
func ParseLabelList(v string) ([]string, bool) {
	var l []string
	if err := json.Unmarshal([]byte(v), &l); err != nil || len(l) == 0 {
		return nil, false
	}
	return l, true
}

// Resolve gives the labels of a job. vars maps variable name to its value as
// GitHub holds it. ok is false when they cannot be known: an unresolved
// expression, or a variable that is not set and has no default. missingVar
// names the variable in the second case.
func (j Job) Resolve(vars map[string]string) (labels []string, ok bool, missingVar string) {
	switch j.Kind {
	case Literal:
		return j.Labels, true, ""
	case Variable:
		chain := j.Vars
		if len(chain) == 0 {
			chain = []string{j.Var}
		}
		for _, name := range chain {
			if v, set := vars[name]; set {
				if l, valid := ParseLabelList(v); valid {
					return l, true, ""
				}
				return nil, false, ""
			}
		}
		if len(j.Labels) > 0 {
			return j.Labels, true, ""
		}
		return nil, false, chain[len(chain)-1]
	}
	return nil, false, ""
}

// SelfHosted says whether a label set targets self-hosted runners.
func SelfHosted(labels []string) bool {
	for _, l := range labels {
		if strings.EqualFold(l, "self-hosted") {
			return true
		}
	}
	return false
}

// IsHosted says whether a label set is GitHub-hosted: no self-hosted label
// and every label names a hosted image or a larger-runner group.
func IsHosted(labels []string) bool {
	if len(labels) == 0 || SelfHosted(labels) {
		return false
	}
	return true
}

// Carries says whether one runner's labels hold every label of need (GitHub
// matches labels case-insensitively).
func Carries(runner, need []string) bool {
	have := make(map[string]bool, len(runner))
	for _, l := range runner {
		have[strings.ToLower(l)] = true
	}
	for _, n := range need {
		if !have[strings.ToLower(n)] {
			return false
		}
	}
	return true
}

// OSOfVariable maps CRSP_RUNS_ON_<OS> to the OS name, "" for any other name.
func OSOfVariable(name string) string {
	switch name {
	case "CRSP_RUNS_ON_LINUX":
		return "linux"
	case "CRSP_RUNS_ON_WINDOWS":
		return "windows"
	case "CRSP_RUNS_ON_MACOS":
		return "macos"
	}
	return ""
}

// VariableOfOS is the inverse of OSOfVariable.
func VariableOfOS(os string) string { return "CRSP_RUNS_ON_" + strings.ToUpper(os) }

// ParseChain reads ${{ fromJSON(vars.A || vars.B || 'default') }}: a chain of
// one or more variables, then an optional default, inside the call or after it.
// ok is false for any other expression.
func ParseChain(v string) (vars []string, def string, ok bool) {
	e := strings.TrimSpace(v)
	if !strings.HasPrefix(e, "${{") || !strings.HasSuffix(e, "}}") {
		return nil, "", false
	}
	e = strings.TrimSpace(e[3 : len(e)-2])
	if !strings.HasPrefix(e, "fromJSON(") {
		return nil, "", false
	}
	rest := e[len("fromJSON("):]
	depth, quote, end := 1, byte(0), -1
	for i := 0; i < len(rest) && end < 0; i++ {
		c := rest[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '(':
			depth++
		case c == ')':
			depth--
			if depth == 0 {
				end = i
			}
		}
	}
	if end < 0 {
		return nil, "", false
	}
	inner, after := rest[:end], strings.TrimSpace(rest[end+1:])
	parts := splitOr(inner)
	i := 0
	for ; i < len(parts); i++ {
		p := strings.TrimSpace(parts[i])
		if !strings.HasPrefix(p, "vars.") || !reVarName.MatchString(p[5:]) {
			break
		}
		vars = append(vars, p[5:])
	}
	if len(vars) == 0 {
		return nil, "", false
	}
	def = strings.TrimSpace(strings.Join(parts[i:], "||"))
	if after != "" {
		if !strings.HasPrefix(after, "||") || def != "" {
			return nil, "", false
		}
		def = strings.TrimSpace(after[2:])
	}
	return vars, def, true
}

var reVarName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// splitOr splits on || outside quotes.
func splitOr(s string) []string {
	var out []string
	quote, last := byte(0), 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '|' && i+1 < len(s) && s[i+1] == '|':
			out = append(out, s[last:i])
			i++
			last = i + 1
		}
	}
	return append(out, s[last:])
}
