package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/client"
	"github.com/luis-mc/ci-runners-shared-pool/internal/cli/output"
)

// sleep waits for d, or the poll override, and stops early on Ctrl+C.
func (c *call) sleep(d time.Duration) error {
	if c.env.PollEvery > 0 {
		d = c.env.PollEvery
	}
	select {
	case <-c.env.Context.Done():
		return fail(ExitFailed, "Stopped.")
	case <-time.After(d):
		return nil
	}
}

func (c *call) yesFlag() bool {
	f := c.fs.Lookup("yes")
	return f != nil && f.Value.String() == "true"
}

func yesFlag(fs *flag.FlagSet) { fs.Bool("yes", false, "do not ask; still print what changes") }

// listRepos returns every repository the user can see, following cursors.
func listRepos(ctx context.Context, s *session, p client.ListRepositoriesParams) ([]client.Repository, error) {
	var all []client.Repository
	for i := 0; i < 200; i++ {
		r, err := s.client.ListRepositoriesWithResponse(ctx, &p)
		if err != nil {
			return nil, s.transport(err)
		}
		if err := apiError(r.StatusCode(), r.Body); err != nil {
			return nil, err
		}
		if r.JSON200 == nil {
			break
		}
		all = append(all, r.JSON200.Items...)
		if r.JSON200.NextCursor == nil || *r.JSON200.NextCursor == "" {
			break
		}
		p.Cursor = r.JSON200.NextCursor
	}
	return all, nil
}

func (c *call) findRepo(s *session, full string) (client.Repository, error) {
	owner, name, ok := strings.Cut(full, "/")
	if !ok || owner == "" || name == "" {
		return client.Repository{}, fail(ExitUsage, "Name the repository as OWNER/NAME, for example luis-mc/app.")
	}
	repos, err := listRepos(c.env.Context, s, client.ListRepositoriesParams{Q: &name})
	if err != nil {
		return client.Repository{}, err
	}
	for _, r := range repos {
		if strings.EqualFold(r.FullName, full) {
			return r, nil
		}
	}
	return client.Repository{}, fail(ExitNotFound, "No repository %s among the installations. Run crsp repo list.", full)
}

func varSummary(vs []client.RepositoryVariable) string {
	if len(vs) == 0 {
		return "-"
	}
	bad := map[string]bool{}
	for _, v := range vs {
		if v.Status != client.VariableStatusOk {
			bad[string(v.Status)] = true
		}
	}
	if len(bad) == 0 {
		return "ok"
	}
	var parts []string
	for k := range bad {
		parts = append(parts, k)
	}
	return strings.Join(sortedStrings(parts), ",")
}

