package cli

import (
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/client"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cli/output"
)

func cmdRunner() *command {
	return &command{
		name: "runner", summary: "runners on machines",
		subs: []*command{
			{name: "list", summary: "list runners", args: "[--machine M] [--os OS]",
				flags: func(c *call, fs *flag.FlagSet) {
					fs.String("machine", "", "only this machine")
					fs.String("os", "", "only this OS (linux, windows, macos)")
				},
				run: func(c *call, _ []string) error {
					s, err := c.open()
					if err != nil {
						return err
					}
					ms, _ := c.machines(s)
					names := map[string]string{}
					for _, m := range ms {
						names[m.Id] = m.Name
					}
					var p client.ListRunnersParams
					if v := c.fs.Lookup("machine").Value.String(); v != "" {
						m, err := c.findMachine(s, v)
						if err != nil {
							return err
						}
						p.MachineId = &m.Id
					}
					if v := c.fs.Lookup("os").Value.String(); v != "" {
						o := client.OS(v)
						p.Os = &o
					}
					rs, err := c.runners(s, p)
					if err != nil {
						return err
					}
					if c.g.json {
						return output.JSON(c.env.Stdout, client.RunnerList{Items: nonNil(rs)})
					}
					if len(rs) == 0 {
						_, _ = fmt.Fprintln(c.env.Stderr, "No runners yet. Run `crsp runner add` to add one.")
						return nil
					}
					var rows [][]string
					for _, r := range rs {
						mach := names[r.MachineId]
						if mach == "" {
							mach = r.MachineId
						}
						job := "-"
						if r.CurrentJob != nil {
							job = r.CurrentJob.JobName
						}
						rows = append(rows, []string{r.Name, mach, string(r.Os), string(r.Backend), string(r.Kind), keepsText(r), strings.Join(r.Labels, ","), job, output.Age(r.CreatedAt, c.env.Now())})
					}
					output.Table(c.env.Stdout, output.MaxWidth, []string{"NAME", "MACHINE", "OS", "BACKEND", "KIND", "DEPS", "LABELS", "JOB", "AGE"}, rows)
					return nil
				}},
			runnerAddCommand("add"),
			{name: "update", summary: "change a persistent runner's setting", args: "NAME --keep-dependencies|--no-keep-dependencies",
				flags: func(_ *call, fs *flag.FlagSet) { keepDependenciesFlags(fs) },
				run:   func(c *call, args []string) error { return c.runnerUpdate(args) }},
			{name: "clear-cache", summary: "remove a persistent runner's downloaded dependencies now", args: "NAME [--yes]",
				flags: func(_ *call, fs *flag.FlagSet) { yesFlag(fs) },
				run:   func(c *call, args []string) error { return c.runnerClearCache(args) }},
			{name: "env", summary: "show the merged environment of a runner (sensitive values masked)", args: "NAME",
				run: func(c *call, args []string) error { return c.runnerEnv(args) }},
			{name: "remove", summary: "remove a runner after its job ends", args: "NAME [--yes]",
				flags: drainFlags,
				run:   func(c *call, args []string) error { return c.runnerRemove(args) }},
			{name: "delete-offline", summary: "delete the registration of an offline runner whose machine cannot remove it", args: "NAME [--yes]",
				flags: func(_ *call, fs *flag.FlagSet) { yesFlag(fs) },
				run:   func(c *call, args []string) error { return c.runnerDeleteOffline(args) }},
		},
	}
}

// runnerAddCommand is `runner add` (the verb is add, never create).
func runnerAddCommand(name string) *command {
	return &command{name: name, summary: "add runners on a machine",
		args: "--machine M --os OS [--backend B] [--persistent|--ephemeral] [--keep-dependencies|--no-keep-dependencies] [--count N] [--yes]",
		flags: func(c *call, fs *flag.FlagSet) {
			fs.String("machine", "", "machine to add the runners on")
			fs.String("os", "", "linux, windows or macos")
			fs.String("backend", "", "isolation backend, for example docker")
			fs.Bool("persistent", false, "a long-lived runner that keeps its tools and caches between jobs")
			fs.Bool("ephemeral", false, "a fresh container for every job (the default)")
			keepDependenciesFlags(fs)
			fs.Int("count", 1, "how many runners")
			fs.String("repo", "", "only this repository, OWNER/NAME (leave out: the runners serve every repository that uses the pool)")
			fs.String("labels", "", "labels, comma separated")
			yesFlag(fs)
		},
		run: func(c *call, _ []string) error { return c.runnerAdd() }}
}

