package checks

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor/runson"
)

// capability is one thing a job can need from the pool (doctor R5.33). Release
// is the release of the parity plan that adds it; Built says this build offers it.
type capability struct {
	Name    string
	Release string
	Built   bool
	// Words are matched against the lower-case text of the job.
	Words []string
}

// capabilities is the table of the first version (v0.2.0): Windows jobs are
// offered; every other need is listed with the release that adds it. Each
// parity release flips Built for what it adds.
var capabilities = []capability{
	{Name: "database", Release: "v0.4.0", Words: []string{"pool-db-env", "crsp db-env", "database_url"}},
	{Name: "browsers", Release: "v0.4.0", Words: []string{"playwright"}},
	{Name: "llvm", Release: "v0.5.0", Words: []string{"cargo xwin", "xwin_"}},
	{Name: "windows-packaging", Release: "v0.5.0", Words: []string{"wine", "msitools", "delivery_shard: msi"}},
	{Name: "linux-packaging", Release: "v0.5.0", Words: []string{"rpmbuild", "checkmodule", "createrepo"}},
	{Name: "kvm", Release: "v0.6.0", Words: []string{"vm_check", "crsp vm"}},
}

// baseNeed is what a self-hosted job needs from the OS alone.
var baseNeed = map[string]capability{
	"windows": {Name: "windows-runner", Release: "v0.1.0", Built: true},
	"linux":   {Name: "linux-container", Release: "v0.3.0"},
	"macos":   {Name: "macos-runner", Release: "none planned"},
}

// JobNeeds is what one job of a workflow needs, read from its text.
type JobNeeds struct {
	Path, Job, OS string
	Line          int
	// Needs are the capability names found in the job text.
	Needs []string
	// Missing are the needs this build does not offer, with their release.
	Missing []string
	// Unknown is true when the runs-on cannot be resolved: the job is not checked.
	Unknown bool
}

// CapabilitiesFromWorkflow reads every job of one workflow file that runs on the
// pool. vars are the repository variables, used to resolve runs-on. A job on a
// GitHub-hosted runner is left out: it needs nothing from the pool.
func CapabilitiesFromWorkflow(file string, content []byte, vars map[string]string) ([]JobNeeds, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	runs, err := runson.Parse(file, content)
	if err != nil {
		return nil, err
	}
	byJob := map[string]runson.Job{}
	for _, j := range runs {
		byJob[j.Job] = j
	}
	texts := jobTexts(&doc, content)
	var out []JobNeeds
	for _, name := range sortedKeys(texts) {
		rj, ok := byJob[name]
		if !ok {
			continue // a reusable workflow call has no runs-on
		}
		n := JobNeeds{Path: file, Job: name, Line: rj.Line}
		labels, resolved, _ := rj.Resolve(vars)
		switch {
		case resolved && runson.IsHosted(labels):
			continue
		case resolved:
			n.OS = osOfLabels(labels)
		case rj.Kind == runson.Variable:
			n.OS = osOfVariable(rj.Var)
			for _, v := range rj.Vars { // fromJSON(vars.CRSP_E2E || vars.RUNS_ON_LINUX || ...)
				if n.OS == "" {
					n.OS = osOfVariable(v)
				}
			}
		}
		if n.OS == "" {
			n.Unknown = true
			out = append(out, n)
			continue
		}
		low := strings.ToLower(texts[name])
		needs := []capability{baseNeed[n.OS]}
		for _, c := range capabilities {
			if containsAny(low, c.Words) {
				needs = append(needs, c)
			}
		}
		for _, c := range needs {
			n.Needs = append(n.Needs, c.Name)
			if !c.Built {
				n.Missing = append(n.Missing, c.Name+" ("+c.Release+")")
			}
		}
		out = append(out, n)
	}
	return out, nil
}

// jobTexts slices the file into one text per job, from the job key to the next.
func jobTexts(doc *yaml.Node, content []byte) map[string]string {
	out := map[string]string{}
	root := doc.Content[0]
	var jobs *yaml.Node
	for i := 0; root.Kind == yaml.MappingNode && i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "jobs" {
			jobs = root.Content[i+1]
		}
	}
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return out
	}
	lines := strings.Split(string(content), "\n")
	for i := 0; i+1 < len(jobs.Content); i += 2 {
		from := jobs.Content[i].Line - 1
		to := len(lines)
		if i+2 < len(jobs.Content) {
			to = jobs.Content[i+2].Line - 1
		}
		if from < 0 || from > to || to > len(lines) {
			continue
		}
		out[jobs.Content[i].Value] = strings.Join(lines[from:to], "\n")
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	k := make([]string, 0, len(m))
	for n := range m {
		k = append(k, n)
	}
	sort.Strings(k)
	return k
}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