func cmdRepo() *command {
	return &command{
		name: "repo", summary: "repositories and their pool policy",
		subs: []*command{
			{name: "list", summary: "list repositories", args: "[--pool on|off] [--q TEXT]",
				flags: func(c *call, fs *flag.FlagSet) {
					fs.String("pool", "", "show only on or off")
					fs.String("q", "", "search by name")
				},
				run: func(c *call, _ []string) error {
					s, err := c.open()
					if err != nil {
						return err
					}
					var p client.ListRepositoriesParams
					if v := c.fs.Lookup("pool").Value.String(); v != "" {
						if v != "on" && v != "off" {
							return fail(ExitUsage, "--pool takes on or off.")
						}
						pv := client.ListRepositoriesParamsPool(v)
						p.Pool = &pv
					}
					if v := c.fs.Lookup("q").Value.String(); v != "" {
						p.Q = &v
					}
					repos, err := listRepos(c.env.Context, s, p)
					if err != nil {
						return err
					}
					if c.g.json {
						return output.JSON(c.env.Stdout, client.RepositoryPage{Items: nonNil(repos)})
					}
					if len(repos) == 0 {
						_, _ = fmt.Fprintln(c.env.Stderr, "No repositories yet. Install the GitHub App on a repository first.")
						return nil
					}
					var rows [][]string
					for _, r := range repos {
						pool := "off"
						if r.PoolOn {
							pool = "on"
						}
						rows = append(rows, []string{r.FullName, pool, strings.Join(r.Policy.Labels, ","), fmt.Sprint(r.Policy.MaxParallelJobs), fmt.Sprint(r.Policy.Priority), varSummary(r.Variables)})
					}
					output.Table(c.env.Stdout, output.MaxWidth, []string{"REPOSITORY", "POOL", "LABELS", "MAX", "PRIO", "VARIABLES"}, rows)
					return nil
				}},
			{name: "enable", summary: "turn the pool on for a repository", args: "OWNER/NAME [--yes]", flags: func(c *call, fs *flag.FlagSet) { yesFlag(fs) },
				run: func(c *call, args []string) error { return c.setPool(args, "on") }},
			{name: "disable", summary: "turn the pool off for a repository", args: "OWNER/NAME [--yes]", flags: func(c *call, fs *flag.FlagSet) { yesFlag(fs) },
				run: func(c *call, args []string) error { return c.setPool(args, "off") }},
			{name: "policy", summary: "show or change a repository's policy", args: "OWNER/NAME [--labels A,B] [--max-parallel N] [--priority N] [--allow-fork-prs] [--runner-kind ephemeral-only|any-runner|default] [--yes]",
				flags: func(c *call, fs *flag.FlagSet) {
					fs.String("labels", "", "generic labels, comma separated")
					fs.Int("max-parallel", 0, "most jobs at once")
					fs.Int("priority", 0, "higher goes first")
					fs.Bool("allow-fork-prs", false, "run jobs from fork pull requests")
					fs.String("runner-kind", "", "which runners take the jobs: ephemeral-only, any-runner, or default (public: ephemeral-only, private: any-runner)")
					yesFlag(fs)
				},
				run: func(c *call, args []string) error { return c.repoPolicy(args) }},
			{name: "packages", summary: "show or set the extra distribution packages of a repository's pool", args: "OWNER/NAME [--set A,B]",
				flags: func(c *call, fs *flag.FlagSet) {
					fs.String("set", "", "the whole list, names comma separated; an empty value clears it")
				},
				run: func(c *call, args []string) error { return c.repoPackages(args) }},
		},
	}
}

func (c *call) setPool(args []string, pool string) error {
	if len(args) != 1 {
		return fail(ExitUsage, "Usage: crsp repo %s OWNER/NAME", map[string]string{"on": "enable", "off": "disable"}[pool])
	}
	s, err := c.open()
	if err != nil {
		return err
	}
	repo, err := c.findRepo(s, args[0])
	if err != nil {
		return err
	}
	pr, err := s.client.PlanRepositoryChangesWithResponse(c.env.Context, client.PlanRepositoryChangesRequest{Changes: []struct {
		Pool         client.PlanRepositoryChangesRequestChangesPool `json:"pool"`
		RepositoryId int64                                          `json:"repository_id"`
	}{{Pool: client.PlanRepositoryChangesRequestChangesPool(pool), RepositoryId: repo.Id}}})
	if err != nil {
		return s.transport(err)
	}
	if err := apiError(pr.StatusCode(), pr.Body); err != nil {
		return err
	}
	if pr.JSON201 == nil {
		return fail(ExitFailed, "The controller sent no plan.")
	}
	get := func() (*client.Plan, error) {
		r, err := s.client.GetRepositoryPlanWithResponse(c.env.Context, pr.JSON201.Id)
		if err != nil {
			return nil, s.transport(err)
		}
		if err := apiError(r.StatusCode(), r.Body); err != nil {
			return nil, err
		}
		return r.JSON200, nil
	}
	apply := func(ids []string) (*client.Plan, error) {
		r, err := s.client.ApplyRepositoryChangesWithResponse(c.env.Context, pr.JSON201.Id, client.ApplyPlanRequest{ApprovedSteps: ids})
		if err != nil {
			return nil, s.transport(err)
		}
		if err := apiError(r.StatusCode(), r.Body); err != nil {
			return nil, err
		}
		return r.JSON202, nil
	}
	title := "Turning the pool on for " + repo.FullName + ":"
	if pool == "off" {
		title = "Turning the pool off for " + repo.FullName + " removes the pool variables it wrote:"
	}
	done, err := c.runPlan(pr.JSON201, title, pool == "off", get, apply)
	if err != nil {
		return err
	}
	if c.g.json {
		return output.JSON(c.env.Stdout, done)
	}
	_, _ = fmt.Fprintf(c.env.Stderr, "Pool %s for %s.\n", pool, repo.FullName)
	return nil
}