func keepDependenciesFlags(fs *flag.FlagSet) {
	fs.Bool("keep-dependencies", false, "persistent runner: keep downloaded dependencies between jobs (the default)")
	fs.Bool("no-keep-dependencies", false, "persistent runner: remove downloaded dependencies after each job too")
}

// keepDependencies reads the two flags: nil when neither is given.
func (c *call) keepDependencies() (*bool, error) {
	set := map[string]bool{}
	c.fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	switch {
	case set["keep-dependencies"] && set["no-keep-dependencies"]:
		return nil, fail(ExitUsage, "Use --keep-dependencies or --no-keep-dependencies, not both.")
	case set["keep-dependencies"]:
		v := c.fs.Lookup("keep-dependencies").Value.String() == "true"
		return &v, nil
	case set["no-keep-dependencies"]:
		v := c.fs.Lookup("no-keep-dependencies").Value.String() != "true"
		return &v, nil
	}
	return nil, nil
}

func (c *call) findRunner(s *session, name string) (*client.Runner, error) {
	rs, err := c.runners(s, client.ListRunnersParams{})
	if err != nil {
		return nil, err
	}
	for i := range rs {
		if strings.EqualFold(rs[i].Name, name) || rs[i].Id == name {
			return &rs[i], nil
		}
	}
	return nil, fail(ExitNotFound, "No runner %q. Run crsp runner list.", name)
}

// runnerUpdate changes the keep-dependencies setting of a persistent runner
// (runners R5.89).
func (c *call) runnerUpdate(args []string) error {
	if len(args) != 1 {
		return fail(ExitUsage, "Usage: crsp runner update NAME --keep-dependencies|--no-keep-dependencies")
	}
	keep, err := c.keepDependencies()
	if err != nil {
		return err
	}
	if keep == nil {
		return fail(ExitUsage, "Nothing to change. Give --keep-dependencies or --no-keep-dependencies.")
	}
	s, err := c.open()
	if err != nil {
		return err
	}
	run, err := c.findRunner(s, args[0])
	if err != nil {
		return err
	}
	if run.Kind != client.RunnerKindPersistent {
		return fail(ExitUsage, "%s is an ephemeral runner. Each job gets a fresh container, so there is nothing to keep. Nothing changed.", run.Name)
	}
	r, err := s.client.UpdateRunnerSettingsWithResponse(c.env.Context, run.Id, client.RunnerSettingsUpdate{KeepDependencies: keep})
	if err != nil {
		return s.transport(err)
	}
	if err := apiError(r.StatusCode(), r.Body); err != nil {
		return err
	}
	if c.g.json && r.JSON200 != nil {
		return output.JSON(c.env.Stdout, r.JSON200)
	}
	if *keep {
		_, _ = fmt.Fprintf(c.env.Stderr, "%s keeps downloaded dependencies between jobs. Temporary files of each job are always removed.\n", run.Name)
	} else {
		_, _ = fmt.Fprintf(c.env.Stderr, "%s removes downloaded dependencies after each job, with the temporary files.\n", run.Name)
	}
	return nil
}

// runnerClearCache removes a persistent runner's dependency cache now; the
// controller waits for a running job first (runners R5.89).
func (c *call) runnerClearCache(args []string) error {
	if len(args) != 1 {
		return fail(ExitUsage, "Usage: crsp runner clear-cache NAME [--yes]")
	}
	s, err := c.open()
	if err != nil {
		return err
	}
	run, err := c.findRunner(s, args[0])
	if err != nil {
		return err
	}
	if run.Kind != client.RunnerKindPersistent {
		return fail(ExitUsage, "%s is an ephemeral runner and has no dependency cache. Nothing changed.", run.Name)
	}
	items := []string{"dependency cache of runner " + run.Name}
	if run.CurrentJob != nil {
		items = append(items, "waits for running job: "+jobTitle(*run.CurrentJob))
	}
	if err := c.confirm("This removes the downloaded dependencies of "+run.Name+". Its next jobs download them again.", items, c.yesFlag()); err != nil {
		return err
	}
	r, err := s.client.ClearRunnerCacheWithResponse(c.env.Context, run.Id)
	if err != nil {
		return s.transport(err)
	}
	if err := apiError(r.StatusCode(), r.Body); err != nil {
		return err
	}
	if c.g.json && r.JSON202 != nil {
		return output.JSON(c.env.Stdout, r.JSON202)
	}
	if run.CurrentJob != nil {
		_, _ = fmt.Fprintf(c.env.Stderr, "The cache of %s is cleared when its running job ends.\n", run.Name)
	} else {
		_, _ = fmt.Fprintf(c.env.Stderr, "The cache of %s is being cleared.\n", run.Name)
	}
	return nil
}

