package checks

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor/runson"
	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
)

func repoSubject(r doctor.Repository) doctor.Subject {
	return doctor.Subject{Kind: "repository", ID: fmt.Sprint(r.ID), Name: r.FullName}
}

// poolRepos lists the pool repositories in scope. It returns skip findings
// when GitHub is not connected or the list cannot be read.
func poolRepos(ctx context.Context, env doctor.Env, check string) ([]doctor.Repository, []doctor.Finding) {
	repos, err := env.Fleet.Repositories(ctx)
	if err != nil {
		return nil, []doctor.Finding{skipFor(check, controllerSubject, "The repository list", err)}
	}
	var out []doctor.Repository
	for _, r := range repos {
		if env.InScopeRepository(r) {
			out = append(out, r)
		}
	}
	if len(out) > 0 && env.GitHub == nil {
		return nil, []doctor.Finding{skip(check, controllerSubject, "GitHub is not connected", "The GitHub App is not set up yet.")}
	}
	return out, nil
}

// repoVariables reads the variables of a repository once per run (one REST call).
func repoVariables(ctx context.Context, env doctor.Env, r doctor.Repository) (map[string]string, error) {
	v, err := env.Memo(fmt.Sprintf("vars:%d", r.ID), func() (any, error) {
		list, err := env.GitHub.ListVariables(ctx, r.InstallationID, r.Owner, r.Name)
		if err != nil {
			return nil, err
		}
		m := make(map[string]string, len(list))
		for _, x := range list {
			m[x.Name] = x.Value
		}
		return m, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(map[string]string), nil
}

type workflowRead struct {
	jobs     []runson.Job
	badFiles []string
}

// repoWorkflowFiles reads the raw workflow files once per run (one REST call),
// for the checks that parse them and for the coverage list (R5.33).
func repoWorkflowFiles(ctx context.Context, env doctor.Env, r doctor.Repository) ([]github.WorkflowFile, error) {
	v, err := env.Memo(fmt.Sprintf("workflow-files:%d", r.ID), func() (any, error) {
		return env.GitHub.ListWorkflowFiles(ctx, r.InstallationID, r.Owner, r.Name, nil)
	})
	if err != nil {
		return nil, err
	}
	return v.([]github.WorkflowFile), nil
}

// repoWorkflows reads and parses the workflow files once per run (one REST call).
func repoWorkflows(ctx context.Context, env doctor.Env, r doctor.Repository) (workflowRead, error) {
	v, err := env.Memo(fmt.Sprintf("workflows:%d", r.ID), func() (any, error) {
		files, err := repoWorkflowFiles(ctx, env, r)
		if err != nil {
			return nil, err
		}
		var w workflowRead
		for _, f := range files {
			if ext := strings.ToLower(path.Ext(f.Path)); ext != ".yml" && ext != ".yaml" {
				continue
			}
			jobs, err := runson.Parse(f.Path, f.Content)
			if err != nil {
				w.badFiles = append(w.badFiles, f.Path)
				continue
			}
			w.jobs = append(w.jobs, jobs...)
		}
		return w, nil
	})
	if err != nil {
		return workflowRead{}, err
	}
	return v.(workflowRead), nil
}

// onlineRunnerLabels lists the labels of every online runner a repository can
// use: its own runners and those of its organization, when the pool has any.
func onlineRunnerLabels(ctx context.Context, env doctor.Env, r doctor.Repository) ([][]string, error) {
	scopes := []github.RunnerScope{{Owner: r.Owner, Repo: r.Name}}
	managed, err := env.Fleet.Runners(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range managed {
		if m.Scope.Organization != "" && strings.EqualFold(m.Scope.Organization, r.Owner) {
			scopes = append(scopes, github.RunnerScope{Organization: m.Scope.Organization, GroupID: m.Scope.GroupID})
			break
		}
	}
	var out [][]string
	for _, sc := range scopes {
		list, err := ghRunners(ctx, env, r.InstallationID, sc)
		if err != nil {
			return nil, err
		}
		for _, gr := range list {
			if gr.Status == "online" {
				out = append(out, gr.Labels)
			}
		}
	}
	return out, nil
}

func anyCarries(runners [][]string, need []string) bool {
	for _, l := range runners {
		if runson.Carries(l, need) {
			return true
		}
	}
	return false
}

func sameLabels(a, b []string) bool {
	norm := func(in []string) string {
		c := make([]string, len(in))
		for i, s := range in {
			c[i] = strings.ToLower(s)
		}
		sort.Strings(c)
		return strings.Join(c, "\x00")
	}
	return norm(a) == norm(b)
}

func varSubject(r doctor.Repository, name string) doctor.Subject {
	return doctor.Subject{Kind: "variable", ID: fmt.Sprintf("%d:%s", r.ID, name), Name: r.FullName + " " + name}
}

func ghCommand(r doctor.Repository, name, value string) string {
	return fmt.Sprintf("gh variable set %s --repo %s --body '%s'", name, r.FullName, value)
}

// repoVars: RUNS_ON_<OS> is set, is a JSON list, and equals what the
// product would write (R5.17). Unset where a workflow uses it, or not a JSON
// list, fails; different warns. Ported from delivery_doctor.py machine_items
// (machine.runs_on) and workflow_items (workflows.windows_var); tests
// TestRepoVars.
func repoVars(ctx context.Context, env doctor.Env) []doctor.Finding {
	repos, out := poolRepos(ctx, env, "repo.vars")
	for _, r := range repos {
		vars, err := repoVariables(ctx, env, r)
		if err != nil {
			out = append(out, skipFor("repo.vars", repoSubject(r), "The variables of "+r.FullName, err))
			continue
		}
		wf, wfErr := repoWorkflows(ctx, env, r) // a failed read means "usage unknown", not an error
		used := map[string]bool{}
		for _, j := range wf.jobs {
			if j.Kind == runson.Variable {
				used[j.Var] = true
				for _, v := range j.Vars {
					used[v] = true
				}
			}
		}
		names := make([]string, 0, len(r.Expected))
		for n := range r.Expected {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			want := r.Expected[name]
			f := doctor.Finding{Check: "repo.vars", Subject: varSubject(r, name)}
			fix := doctor.Fix{Text: "Set it from the Repositories screen.", Command: ghCommand(r, name, want), Link: "/repositories"}
			got, set := vars[name]
			switch {
			case !set && used[name]:
				f.Severity, f.Title = doctor.Fail, name+" is not set on "+r.FullName
				f.Detail = "A workflow reads it, so those jobs get no runner labels and never start."
				f.Fix = fix
			case !set:
				f.Severity, f.Title = doctor.Warn, name+" is not set on "+r.FullName
				f.Detail = "The product writes it when the pool is on. No workflow reads it yet" + unknownNote(wfErr) + "."
				f.Fix = fix
			default:
				labels, ok := runson.ParseLabelList(got)
				switch {
				case !ok:
					f.Severity, f.Title = doctor.Fail, name+" is not a JSON list of labels"
					f.Detail = "Its value is not a JSON list of strings, so fromJSON fails."
					f.Fix = fix
				case wantLabels(want) != nil && !sameLabels(labels, wantLabels(want)):
					f.Severity, f.Title = doctor.Warn, name+" differs from the value the product writes"
					f.Detail = "It is " + quote(labels) + " and should be " + quote(wantLabels(want)) + "."
					f.Fix = fix
				default:
					f.Severity, f.Title = doctor.OK, name+" is correct on "+r.FullName
				}
			}
			out = append(out, f)
		}
	}
	return out
}

func wantLabels(v string) []string {
	l, _ := runson.ParseLabelList(v)
	return l
}

func unknownNote(err error) string {
	if err != nil {
		return " (the workflows could not be read, so this is not certain)"
	}
	return ""
}

// repoVarsRunner: a RUNS_ON_<OS> whose labels no online runner carries.
// Its jobs would queue forever (R5.17). New check.
func repoVarsRunner(ctx context.Context, env doctor.Env) []doctor.Finding {
	repos, out := poolRepos(ctx, env, "repo.vars.runner")
	for _, r := range repos {
		vars, err := repoVariables(ctx, env, r)
		if err != nil {
			out = append(out, skipFor("repo.vars.runner", repoSubject(r), "The variables of "+r.FullName, err))
			continue
		}
		var names []string
		for _, os := range []string{"linux", "windows", "macos"} {
			n := runson.VariableOfOS(os)
			if v, ok := vars[n]; ok {
				if _, valid := runson.ParseLabelList(v); valid {
					names = append(names, n)
				}
			}
		}
		if len(names) == 0 {
			continue
		}
		runners, err := onlineRunnerLabels(ctx, env, r)
		if err != nil {
			out = append(out, skipFor("repo.vars.runner", repoSubject(r), "The runners of "+r.FullName, err))
			continue
		}
		for _, n := range names {
			labels, _ := runson.ParseLabelList(vars[n])
			f := doctor.Finding{Check: "repo.vars.runner", Subject: varSubject(r, n)}
			if hosted(r, runson.OSOfVariable(n)) {
				// On the hosted fallback the variable names a GitHub-hosted label on purpose.
				f.Severity, f.Title = doctor.OK, n+" is on a GitHub-hosted label on purpose (hosted fallback)"
				out = append(out, f)
				continue
			}
			if anyCarries(runners, labels) {
				f.Severity, f.Title = doctor.OK, "A runner is online for "+n+" on "+r.FullName
			} else {
				f.Severity, f.Title = doctor.Fail, "No online runner carries the labels of "+n
				f.Detail = n + " is " + quote(labels) + " on " + r.FullName + ". Jobs that use it would queue forever."
				f.Fix = doctor.Fix{Text: "Add or start a runner for " + osTitle(runson.OSOfVariable(n)) + " with these labels, or turn the pool off for the repository.", Link: "/runners"}
			}
			out = append(out, f)
		}
	}
	return out
}

// jobsWithRunners resolves every job of a repository.
type resolvedJob struct {
	runson.Job
	Labels  []string // resolved
	OK      bool
	Missing string
}

func resolveJobs(vars map[string]string, jobs []runson.Job) []resolvedJob {
	out := make([]resolvedJob, 0, len(jobs))
	for _, j := range jobs {
		l, ok, miss := j.Resolve(vars)
		out = append(out, resolvedJob{Job: j, Labels: l, OK: ok, Missing: miss})
	}
	return out
}

func where(j runson.Job) string { return fmt.Sprintf("%s:%d (job %s)", j.Path, j.Line, j.Job) }

type workflowRepo struct {
	r    doctor.Repository
	jobs []resolvedJob
	wf   workflowRead
}

// workflowRepos loads and resolves the workflows of every pool repository.
func workflowRepos(ctx context.Context, env doctor.Env, check string) ([]workflowRepo, []doctor.Finding) {
	repos, out := poolRepos(ctx, env, check)
	var res []workflowRepo
	for _, r := range repos {
		vars, err := repoVariables(ctx, env, r)
		if err != nil {
			out = append(out, skipFor(check, repoSubject(r), "The variables of "+r.FullName, err))
			continue
		}
		wf, err := repoWorkflows(ctx, env, r)
		if err != nil {
			out = append(out, skipFor(check, repoSubject(r), "The workflows of "+r.FullName, err))
			continue
		}
		res = append(res, workflowRepo{r: r, jobs: resolveJobs(vars, wf.jobs), wf: wf})
	}
	return res, out
}

// workflowLabels: a self-hosted runs-on that no online runner carries in full
// fails, with file and line. GitHub-hosted label sets are skipped (R5.18).
// Ported from delivery_doctor.py workflow_jobs and workflow_items and
// runner_health.py check; tests TestWorkflowLabels.
func workflowLabels(ctx context.Context, env doctor.Env) []doctor.Finding {
	wrs, out := workflowRepos(ctx, env, "workflow.labels")
	for _, w := range wrs {
		s := repoSubject(w.r)
		var lines []string
		checked := 0
		for _, j := range w.jobs {
			if !j.OK || !runson.SelfHosted(j.Labels) {
				continue
			}
			checked++
			runners, err := onlineRunnerLabels(ctx, env, w.r)
			if err != nil {
				out = append(out, skipFor("workflow.labels", s, "The runners of "+w.r.FullName, err))
				lines = nil
				checked = -1
				break
			}
			if !anyCarries(runners, j.Labels) {
				lines = append(lines, fmt.Sprintf("%s needs %s", where(j.Job), quote(j.Labels)))
			}
		}
		switch {
		case checked < 0:
			continue
		case len(lines) > 0:
			out = append(out, doctor.Finding{Check: "workflow.labels", Subject: s, Severity: doctor.Fail,
				Title:  "No online runner carries the labels some workflows ask for in " + w.r.FullName,
				Detail: strings.Join(lines, "\n"),
				Fix: doctor.Fix{Text: "Add a runner with the labels, or change the workflow to `runs-on: ${{ fromJSON(vars.RUNS_ON_<OS>) }}`.",
					Link: "/runners"}})
		default:
			out = append(out, doctor.Finding{Check: "workflow.labels", Subject: s, Severity: doctor.OK,
				Title: fmt.Sprintf("Every self-hosted job in %s has a runner (%d checked)", w.r.FullName, checked)})
		}
	}
	return out
}

// workflowGeneric: a self-hosted job with literal labels instead of
// fromJSON(vars.RUNS_ON_<OS>) (R5.19). Ported from delivery_doctor.py
// workflow_items (workflows.generic); tests TestWorkflowGeneric.
func workflowGeneric(ctx context.Context, env doctor.Env) []doctor.Finding {
	wrs, out := workflowRepos(ctx, env, "workflow.generic")
	for _, w := range wrs {
		var lines []string
		for _, j := range w.jobs {
			if j.Kind == runson.Literal && runson.SelfHosted(j.Labels) {
				lines = append(lines, fmt.Sprintf("%s uses %s", where(j.Job), quote(j.Labels)))
			}
		}
		f := doctor.Finding{Check: "workflow.generic", Subject: repoSubject(w.r)}
		if len(lines) == 0 {
			f.Severity, f.Title = doctor.OK, "No self-hosted job in "+w.r.FullName+" uses literal labels"
		} else {
			f.Severity, f.Title = doctor.Warn, "Some jobs in "+w.r.FullName+" write their runner labels by hand"
			f.Detail = strings.Join(lines, "\n")
			f.Fix = doctor.Fix{Text: "Use `runs-on: ${{ fromJSON(vars.RUNS_ON_<OS>) }}` so the product keeps the labels correct."}
		}
		out = append(out, f)
	}
	return out
}

// workflowUnresolved: a runs-on expression the doctor cannot resolve is info:
// it says it was not checked (R5.18). A variable that is not set, a workflow
// file that does not parse, and a matrix value all land here.
func workflowUnresolved(ctx context.Context, env doctor.Env) []doctor.Finding {
	wrs, out := workflowRepos(ctx, env, "workflow.unresolved")
	for _, w := range wrs {
		var lines []string
		for _, j := range w.jobs {
			switch {
			case j.OK:
			case j.Missing != "":
				lines = append(lines, fmt.Sprintf("%s reads %s, which is not set", where(j.Job), j.Missing))
			case j.Kind == runson.Unresolved:
				lines = append(lines, fmt.Sprintf("%s has runs-on %s", where(j.Job), j.Expr))
			default:
				lines = append(lines, fmt.Sprintf("%s has a variable that is not a JSON list", where(j.Job)))
			}
		}
		for _, p := range w.wf.badFiles {
			lines = append(lines, p+" is not valid YAML")
		}
		f := doctor.Finding{Check: "workflow.unresolved", Subject: repoSubject(w.r)}
		if len(lines) == 0 {
			f.Severity, f.Title = doctor.OK, "Every runs-on in "+w.r.FullName+" could be resolved"
		} else {
			f.Severity, f.Title = doctor.Info, "Some runs-on values in "+w.r.FullName+" were not checked"
			f.Detail = strings.Join(lines, "\n")
			f.Fix = doctor.Fix{Text: "Nothing to do. The doctor cannot resolve these, so it did not check them."}
		}
		out = append(out, f)
	}
	return out
}