// runPlan shows a plan, asks when a step deletes or the command removes, applies
// it and waits for the outcome (cli R5.14, github-integration R5.20).
func (c *call) runPlan(plan *client.Plan, title string, alwaysAsk bool, get func() (*client.Plan, error), apply func([]string) (*client.Plan, error)) (*client.Plan, error) {
	var items, ids []string
	ask := alwaysAsk
	for _, st := range plan.Steps {
		items = append(items, st.Text)
		ids = append(ids, st.Id)
		if st.NeedsYes {
			ask = true
		}
	}
	if ask {
		if err := c.confirm(title, items, c.yesFlag()); err != nil {
			return nil, err
		}
	} else {
		_, _ = fmt.Fprintln(c.env.Stderr, title)
		for _, it := range items {
			_, _ = fmt.Fprintf(c.env.Stderr, "  - %s\n", it)
		}
	}
	cur, err := apply(ids)
	if err != nil {
		return nil, err
	}
	for i := 0; cur != nil && cur.State == client.PlanStateApplying && i < 3600; i++ {
		if err := c.sleep(time.Second); err != nil {
			return nil, err
		}
		if cur, err = get(); err != nil {
			return nil, err
		}
	}
	if cur == nil {
		return plan, nil
	}
	switch cur.State {
	case client.PlanStateFailed, client.PlanStateStopped:
		msg := "The change did not finish."
		for _, st := range cur.Steps {
			if st.Error != nil && *st.Error != "" {
				msg += " " + st.Text + ": " + *st.Error + "."
			}
		}
		return nil, fail(ExitFailed, "%s", msg)
	case client.PlanStateStale:
		return nil, fail(ExitConflict, "The state changed since the plan was made. Nothing changed. Try again.")
	}
	return cur, nil
}

func (c *call) repoPolicy(args []string) error {
	if len(args) != 1 {
		return fail(ExitUsage, "Usage: crsp repo policy OWNER/NAME [flags]")
	}
	s, err := c.open()
	if err != nil {
		return err
	}
	repo, err := c.findRepo(s, args[0])
	if err != nil {
		return err
	}
	set := map[string]bool{}
	c.fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	var upd client.RepositoryPolicyUpdate
	changed := false
	if set["labels"] {
		l := splitList(c.fs.Lookup("labels").Value.String())
		upd.Labels, changed = &l, true
	}
	if set["max-parallel"] {
		n := mustInt(c.fs.Lookup("max-parallel").Value.String())
		upd.MaxParallelJobs, changed = &n, true
	}
	if set["priority"] {
		n := mustInt(c.fs.Lookup("priority").Value.String())
		upd.Priority, changed = &n, true
	}
	if set["allow-fork-prs"] {
		b := c.fs.Lookup("allow-fork-prs").Value.String() == "true"
		upd.AllowForkPullRequests, changed = &b, true
		if b {
			if err := c.confirm("Fork pull requests run code from people outside your team on your machines. Only turn this on for a repository you trust:", []string{repo.FullName + ": allow jobs from fork pull requests"}, c.yesFlag()); err != nil {
				return err
			}
			t := true
			upd.ConfirmForkRisk = &t
		}
	}
	if set["runner-kind"] {
		k, err := parseRunnerKind(c.fs.Lookup("runner-kind").Value.String())
		if err != nil {
			return err
		}
		upd.RunnerKinds, changed = &k, true
		if k == client.RepositoryPolicyUpdateRunnerKindsAnyRunner && repo.Visibility == client.Public {
			_, _ = fmt.Fprintf(c.env.Stderr, "Warning: %s is a public repository. With Any runner, its jobs may run on a persistent runner that keeps files between jobs, and anyone who can open a pull request can start those jobs. Ephemeral only is the safe choice.\n", repo.FullName)
		}
	}
	policy := repo.Policy
	if changed {
		r, err := s.client.UpdateRepositoryPolicyWithResponse(c.env.Context, repo.Id, upd)
		if err != nil {
			return s.transport(err)
		}
		if err := apiError(r.StatusCode(), r.Body); err != nil {
			return err
		}
		if r.JSON200 != nil {
			policy = *r.JSON200
		}
	}
	if c.g.json {
		return output.JSON(c.env.Stdout, policy)
	}
	_, _ = fmt.Fprintf(c.env.Stdout, "Repository:      %s\nLabels:          %s\nMax parallel:    %d\nPriority:        %d\nFork PRs:        %s\nRunner type:     %s\n",
		repo.FullName, strings.Join(policy.Labels, ", "), policy.MaxParallelJobs, policy.Priority, onOff(policy.AllowForkPullRequests), repoKindText(policy))
	if changed {
		_, _ = fmt.Fprintln(c.env.Stderr, "Policy saved.")
	}
	return nil
}