// keepsText is what a persistent runner keeps between jobs: kept, removed, or
// "-" for an ephemeral runner. An unclean runner says so (runners R5.89, R5.90).
func keepsText(r client.Runner) string {
	text := "-"
	if r.Kind == client.RunnerKindPersistent && r.KeepDependencies != nil {
		text = "removed"
		if *r.KeepDependencies {
			text = "kept"
		}
	}
	if r.UncleanReason != nil && *r.UncleanReason != "" {
		text = "unclean"
	}
	return text
}

func (c *call) runners(s *session, p client.ListRunnersParams) ([]client.Runner, error) {
	r, err := s.client.ListRunnersWithResponse(c.env.Context, &p)
	if err != nil {
		return nil, s.transport(err)
	}
	if err := apiError(r.StatusCode(), r.Body); err != nil {
		return nil, err
	}
	if r.JSON200 == nil {
		return nil, nil
	}
	return r.JSON200.Items, nil
}

func (c *call) runnerAdd() error {
	f := func(n string) string { return c.fs.Lookup(n).Value.String() }
	if f("machine") == "" || f("os") == "" {
		return fail(ExitUsage, "Usage: crsp runner add --machine M --os OS [--repo OWNER/NAME] [--backend B] [--persistent|--ephemeral] [--keep-dependencies|--no-keep-dependencies] [--count N]")
	}
	os := client.OS(f("os"))
	if !os.Valid() {
		return fail(ExitUsage, "--os takes linux, windows or macos.")
	}
	count := mustInt(f("count"))
	if count < 1 || count > 64 {
		return fail(ExitUsage, "--count must be between 1 and 64.")
	}
	s, err := c.open()
	if err != nil {
		return err
	}
	ms, err := c.machines(s)
	if err != nil {
		return err
	}
	var m *client.Machine
	for i := range ms {
		if strings.EqualFold(ms[i].Name, f("machine")) || ms[i].Id == f("machine") {
			m = &ms[i]
		}
	}
	if m == nil {
		return fail(ExitNotFound, "No machine %q. Run crsp machine list.", f("machine"))
	}
	backend := f("backend")
	supports := func(x client.Machine) bool {
		if x.Os != os {
			return false
		}
		if backend == "" {
			return true
		}
		for _, b := range x.Backends {
			if string(b.Name) == backend && b.Present && b.Usable && b.Allowed {
				return true
			}
		}
		return false
	}
	if !supports(*m) {
		var ok []string
		for _, x := range ms {
			if supports(x) {
				ok = append(ok, x.Name)
			}
		}
		want := string(os)
		if backend != "" {
			want += " with backend " + backend
		}
		hint := "No machine supports it."
		if len(ok) > 0 {
			hint = "These do: " + strings.Join(ok, ", ") + "."
		}
		return fail(ExitUsage, "Machine %s does not support %s. %s Nothing changed.", m.Name, want, hint)
	}
	if f("persistent") == "true" && f("ephemeral") == "true" {
		return fail(ExitUsage, "Use --persistent or --ephemeral, not both.")
	}
	keep, err := c.keepDependencies()
	if err != nil {
		return err
	}
	kind := client.RunnerKindEphemeral
	if f("persistent") == "true" {
		kind = client.RunnerKindPersistent
	}
	if keep != nil && kind != client.RunnerKindPersistent {
		return fail(ExitUsage, "--keep-dependencies and --no-keep-dependencies are for persistent runners. Add --persistent, or leave them out. Nothing changed.")
	}
	req := client.PlanRunnersRequest{Os: os, Kind: kind, MachineId: m.Id, Count: count}
	if kind == client.RunnerKindPersistent {
		req.KeepDependencies = keep
	}
	if f("repo") != "" { // without a repository the runners are pool runners
		repo, err := c.findRepo(s, f("repo"))
		if err != nil {
			return err
		}
		req.Scope = &client.RunnerScope{Kind: client.RunnerScopeKindRepository, RepositoryId: &repo.Id}
	}
	if l := splitList(f("labels")); len(l) > 0 {
		req.Labels = &l
	}
	pr, err := s.client.PlanRunnersWithResponse(c.env.Context, req)
	if err != nil {
		return s.transport(err)
	}
	if err := apiError(pr.StatusCode(), pr.Body); err != nil {
		return err
	}
	if pr.JSON201 == nil {
		return fail(ExitFailed, "The controller sent no plan.")
	}
	id := pr.JSON201.Id
	get := func() (*client.Plan, error) {
		r, err := s.client.GetRunnerPlanWithResponse(c.env.Context, id)
		if err != nil {
			return nil, s.transport(err)
		}
		if err := apiError(r.StatusCode(), r.Body); err != nil {
			return nil, err
		}
		return r.JSON200, nil
	}
	apply := func(ids []string) (*client.Plan, error) {
		r, err := s.client.CreateRunnersWithResponse(c.env.Context, id, client.ApplyPlanRequest{ApprovedSteps: ids})
		if err != nil {
			return nil, s.transport(err)
		}
		if err := apiError(r.StatusCode(), r.Body); err != nil {
			return nil, err
		}
		return r.JSON202, nil
	}
	done, err := c.runPlan(pr.JSON201, fmt.Sprintf("Adding %d %s runner(s) on %s:", count, os, m.Name), false, get, apply)
	if err != nil {
		return err
	}
	if c.g.json {
		return output.JSON(c.env.Stdout, done)
	}
	_, _ = fmt.Fprintf(c.env.Stderr, "Runners added on %s.\n", m.Name)
	return nil
}

