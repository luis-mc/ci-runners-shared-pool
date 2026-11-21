package checks

import (
	"context"
	"sort"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
)

// toolGroup is the runners that can get the same job: same repository scope
// and the same label set.
type toolGroup struct {
	scope   string
	labels  string
	runners []doctor.Runner
}

func (g toolGroup) name() string { return g.scope + " [" + g.labels + "]" }

// toolGroups groups persistent runners by scope and label set. Runners with
// different labels (another image profile, another OS) differ on purpose and
// are never compared. Only groups of two or more are returned, in a fixed order.
func toolGroups(rs []doctor.Runner) []toolGroup {
	idx := map[string]*toolGroup{}
	var keys []string
	for _, r := range rs {
		scope := runnerScopeName(r)
		if scope == "" {
			continue
		}
		ls := make([]string, 0, len(r.Labels))
		for _, l := range r.Labels {
			ls = append(ls, strings.ToLower(strings.TrimSpace(l)))
		}
		sort.Strings(ls)
		labels := strings.Join(ls, ", ")
		k := scope + "\x00" + labels
		g, ok := idx[k]
		if !ok {
			g = &toolGroup{scope: scope, labels: labels}
			idx[k] = g
			keys = append(keys, k)
		}
		g.runners = append(g.runners, r)
	}
	sort.Strings(keys)
	var out []toolGroup
	for _, k := range keys {
		if g := idx[k]; len(g.runners) >= 2 {
			sort.Slice(g.runners, func(i, j int) bool { return g.runners[i].Name < g.runners[j].Name })
			out = append(out, *g)
		}
	}
	return out
}

// runnerScopeName is the repository (owner/name) or organization a runner
// serves; empty when it has no scope. A pool runner serves each repository
// through its own registration, so it joins the group of every repository it serves.
func runnerScopeName(r doctor.Runner) string {
	switch {
	case r.Scope.Repo != "":
		return r.Scope.Owner + "/" + r.Scope.Repo
	case r.Scope.Organization != "":
		return r.Scope.Organization + " (organization)"
	}
	return ""
}

func toolSet(p *doctor.ToolsProbe) map[string]bool {
	m := map[string]bool{}
	for _, t := range p.Tools {
		m[t.Name] = t.Found
	}
	return m
}

// runnerToolsSame is R5.63: runners that can get the same job have the same
// tools. GitHub can send a job to any of them, so a tool one has and another
// lacks makes a job pass or fail by luck. The check reads only (R5.7).
func runnerToolsSame(ctx context.Context, env doctor.Env) []doctor.Finding {
	const id = "runner.tools.same"
	rs, ms, errs := persistentRunners(ctx, env, id)
	if errs != nil {
		return errs
	}
	var out []doctor.Finding
	for _, g := range toolGroups(rs) {
		gsub := doctor.Subject{Kind: "repository", ID: g.scope, Name: g.scope}
		var probed []doctor.Runner
		var why []string
		for _, r := range g.runners {
			m := ms[r.MachineID]
			switch {
			case !isOnline(env, m):
				why = append(why, r.Name+": "+m.Name+" is offline")
			case r.Tools == nil:
				why = append(why, r.Name+": the agent did not report its tools")
			case r.Tools.Err != "":
				why = append(why, r.Name+": "+r.Tools.Err)
			default:
				probed = append(probed, r)
			}
		}
		if len(probed) < 2 {
			out = append(out, skip(id, gsub, "Tools of the runners for "+g.name()+" were not compared",
				"Fewer than two of its runners reported their tools. "+strings.Join(why, "; ")+"."))
			continue
		}
		have := map[string][]string{}
		for _, r := range probed {
			for n, ok := range toolSet(r.Tools) {
				if ok {
					have[n] = append(have[n], r.Name)
				}
			}
		}
		var missing = map[string][]string{} // runner -> tools it lacks
		for _, name := range toolNames(have) {
			if len(have[name]) == len(probed) {
				continue
			}
			for _, r := range probed {
				if !toolSet(r.Tools)[name] {
					missing[r.Name] = append(missing[r.Name], name)
				}
			}
		}
		if len(missing) == 0 {
			out = append(out, doctor.Finding{Check: id, Subject: gsub, Severity: doctor.OK,
				Title: "The runners for " + g.name() + " have the same tools"})
			continue
		}
		for _, r := range probed {
			lacks := missing[r.Name]
			if len(lacks) == 0 {
				continue
			}
			f := doctor.Finding{Check: id, Subject: runnerSubject(r), Severity: doctor.Warn,
				Title: r.Name + " lacks " + strings.Join(lacks, ", ") + " that other runners for " + g.name() + " have"}
			var parts []string
			for _, t := range lacks {
				parts = append(parts, t+" is on "+strings.Join(have[t], ", "))
			}
			f.Detail = "GitHub can send a job to any of these runners, so a job that needs " + strings.Join(lacks, ", ") +
				" passes or fails by luck. " + strings.Join(parts, "; ") + "."
			f.Fix = doctor.Fix{
				Text: "Install " + strings.Join(lacks, ", ") + " for the service account of " + r.Name + " on " + ms[r.MachineID].Name +
					" and add its folder to the runner's PATH, or use the same image profile as the other runners. If the runners differ on purpose, give them different labels.",
				Link: "/runners"}
			out = append(out, f)
		}
	}
	return out
}

func toolNames(m map[string][]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// runnerToolsPlan is R5.65 (OPS75): the tool plan of the runner's workflows against the tool store of its
// machine. A runner that lacks a version the plan names warns before a job fails on it. It reads only and
// installs nothing; the agent installs a missing version at the first job that needs it (runners R5.111).
func runnerToolsPlan(ctx context.Context, env doctor.Env) []doctor.Finding {
	const id = "runner.tools.plan"
	rs, ms, out := persistentRunners(ctx, env, id)
	if out != nil {
		return out
	}
	for _, r := range rs {
		p, m := r.ToolPlan, ms[r.MachineID]
		if p == nil || !isOnline(env, m) {
			continue // no store on this OS, an older agent, or a machine the machine checks already report
		}
		sub := runnerSubject(r)
		if p.Err != "" {
			out = append(out, skip(id, sub, "The tool plan of "+r.Name+" was not compared", p.Err))
			continue
		}
		var lacks, have []string
		for _, it := range p.Items {
			if it.Present {
				have = append(have, it.Tool+" "+it.Version)
			} else {
				lacks = append(lacks, it.Tool+" "+it.Version)
			}
		}
		if len(lacks) == 0 {
			out = append(out, doctor.Finding{Check: id, Subject: sub, Severity: doctor.OK, Title: r.Name + " has every tool its workflows use"})
			continue
		}
		f := doctor.Finding{Check: id, Subject: sub, Severity: doctor.Warn,
			Title: r.Name + " lacks " + strings.Join(lacks, ", ") + ", which the workflow uses"}
		f.Detail = "The tool store on " + m.Name + " has no " + strings.Join(lacks, ", ") + "."
		if len(have) > 0 {
			f.Detail += " It has " + strings.Join(have, ", ") + "."
		}
		f.Detail += " The next job that needs it installs it first, so that job starts late, and fails if the install fails."
		f.Fix = doctor.Fix{Text: "Let the agent install it now by running a job of the repository, or change the version in the workflow, a version file or the tools: list of .crsp.yml.", Link: "/runners"}
		out = append(out, f)
	}
	return out
}