// repoPackages shows the extra packages of a repository's pool, or replaces
// the list with --set (runners R5.51). A bad name is refused by the server with
// its line number and nothing changes.
func (c *call) repoPackages(args []string) error {
	if len(args) != 1 {
		return fail(ExitUsage, "Usage: crsp repo packages OWNER/NAME [--set A,B]")
	}
	s, err := c.open()
	if err != nil {
		return err
	}
	repo, err := c.findRepo(s, args[0])
	if err != nil {
		return err
	}
	var list client.ExtraPackages
	if c.fs.Lookup("set") != nil && flagSet(c.fs, "set") {
		text := strings.Join(splitList(c.fs.Lookup("set").Value.String()), "\n")
		r, err := s.client.SetRepositoryExtraPackagesWithResponse(c.env.Context, repo.Id, client.ExtraPackagesUpdate{Text: text})
		if err != nil {
			return s.transport(err)
		}
		if err := apiError(r.StatusCode(), r.Body); err != nil {
			return err
		}
		if r.JSON200 != nil {
			list = *r.JSON200
		}
		defer func() {
			_, _ = fmt.Fprintln(c.env.Stderr, "Extra packages saved. A changed list builds a new image aside.")
		}()
	} else {
		r, err := s.client.GetRepositoryExtraPackagesWithResponse(c.env.Context, repo.Id)
		if err != nil {
			return s.transport(err)
		}
		if err := apiError(r.StatusCode(), r.Body); err != nil {
			return err
		}
		if r.JSON200 != nil {
			list = *r.JSON200
		}
	}
	if c.g.json {
		return output.JSON(c.env.Stdout, list)
	}
	if len(list.Packages) == 0 {
		_, _ = fmt.Fprintf(c.env.Stdout, "%s: no extra packages.\n", repo.FullName)
		return nil
	}
	_, _ = fmt.Fprintf(c.env.Stdout, "%s: %s\n", repo.FullName, strings.Join(list.Packages, ", "))
	return nil
}

func flagSet(fs *flag.FlagSet, name string) bool {
	seen := false
	fs.Visit(func(f *flag.Flag) { seen = seen || f.Name == name })
	return seen
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// parseRunnerKind reads the --runner-kind value (github-integration R5.60).
func parseRunnerKind(v string) (client.RepositoryPolicyUpdateRunnerKinds, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "ephemeral-only", "ephemeral", "ephemeral_only":
		return client.RepositoryPolicyUpdateRunnerKindsEphemeralOnly, nil
	case "any-runner", "any", "any_runner":
		return client.RepositoryPolicyUpdateRunnerKindsAnyRunner, nil
	case "default":
		return client.RepositoryPolicyUpdateRunnerKindsDefault, nil
	}
	return "", fail(ExitUsage, "--runner-kind takes ephemeral-only, any-runner or default.")
}

// repoKindText says which runners take a repository's jobs and whether the
// owner chose it or it is the default of the visibility.
func repoKindText(p client.RepositoryPolicy) string {
	if p.RunnerKinds == nil {
		return "-"
	}
	text := "Any runner"
	if *p.RunnerKinds == client.RepositoryPolicyRunnerKindsEphemeralOnly {
		text = "Ephemeral only"
	}
	if p.RunnerKindsSet != nil && *p.RunnerKindsSet {
		return text + " (set by you)"
	}
	return text + " (default)"
}