func (c *call) runnerRemove(args []string) error {
	if len(args) != 1 {
		return fail(ExitUsage, "Usage: crsp runner remove NAME [--yes]")
	}
	s, err := c.open()
	if err != nil {
		return err
	}
	rs, err := c.runners(s, client.ListRunnersParams{})
	if err != nil {
		return err
	}
	var run *client.Runner
	for i := range rs {
		if strings.EqualFold(rs[i].Name, args[0]) || rs[i].Id == args[0] {
			run = &rs[i]
		}
	}
	if run == nil {
		return fail(ExitNotFound, "No runner %q. Run crsp runner list.", args[0])
	}
	items := []string{"runner " + run.Name}
	title := "This removes runner " + run.Name + "."
	if run.CurrentJob != nil {
		items = append(items, "waits for running job: "+jobTitle(*run.CurrentJob))
	}
	if err := c.confirm(title, items, c.yesFlag()); err != nil {
		return err
	}
	// The runner's machine sets the drain timeout; the default is 60 minutes.
	// Past it the command keeps waiting and warns (never cuts the job).
	timeout := time.Hour
	if run.MachineId != "" {
		if ms, err := c.machines(s); err == nil {
			for _, m := range ms {
				if m.Id == run.MachineId && m.Settings.DrainTimeoutMinutes > 0 {
					timeout = time.Duration(m.Settings.DrainTimeoutMinutes) * time.Minute
				}
			}
		}
	}
	yes := true
	d, err := s.client.DeleteRunnerWithResponse(c.env.Context, run.Id, &client.DeleteRunnerParams{Confirm: &yes})
	if err != nil {
		return s.transport(err)
	}
	if err := apiError(d.StatusCode(), d.Body); err != nil {
		return err
	}
	started := c.env.Now()
	var lastWarn time.Time
	if run.CurrentJob != nil {
		_, _ = fmt.Fprintf(c.env.Stderr, "Waiting for 1 job to end on %s.\n", run.Name)
	}
	for {
		g, err := s.client.GetRunnerWithResponse(c.env.Context, run.Id)
		if err != nil {
			return s.transport(err)
		}
		if g.StatusCode() == 404 {
			break
		}
		if err := apiError(g.StatusCode(), g.Body); err != nil {
			return err
		}
		if g.JSON200 != nil && g.JSON200.State == client.RunnerStateFailed {
			return fail(ExitFailed, "Removing runner %s failed. Run crsp doctor.", run.Name)
		}
		if waited := c.env.Now().Sub(started); waited >= timeout && c.env.Now().Sub(lastWarn) >= stillDrainingEvery {
			lastWarn = c.env.Now()
			_, _ = fmt.Fprintf(c.env.Stderr, "Still draining %s: 1 job running, waited %s. Nothing is stopped; press Ctrl-C to stop waiting (the removal goes on).\n", run.Name, waited.Round(time.Minute))
		}
		if err := c.sleep(2 * time.Second); err != nil {
			return err
		}
	}
	if c.g.json {
		return output.JSON(c.env.Stdout, map[string]string{"removed": run.Name})
	}
	_, _ = fmt.Fprintf(c.env.Stderr, "Runner %s removed.\n", run.Name)
	return nil
}