func osOfLabels(labels []string) string {
	for _, l := range labels {
		switch strings.ToLower(l) {
		case "windows", "linux", "macos":
			return strings.ToLower(l)
		}
	}
	return ""
}

// osOfVariable knows RUNS_ON_<OS> and the old names of github-integration R5.39.
func osOfVariable(name string) string {
	if os := runson.OSOfVariable(name); os != "" {
		return os
	}
	switch name {
	case "CI_RUNS_ON":
		return "linux"
	case "CI_RUNS_ON_WINDOWS":
		return "windows"
	}
	return ""
}

func coverageSubject(r doctor.Repository, os string) doctor.Subject {
	return doctor.Subject{Kind: "repository_os", ID: fmt.Sprintf("%d:%s", r.ID, os), Name: r.FullName + " (" + osTitle(os) + ")"}
}

// repoCoverage lists, for each pool repository and OS, whether the pool can run
// every job the workflows send to it (doctor R5.33, first version). `covered`
// is ok. A missing capability and a job that cannot be classified are info:
// "not checked" is never ok.
func repoCoverage(ctx context.Context, env doctor.Env) []doctor.Finding {
	repos, out := poolRepos(ctx, env, "repo.coverage")
	for _, r := range repos {
		vars, err := repoVariables(ctx, env, r)
		if err != nil {
			out = append(out, skipFor("repo.coverage", repoSubject(r), "The variables of "+r.FullName, err))
			continue
		}
		files, err := repoWorkflowFiles(ctx, env, r)
		if err != nil {
			out = append(out, skipFor("repo.coverage", repoSubject(r), "The workflows of "+r.FullName, err))
			continue
		}
		var jobs []JobNeeds
		for _, f := range files {
			if ext := strings.ToLower(path.Ext(f.Path)); ext != ".yml" && ext != ".yaml" {
				continue
			}
			j, err := CapabilitiesFromWorkflow(f.Path, f.Content, vars)
			if err != nil {
				jobs = append(jobs, JobNeeds{Path: f.Path, Job: "(whole file)", Unknown: true})
				continue
			}
			jobs = append(jobs, j...)
		}
		out = append(out, coverageRows(r, jobs)...)
	}
	return out
}

func coverageRows(r doctor.Repository, jobs []JobNeeds) []doctor.Finding {
	var out []doctor.Finding
	var unknown []string
	byOS := map[string][]JobNeeds{}
	for _, j := range jobs {
		if j.Unknown {
			unknown = append(unknown, fmt.Sprintf("%s:%d (job %s)", j.Path, j.Line, j.Job))
			continue
		}
		byOS[j.OS] = append(byOS[j.OS], j)
	}
	for _, os := range []string{"linux", "windows", "macos"} {
		js := byOS[os]
		if len(js) == 0 {
			continue
		}
		missing := map[string][]string{}
		for _, j := range js {
			for _, m := range j.Missing {
				missing[m] = append(missing[m], j.Job)
			}
		}
		f := doctor.Finding{Check: "repo.coverage", Subject: coverageSubject(r, os)}
		if len(missing) == 0 {
			f.Severity, f.Title = doctor.OK, "covered"
			f.Detail = fmt.Sprintf("The pool can run all %d %s jobs of %s.", len(js), osTitle(os), r.FullName)
			out = append(out, f)
			continue
		}
		var names []string
		for m := range missing {
			names = append(names, m)
		}
		sort.Strings(names)
		var parts []string
		for _, m := range names {
			parts = append(parts, m+": "+strings.Join(missing[m], ", "))
		}
		f.Severity, f.Title = doctor.Info, "missing: "+strings.Join(names, ", ")
		f.Detail = "These jobs need what the pool does not offer yet: " + strings.Join(parts, "; ") + "."
		f.Fix = doctor.Fix{Text: "Keep the " + osTitle(os) + " jobs of this repository on the old pool until these releases (see the parity plan)."}
		out = append(out, f)
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		out = append(out, doctor.Finding{Check: "repo.coverage", Subject: repoSubject(r), Severity: doctor.Info,
			Title:  "not checked",
			Detail: "The doctor cannot tell what these jobs need: " + strings.Join(unknown, ", ") + ".",
			Fix:    doctor.Fix{Text: "Write runs-on with a variable (fromJSON(vars.RUNS_ON_<OS>)) or labels so the job can be read."}})
	}
	return out
}
