package github

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// WorkflowGraph is the needs graph of one workflow file, with jobs named the
// way a run shows them.
type WorkflowGraph struct {
	// Needs maps a job's display name to the display names it needs.
	Needs map[string][]string
	// Dynamic marks jobs whose runs expand at run time: a matrix, a reusable
	// workflow (uses:) or a name that holds an expression.
	Dynamic map[string]bool
}

type wfJob struct {
	Name     string `yaml:"name"`
	Needs    any    `yaml:"needs"`
	Uses     string `yaml:"uses"`
	Strategy struct {
		Matrix any `yaml:"matrix"`
	} `yaml:"strategy"`
}

// ParseWorkflowGraph reads jobs.<id>.needs, name, uses and strategy.matrix of a
// workflow file. A job is keyed by its name: field when it has a plain one,
// else by its id, so an edge joins to the job of a run by name; a run's matrix
// job "test (ubuntu, 22)" joins on its type "test". Every job is a key, with an
// empty list when it needs nothing.
func ParseWorkflowGraph(content []byte) (WorkflowGraph, error) {
	var f struct {
		Jobs map[string]wfJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(content, &f); err != nil {
		return WorkflowGraph{}, fmt.Errorf("workflow: %w", err)
	}
	if len(f.Jobs) == 0 {
		return WorkflowGraph{}, fmt.Errorf("workflow: no jobs")
	}
	display := map[string]string{}
	g := WorkflowGraph{Needs: map[string][]string{}, Dynamic: map[string]bool{}}
	ids := make([]string, 0, len(f.Jobs))
	for id, j := range f.Jobs {
		ids = append(ids, id)
		name := strings.TrimSpace(j.Name)
		if name == "" || strings.Contains(name, "${{") {
			name = id
		}
		display[id] = name
	}
	sort.Strings(ids)
	for _, id := range ids {
		j := f.Jobs[id]
		name := display[id]
		needs := []string{}
		for _, n := range needList(j.Needs) {
			if d, ok := display[n]; ok {
				n = d
			}
			needs = append(needs, n)
		}
		g.Needs[name] = needs
		if j.Strategy.Matrix != nil || j.Uses != "" || strings.Contains(j.Name, "${{") {
			g.Dynamic[name] = true
		}
	}
	return g, nil
}

func needList(v any) []string {
	switch n := v.(type) {
	case string:
		return []string{n}
	case []any:
		var out []string
		for _, e := range n {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