// runnerDeleteOffline is the yes path of the offline runner cleanup
// (github-integration R5.33): it names the runner, asks, then deletes the
// registration at GitHub. The controller refuses an online or busy runner.
func (c *call) runnerDeleteOffline(args []string) error {
	if len(args) != 1 {
		return fail(ExitUsage, "Usage: crsp runner delete-offline NAME [--yes]")
	}
	s, err := c.open()
	if err != nil {
		return err
	}
	rs, err := c.runners(s, client.ListRunnersParams{})
	if err != nil {
		return err
	}
	var run *client.Runner
	for i := range rs {
		if strings.EqualFold(rs[i].Name, args[0]) || rs[i].Id == args[0] {
			run = &rs[i]
		}
	}
	if run == nil {
		return fail(ExitNotFound, "No runner %q. Run crsp runner list.", args[0])
	}
	items := []string{"the registration of offline runner " + run.Name + " on GitHub", "its record in crsp", "its folder, if its machine answers"}
	if err := c.confirm("This deletes offline runner "+run.Name+" without asking its machine.", items, c.yesFlag()); err != nil {
		return err
	}
	yes := true
	d, err := s.client.DeleteRunnerRegistrationWithResponse(c.env.Context, run.Id, &client.DeleteRunnerRegistrationParams{Confirm: &yes})
	if err != nil {
		return s.transport(err)
	}
	if err := apiError(d.StatusCode(), d.Body); err != nil {
		return err
	}
	if c.g.json {
		return output.JSON(c.env.Stdout, map[string]string{"deleted": run.Name})
	}
	_, _ = fmt.Fprintf(c.env.Stderr, "Runner %s deleted.\n", run.Name)
	return nil
}

// runnerEnv shows the merged environment of a runner: machine values, then
// each set in order, then the runner's own (runners R5.75, R5.76). The API
// masks sensitive values; this command prints what it is given.
func (c *call) runnerEnv(args []string) error {
	if len(args) != 1 {
		return fail(ExitUsage, "Usage: crsp runner env NAME")
	}
	s, err := c.open()
	if err != nil {
		return err
	}
	rs, err := c.runners(s, client.ListRunnersParams{})
	if err != nil {
		return err
	}
	var run *client.Runner
	for i := range rs {
		if strings.EqualFold(rs[i].Name, args[0]) || rs[i].Id == args[0] {
			run = &rs[i]
		}
	}
	if run == nil {
		return fail(ExitNotFound, "No runner %q. Run crsp runner list.", args[0])
	}
	r, err := s.client.GetRunnerEnvWithResponse(c.env.Context, run.Id)
	if err != nil {
		return s.transport(err)
	}
	if err := apiError(r.StatusCode(), r.Body); err != nil {
		return err
	}
	if r.JSON200 == nil {
		return nil
	}
	if c.g.json {
		return output.JSON(c.env.Stdout, *r.JSON200)
	}
	if len(r.JSON200.Vars) == 0 {
		_, _ = fmt.Fprintln(c.env.Stderr, "This runner has no environment values from machine, sets or its own list.")
		return nil
	}
	var rows [][]string
	for _, v := range r.JSON200.Vars {
		rows = append(rows, []string{v.Name, v.Value, v.Source})
	}
	output.Table(c.env.Stdout, output.MaxWidth, []string{"NAME", "VALUE", "FROM"}, rows)
	return nil
}
