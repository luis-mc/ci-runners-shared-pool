package checks

import (
	"context"
	"fmt"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
	"sort"
	"strings"
	"time"

	listener "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/persistent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

func runnerSubject(r doctor.Runner) doctor.Subject {
	return doctor.Subject{Kind: "runner", ID: r.ID, Name: r.Name}
}

// persistentRunners lists the managed persistent runners in scope, with their machines.
func persistentRunners(ctx context.Context, env doctor.Env, check string) ([]doctor.Runner, map[string]doctor.Machine, []doctor.Finding) {
	ms, errs := scopedMachines(ctx, env, check)
	if errs != nil {
		return nil, nil, errs
	}
	byID := map[string]doctor.Machine{}
	for _, m := range ms {
		byID[m.ID] = m
	}
	rs, err := env.Fleet.Runners(ctx)
	if err != nil {
		return nil, nil, []doctor.Finding{skipFor(check, controllerSubject, "The runner list", err)}
	}
	var out []doctor.Runner
	for _, r := range rs {
		if _, ok := byID[r.MachineID]; ok && r.Kind == "persistent" {
			out = append(out, r)
		}
	}
	return out, byID, nil
}

// services reads the runner services of a machine once per run.
func services(ctx context.Context, env doctor.Env, m doctor.Machine) ([]doctor.OSService, error) {
	v, err := env.Memo("services:"+m.ID, func() (any, error) { return env.Fleet.Services(ctx, m.ID) })
	if err != nil {
		return nil, err
	}
	return v.([]doctor.OSService), nil
}

func findService(svcs []doctor.OSService, r doctor.Runner) (doctor.OSService, bool) {
	for _, s := range svcs {
		if (r.ServiceName != "" && s.Name == r.ServiceName) || s.RunnerName == r.Name {
			return s, true
		}
	}
	return doctor.OSService{}, false
}

// runnerDetected: a runner service the product does not manage is info (R5.11).
// Ported from delivery_doctor.py service_rows; tests TestRunnerDetected.
func runnerDetected(ctx context.Context, env doctor.Env) []doctor.Finding {
	ms, out := scopedMachines(ctx, env, "runner.detected")
	if out != nil {
		return out
	}
	managed, err := env.Fleet.Runners(ctx)
	if err != nil {
		return []doctor.Finding{skipFor("runner.detected", controllerSubject, "The runner list", err)}
	}
	for _, m := range ms {
		if !isOnline(env, m) {
			out = append(out, offlineSkip("runner.detected", m))
			continue
		}
		svcs, err := services(ctx, env, m)
		if err != nil {
			out = append(out, skipFor("runner.detected", machineSubject(m), "The service list of "+m.Name, err))
			continue
		}
		found := 0
		for _, s := range svcs {
			known := false
			for _, r := range managed {
				if r.MachineID == m.ID && ((r.ServiceName != "" && r.ServiceName == s.Name) || r.Name == s.RunnerName) {
					known = true
				}
			}
			if known {
				continue
			}
			found++
			out = append(out, doctor.Finding{
				Check: "runner.detected", Severity: doctor.Info,
				Subject: doctor.Subject{Kind: "runner_service", ID: m.ID + "/" + s.Name, Name: s.RunnerName + " on " + m.Name},
				Title:   "A runner on " + m.Name + " is not managed",
				Detail:  fmt.Sprintf("Service %s runs a runner (%s) that the product did not make.", s.Name, s.Scope),
				Fix:     doctor.Fix{Text: "Replace it with a managed runner from the Runners screen, or leave it as it is.", Link: "/runners"},
			})
		}
		if found == 0 {
			out = append(out, doctor.Finding{Check: "runner.detected", Severity: doctor.OK, Subject: machineSubject(m),
				Title: "Every runner service on " + m.Name + " is managed"})
		}
	}
	return out
}

// runnerService: the service is missing, stopped, or not set to start with the
// machine. Ported from delivery_doctor.py windows_runner_items (runner.service).
func runnerService(ctx context.Context, env doctor.Env) []doctor.Finding {
	rs, ms, out := persistentRunners(ctx, env, "runner.service")
	for _, r := range rs {
		m := ms[r.MachineID]
		s := runnerSubject(r)
		if !isOnline(env, m) {
			out = append(out, offlineSkip("runner.service", m))
			continue
		}
		svcs, err := services(ctx, env, m)
		if err != nil {
			out = append(out, skipFor("runner.service", s, "The service list of "+m.Name, err))
			continue
		}
		f := doctor.Finding{Check: "runner.service", Subject: s}
		svc, ok := findService(svcs, r)
		switch {
		case !ok:
			f.Severity, f.Title = doctor.Fail, "The service of "+r.Name+" is missing"
			f.Detail = "No runner service with this name is in the service list of " + m.Name + "."
			f.Fix = doctor.Fix{Text: "Add the runner again from the Runners screen; it installs the service.", Link: "/runners"}
		case !svc.Running:
			f.Severity, f.Title = doctor.Fail, "The service of "+r.Name+" is stopped"
			f.Detail = svc.Name + " is not running, so the runner takes no jobs."
			f.Fix = doctor.Fix{Text: "Start the service on " + m.Name + " and set it to start with the machine.", Link: "/runners"}
		case svc.AutomaticKnown && !svc.Automatic:
			f.Severity, f.Title = doctor.Fail, "The service of "+r.Name+" does not start with the machine"
			f.Detail = svc.Name + " runs now but is not set to start automatically."
			f.Fix = doctor.Fix{Text: "Set the service to start automatically on " + m.Name + ".", Link: "/runners"}
		default:
			f.Severity, f.Title = doctor.OK, "The service of "+r.Name+" is running"
		}
		out = append(out, f)
	}
	return out
}

// ghRunners lists the runners of a scope once per run.
func ghRunners(ctx context.Context, env doctor.Env, inst int64, sc github.RunnerScope) ([]github.Runner, error) {
	key := fmt.Sprintf("ghrunners:%d:%s/%s/%s", inst, sc.Organization, sc.Owner, sc.Repo)
	v, err := env.Memo(key, func() (any, error) { return env.GitHub.ListRunners(ctx, inst, sc) })
	if err != nil {
		return nil, err
	}
	return v.([]github.Runner), nil
}

// runnerRegistered: a managed runner GitHub does not list, or lists as
// offline. Ported from runner_plan.py runners and runner_health.py
// list_online_runners; tests TestRunnerRegistered.
func runnerRegistered(ctx context.Context, env doctor.Env) []doctor.Finding {
	rs, _, out := persistentRunners(ctx, env, "runner.registered")
	if len(rs) == 0 {
		return out
	}
	if env.GitHub == nil {
		return append(out, skip("runner.registered", controllerSubject, "GitHub is not connected", "Runner registration cannot be checked until the GitHub App is set up."))
	}
	for _, r := range rs {
		s := runnerSubject(r)
		list, err := ghRunners(ctx, env, r.InstallationID, r.Scope)
		if err != nil {
			out = append(out, skipFor("runner.registered", s, "The runner list of GitHub", err))
			continue
		}
		var got *github.Runner
		for i := range list {
			if (r.GitHubRunnerID != 0 && list[i].ID == r.GitHubRunnerID) || list[i].Name == r.Name {
				got = &list[i]
				break
			}
		}
		f := doctor.Finding{Check: "runner.registered", Subject: s}
		switch {
		case got == nil:
			f.Severity, f.Title = doctor.Fail, r.Name+" is not registered on GitHub"
			f.Detail = "GitHub does not list a runner with this name, so no job reaches it."
			f.Fix = doctor.Fix{Text: "Restart the runner service; if it stays missing, remove the record here (Remove) and add the runner again from the Runners screen.", Link: "/runners"}
			// A record GitHub does not list: a half-made add (R5.50). The button asks first.
			f.Action = &doctor.Action{ID: doctor.ActionRemoveRunnerRecord, Label: "Remove", Args: &doctor.ActionArgs{RunnerID: r.ID, Name: r.Name}}
		case got.Status != "online":
			f.Severity, f.Title = doctor.Fail, r.Name+" is offline on GitHub"
			f.Detail = "GitHub lists the runner but it is not connected."
			f.Fix = doctor.Fix{Text: "Restart the runner service on " + r.MachineName + "; if it stays offline, add the runner again.", Link: "/runners"}
		default:
			f.Severity, f.Title = doctor.OK, r.Name+" is registered and online"
		}
		out = append(out, f)
	}
	return out
}

// runnerOrphan: a GitHub runner with the pool's name prefix that no machine has.
func runnerOrphan(ctx context.Context, env doctor.Env) []doctor.Finding {
	if env.GitHub == nil {
		return nil
	}
	managed, err := env.Fleet.Runners(ctx)
	if err != nil {
		return []doctor.Finding{skipFor("runner.orphan", controllerSubject, "The runner list", err)}
	}
	repos, err := env.Fleet.Repositories(ctx)
	if err != nil {
		return []doctor.Finding{skipFor("runner.orphan", controllerSubject, "The repository list", err)}
	}
	type scope struct {
		inst int64
		sc   github.RunnerScope
		name string
	}
	seen := map[string]scope{}
	for _, r := range managed {
		n := r.Scope.Organization
		if n == "" {
			n = r.Scope.Owner + "/" + r.Scope.Repo
		}
		seen[fmt.Sprintf("%d|%s", r.InstallationID, n)] = scope{r.InstallationID, r.Scope, n}
	}
	for _, r := range repos {
		if !env.InScopeRepository(r) {
			continue
		}
		seen[fmt.Sprintf("%d|%s", r.InstallationID, r.FullName)] = scope{r.InstallationID, github.RunnerScope{Owner: r.Owner, Repo: r.Name}, r.FullName}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []doctor.Finding
	for _, k := range keys {
		sc := seen[k]
		list, err := ghRunners(ctx, env, sc.inst, sc.sc)
		if err != nil {
			out = append(out, skipFor("runner.orphan", doctor.Subject{Kind: "scope", ID: k, Name: sc.name}, "The runner list of "+sc.name, err))
			continue
		}
		orphans := 0
		for _, gr := range list {
			if !strings.HasPrefix(gr.Name, env.Prefix()) {
				continue
			}
			has := false
			for _, r := range managed {
				if r.Name == gr.Name || (r.GitHubRunnerID != 0 && r.GitHubRunnerID == gr.ID) {
					has = true
					break
				}
			}
			if has {
				continue
			}
			orphans++
			out = append(out, doctor.Finding{
				Check: "runner.orphan", Severity: doctor.Warn,
				Subject: doctor.Subject{Kind: "github_runner", ID: fmt.Sprint(gr.ID), Name: gr.Name},
				Title:   gr.Name + " on GitHub belongs to no machine",
				Detail:  "It has the pool's name prefix in " + sc.name + " but no machine runs it.",
				Fix:     doctor.Fix{Text: "Press Remove. The product asks first and removes only this registration from GitHub.", Link: "/runners"},
				Action: &doctor.Action{ID: doctor.ActionRemoveGitHubRunner, Label: "Remove", Args: &doctor.ActionArgs{
					GitHubRunnerID: gr.ID, Name: gr.Name, InstallationID: sc.inst, Owner: sc.sc.Owner, Repo: sc.sc.Repo, Organization: sc.sc.Organization}},
			})
		}
		if orphans == 0 {
			out = append(out, doctor.Finding{Check: "runner.orphan", Severity: doctor.OK,
				Subject: doctor.Subject{Kind: "scope", ID: k, Name: sc.name}, Title: "No stray runners in " + sc.name})
		}
	}
	return out
}

// runnerHooks: job hooks missing or unusable. The folder of a runner is locked
// to the runner's account, so without administrator rights the check is a
// skip with needs_admin and the fix says how to run it (R5.13). The runner is
// still found and the service checks still run. Ported from delivery_doctor.py
// _locked_runner_items; tests TestRunnerHooksNeedAdmin.
func runnerHooks(ctx context.Context, env doctor.Env) []doctor.Finding {
	rs, ms, out := persistentRunners(ctx, env, "runner.hooks")
	for _, r := range rs {
		m := ms[r.MachineID]
		s := runnerSubject(r)
		if !isOnline(env, m) {
			out = append(out, offlineSkip("runner.hooks", m))
			continue
		}
		if !r.FolderReadable {
			out = append(out, doctor.Finding{
				Check: "runner.hooks", Subject: s, Severity: doctor.Skip, NeedsAdmin: true,
				Title:  "The hooks of " + r.Name + " were not checked",
				Detail: "The runner folder is locked to the runner's account, so the agent cannot read it.",
				Fix: doctor.Fix{
					Text:    "Run the checks that need administrator rights on " + m.Name + " from an administrator shell.",
					Command: "crsp doctor --local --check runner.hooks",
				},
			})
			continue
		}
		f := doctor.Finding{Check: "runner.hooks", Subject: s}
		if len(r.HookProblems) > 0 {
			f.Severity, f.Title = doctor.Fail, "The job hooks of "+r.Name+" are not usable"
			f.Detail = strings.Join(r.HookProblems, "; ")
			f.Fix = doctor.Fix{Text: "Set the hooks again from the Runners screen.", Link: "/runners"}
		} else {
			f.Severity, f.Title = doctor.OK, "The job hooks of "+r.Name+" are set"
		}
		out = append(out, f)
	}
	return out
}

// hookProbeOK is what a working hook prints in probe mode (internal/hooks ProbeOK).
// The check keeps its own copy so the doctor does not import the hook runtime.
const hookProbeOK = "crsp hook ok"

// hookNonInteractive is the word a hook prints in probe mode when it has turned
// prompts off (internal/hooks NonInteractiveOK).
const hookNonInteractive = "non-interactive"

// runnerHookRun reports what happened when the agent ran each runner's real
// job-started hook as the service account (R5.29). A hook needs only the shell
// the runner already uses; it is not allowed to depend on anything else. A
// hook that cannot start, exits non-zero, prints a ::warning annotation, or
// does not reach crsp is a fail: its jobs would run without the queue and the
// load gate.
func runnerHookRun(ctx context.Context, env doctor.Env) []doctor.Finding {
	rs, ms, out := persistentRunners(ctx, env, "runner.hookrun")
	for _, r := range rs {
		m := ms[r.MachineID]
		f := doctor.Finding{Check: "runner.hookrun", Subject: runnerSubject(r)}
		switch p := r.HookProbe; {
		case !isOnline(env, m):
			out = append(out, offlineSkip("runner.hookrun", m))
			continue
		case p == nil:
			f.Severity, f.Title = doctor.Skip, "The job hook of "+r.Name+" was not run"
			f.Detail = "The agent on " + m.Name + " did not run the hook as the runner's service account."
			f.Fix = doctor.Fix{Text: "Update the agent on " + m.Name + " and run the checks again.", Command: "crsp doctor --check runner.hookrun"}
		case p.Err != "":
			f.Severity, f.Title = doctor.Fail, "The job hook of "+r.Name+" cannot start"
			f.Detail = p.Err
			f.Fix = doctor.Fix{Text: "Set the hooks again from the Runners screen.", Link: "/runners"}
		case p.ExitCode != 0 || strings.Contains(p.Output, "::warning") || !strings.Contains(p.Output, hookProbeOK):
			f.Severity, f.Title = doctor.Fail, "The job hook of "+r.Name+" does not reach crsp"
			f.Detail = fmt.Sprintf("Exit %d. %s", p.ExitCode, strings.TrimSpace(p.Output))
			f.Fix = doctor.Fix{Text: "Install crsp for all users on " + m.Name + " again, then run the check again.", Command: "crsp doctor --check runner.hookrun"}
		case !strings.Contains(p.Output, hookNonInteractive):
			// The hook runs, but it does not say it turns prompts off: a git or
			// credential helper it starts could wait for a login (R5.30).
			f.Severity, f.Title = doctor.Warn, "The job hook of "+r.Name+" does not turn prompts off"
			f.Detail = "The hook reaches crsp, but crsp did not report that it sets GIT_TERMINAL_PROMPT=0 and GCM_INTERACTIVE=never."
			f.Fix = doctor.Fix{Text: "Update crsp on " + m.Name + " and set the hooks again.", Command: "crsp doctor --check runner.hookrun"}
		default:
			f.Severity, f.Title = doctor.OK, "The job hook of "+r.Name+" reaches crsp"
		}
		out = append(out, f)
	}
	return out
}

// runnerListener is runners R5.46 as the doctor sees it: a registered runner
// has exactly one listener, and a listener never runs while its service is
// stopped. Two listeners, or an orphan beside a stopped service, make GitHub
// refuse the next one ("a session for this runner already exists").
func runnerListener(ctx context.Context, env doctor.Env) []doctor.Finding {
	rs, ms, out := persistentRunners(ctx, env, "runner.listener")
	for _, r := range rs {
		m := ms[r.MachineID]
		f := doctor.Finding{Check: "runner.listener", Subject: runnerSubject(r)}
		n := len(r.ListenerPIDs)
		switch {
		case !isOnline(env, m):
			out = append(out, offlineSkip("runner.listener", m))
			continue
		case !r.ListenerKnown:
			f.Severity, f.Title = doctor.Skip, "The listener of "+r.Name+" was not looked at"
			f.Detail = "The agent on " + m.Name + " did not list the runner's listener processes."
			f.Fix = doctor.Fix{Text: "Update the agent on " + m.Name + " and run the checks again.", Command: "crsp doctor --check runner.listener"}
		case n > 1:
			f.Severity, f.Title = doctor.Fail, r.Name+" has more than one listener"
			f.Detail = fmt.Sprintf("%d listener processes run from the runner folder (%v). GitHub keeps one session per runner and refuses the others.", n, r.ListenerPIDs)
			f.Fix = doctor.Fix{Text: "Stop the runner from the Runners screen. crsp waits for the listener itself to end, then starts one.", Link: "/runners"}
		case n == 1 && !r.ServiceRunning:
			f.Severity, f.Title = doctor.Fail, r.Name+" has a listener but its service is stopped"
			f.Detail = fmt.Sprintf("The listener (process %d) outlived its service. Starting the service would start a second listener, which GitHub refuses.", r.ListenerPIDs[0])
			f.Fix = doctor.Fix{Text: "End that listener process, or restart the runner from the Runners screen when it is idle.", Link: "/runners"}
		case r.SessionRefused && listener.InSessionWindow(r.RestartedAt, env.Now, env.SessionWait):
			until := r.RestartedAt.Add(sessionWait(env)).UTC().Format("15:04:05Z")
			f.Severity, f.Title = doctor.OK, "The listener of "+r.Name+" waits for GitHub to drop the old session"
			f.Detail = "After the restart GitHub still holds the old session, which is expected until " + until + ". No second listener is started."
		case r.SessionRefused:
			f.Severity, f.Title = doctor.Warn, "GitHub still refuses the session of "+r.Name
			f.Detail = "The listener logs \"a session for this runner already exists\" and the wait after the last restart (" + sessionWait(env).String() + ") is over."
			f.Fix = doctor.Fix{Text: "Look for a second listener or another machine that uses this runner name; if there is none, remove the runner in GitHub and add it again from the Runners screen.", Link: "/runners"}
		case n == 0 && r.ServiceRunning:
			f.Severity, f.Title = doctor.Warn, "The service of "+r.Name+" runs without a listener"
			f.Detail = "The service wrapper is up, but no Runner.Listener process runs from the runner folder, so the runner takes no job."
			f.Fix = doctor.Fix{Text: "Restart the runner from the Runners screen.", Link: "/runners"}
		default:
			f.Severity, f.Title = doctor.OK, "The listener of "+r.Name+" is one process under its service"
		}
		out = append(out, f)
	}
	return out
}

// sessionWait is the stale-session window of the run.
func sessionWait(env doctor.Env) time.Duration {
	if env.SessionWait > 0 {
		return env.SessionWait
	}
	return listener.DefaultSessionWindow
}

var genericLabels = map[string]bool{
	"self-hosted": true, "linux": true, "windows": true, "macos": true,
	"x64": true, "x86": true, "arm": true, "arm64": true,
	"docker": true, "kvm": true, "gpu": true,
}

// IsGenericLabel says whether a runner label is one the product allows:
// self-hosted, an OS, an architecture, docker, kvm, gpu, or the pool name (R5.19).
func IsGenericLabel(label, poolName string) bool {
	l := strings.ToLower(label)
	return genericLabels[l] || (poolName != "" && l == strings.ToLower(poolName))
}

// runnerLabelsGeneric: a runner label that is not generic (dev-standards learning 7).
func runnerLabelsGeneric(ctx context.Context, env doctor.Env) []doctor.Finding {
	rs, _, out := persistentRunners(ctx, env, "runner.labels.generic")
	for _, r := range rs {
		var bad []string
		for _, l := range r.Labels {
			if !IsGenericLabel(l, env.PoolName) {
				bad = append(bad, l)
			}
		}
		f := doctor.Finding{Check: "runner.labels.generic", Subject: runnerSubject(r)}
		if len(bad) == 0 {
			f.Severity, f.Title = doctor.OK, r.Name+" carries only generic labels"
		} else {
			f.Severity, f.Title = doctor.Warn, r.Name+" carries labels that are not generic"
			f.Detail = "Not generic: " + quote(bad) + ". Labels name a capability, never a project."
			f.Fix = doctor.Fix{Text: "Remove the label and target the runner through the repository variable instead.", Link: "/runners"}
		}
		out = append(out, f)
	}
	return out
}

// toolCacheFixCommands are the two commands that put a tool cache right after
// an account switch (runners R5.58).
func toolCacheFixCommands(dir string) string {
	return "takeown /F " + dir + " /R /D Y && icacls " + dir + " /reset /T /C"
}

// runnerToolCache is R5.35: the runner's own account can read its tool cache.
// A runner moved from one account to another can keep files with the old owner
// and explicit entries, and then every job that runs a tool from the cache
// fails with "Access is denied". The agent opens a file there as the runner
// account; when it cannot, it reports the owner and inheritance flags.
func runnerToolCache(ctx context.Context, env doctor.Env) []doctor.Finding {
	rs, ms, out := persistentRunners(ctx, env, "runner.toolcache")
	for _, r := range rs {
		m := ms[r.MachineID]
		f := doctor.Finding{Check: "runner.toolcache", Subject: runnerSubject(r)}
		p := r.ToolCache
		switch {
		case !isOnline(env, m):
			out = append(out, offlineSkip("runner.toolcache", m))
			continue
		case p == nil:
			f.Severity, f.Title = doctor.Skip, "The tool cache of "+r.Name+" was not looked at"
			f.Detail = "The agent on " + m.Name + " did not try to read the tool cache as the runner's account."
			f.Fix = doctor.Fix{Text: "Update the agent on " + m.Name + " and run the checks again.", Command: "crsp doctor --check runner.toolcache"}
		case p.Err != "" || len(p.WrongOwner) > 0 || len(p.NotInherited) > 0:
			f.Severity, f.Title = doctor.Fail, r.Name+" cannot read its tool cache"
			var parts []string
			if p.Err != "" {
				parts = append(parts, fmt.Sprintf("Opening %s failed: %s.", p.File, p.Err))
			}
			if len(p.WrongOwner) > 0 {
				parts = append(parts, fmt.Sprintf("%d file(s) have another owner, for example %s.", len(p.WrongOwner), p.WrongOwner[0]))
			}
			if len(p.NotInherited) > 0 {
				parts = append(parts, fmt.Sprintf("%d file(s) do not inherit access, for example %s.", len(p.NotInherited), p.NotInherited[0]))
			}
			f.Detail = strings.Join(parts, " ")
			f.Fix = doctor.Fix{
				Text:    "Take ownership of the runner folder and reset every file to inherited access, from an administrator shell on " + m.Name + ".",
				Command: toolCacheFixCommands(p.Dir),
			}
		default:
			f.Severity, f.Title = doctor.OK, r.Name+" can read its tool cache"
		}
		out = append(out, f)
	}
	return out
}

// runnerSymlink is R5.42: a Windows runner whose setting asks for the symbolic
// link right holds it (runners R5.14). Without it cargo-xwin and Rust tooling
// fail on `os error 1314`. Developer Mode also lets the account link, so it
// makes the finding `ok` with a note. The check reads only: it never grants
// the right and never turns Developer Mode on.
func runnerSymlink(ctx context.Context, env doctor.Env) []doctor.Finding {
	rs, ms, out := persistentRunners(ctx, env, "runner.symlink")
	for _, r := range rs {
		if r.OS != "windows" {
			continue
		}
		m := ms[r.MachineID]
		f := doctor.Finding{Check: "runner.symlink", Subject: runnerSubject(r)}
		p := r.Symlink
		switch {
		case !isOnline(env, m):
			out = append(out, offlineSkip("runner.symlink", m))
			continue
		case p == nil && !r.SymlinkWanted:
			continue // nothing asked, nothing read: no finding
		case p == nil:
			f.Severity, f.Title = doctor.Skip, "The symbolic link right of "+r.Name+" was not looked at"
			f.Detail = "The agent on " + m.Name + " did not read the rights of the runner's account."
			f.Fix = doctor.Fix{Text: "Update the agent on " + m.Name + " and run the checks again.", Command: "crsp doctor --check runner.symlink"}
		case p.Err != "":
			f.Severity, f.Title = doctor.Skip, "The symbolic link right of "+r.Name+" could not be read"
			f.Detail = "The agent said: " + p.Err + "."
			f.Fix = doctor.Fix{Text: "Run the check again from an agent with administrator rights on " + m.Name + ".", Command: "crsp doctor --check runner.symlink"}
		case r.SymlinkWanted && p.Held:
			f.Severity, f.Title = doctor.OK, r.Name+" can create symbolic links"
		case r.SymlinkWanted && p.DeveloperMode:
			f.Severity, f.Title = doctor.OK, r.Name+" can create symbolic links through Developer Mode"
			f.Detail = "The account does not hold the right itself; Developer Mode on " + m.Name + " lets it link. Turning Developer Mode off would stop it."
		case r.SymlinkWanted:
			f.Severity, f.Title = doctor.Fail, r.Name+" cannot create symbolic links"
			f.Detail = "The setting asks for the symbolic link right, but the account of " + r.Name + " does not hold it and Developer Mode is off on " + m.Name + ". Jobs that link files fail with \"A required privilege is not held by the client\"."
			f.Fix = doctor.Fix{Text: "Add the runner again from the Runners screen; it gives the right to the runner's account and to no other account.", Link: "/runners"}
		case p.Held:
			f.Severity, f.Title = doctor.Warn, r.Name+" holds a symbolic link right that its setting does not ask for"
			f.Detail = "The account of " + r.Name + " holds the right while the machine and label settings do not ask for it."
			f.Fix = doctor.Fix{Text: "Add the runner again from the Runners screen; it takes the right away.", Link: "/runners"}
		default:
			f.Severity, f.Title = doctor.OK, r.Name+" holds no symbolic link right, as its setting says"
		}
		out = append(out, f)
	}
	return out
}

// runnerCacheWrite is caches R5.57: a runner can write the shared tool folders
// of its repository (cargo-target, cargo-home, rustup-home, xwin, ...). A second
// runner on a machine met `Permission denied` on folders the first runner's user
// had made. The agent tries a file in each folder as the runner's own user; the
// fix is the group and ACL commands crsp runs when it adds a runner, shown as
// text. The check reads only (R5.7).
func runnerCacheWrite(ctx context.Context, env doctor.Env) []doctor.Finding {
	rs, ms, out := persistentRunners(ctx, env, "runner.cachewrite")
	for _, r := range rs {
		m := ms[r.MachineID]
		f := doctor.Finding{Check: "runner.cachewrite", Subject: runnerSubject(r)}
		p := r.CacheWrite
		switch {
		case !isOnline(env, m):
			out = append(out, offlineSkip("runner.cachewrite", m))
			continue
		case p == nil:
			// A runner with its own folders only has nothing shared to write.
			f.Severity, f.Title = doctor.Skip, "The shared tool folders of "+r.Name+" were not looked at"
			f.Detail = "The agent on " + m.Name + " did not try to write them as the runner's user, or the runner keeps its own folders."
			f.Fix = doctor.Fix{Text: "Update the agent on " + m.Name + " and run the checks again.", Command: "crsp doctor --check runner.cachewrite"}
		default:
			var bad []string
			for _, d := range p.Dirs {
				if d.Err != "" {
					bad = append(bad, fmt.Sprintf("%s (%s)", d.Dir, d.Err))
				}
			}
			if len(bad) == 0 {
				f.Severity, f.Title = doctor.OK, r.Name+" can write its shared tool folders"
				break
			}
			f.Severity, f.Title = doctor.Fail, r.Name+" cannot write its shared tool folders"
			f.Detail = "Permission denied in: " + strings.Join(bad, "; ") + ". Another runner's user made them, so this runner's user has no access."
			f.Fix = doctor.Fix{
				Text:    "Give the group " + p.Group + " read and write access to " + p.Root + ", with default access for new files, from an administrator shell on " + m.Name + ". Adding a runner does this itself.",
				Command: runnercache.FixCommands(r.OS, runnercache.SharedTools{Root: p.Root, Group: p.Group}, r.Name),
			}
		}
		out = append(out, f)
	}
	return out
}
