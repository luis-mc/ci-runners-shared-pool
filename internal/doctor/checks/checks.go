// Package checks holds the milestone 1 core checks of the doctor (doctor 7.2):
// machine, runner, GitHub App, repository variables and workflow labels.
//
// Spec 7.1 plans one package per group; the M1 groups are small, so they share
// this package, one file each. Later milestones add their checks the same way
// or in packages of their own through doctor.Registry.Register.
//
// Ported from dev-standards delivery_doctor.py (machine_items, service_rows,
// workflow_items) and runner_health.py; each file names what it ports.
package checks

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
)

// M1 is the milestone of every check here.
const M1 = 1

// Register adds every M1 core check to reg.
func Register(reg *doctor.Registry) {
	for _, c := range []doctor.Check{
		doctor.NewCheck("machine.online", doctor.GroupMachine, M1, false, machineOnline),
		doctor.NewCheck("machine.load.fresh", doctor.GroupMachine, M1, false, machineLoadFresh),
		doctor.NewCheck("machine.load.measured", doctor.GroupMachine, M1, false, machineLoadMeasured),
		doctor.NewCheck("machine.load.budget", doctor.GroupMachine, M1, false, machineLoadBudget),
		doctor.NewCheck("machine.engine", doctor.GroupMachine, M1, false, machineEngine),
		doctor.NewCheck("machine.disk", doctor.GroupMachine, M1, false, machineDisk),
		doctor.NewCheck("machine.guard.disk", doctor.GroupMachine, M1, false, machineDiskGuard),
		doctor.NewCheck("machine.docker.compact", doctor.GroupMachine, M1, false, machineDockerCompact),
		doctor.NewCheck("machine.pool.memory", doctor.GroupMachine, M1, false, machinePoolMemory),
		doctor.NewCheck("machine.runner_leftovers", doctor.GroupMachine, M1, false, machineRunnerLeftovers),
		doctor.NewCheck("cache.cap", doctor.GroupMachine, M1, false, cacheCap),
		doctor.NewCheck("machine.floor.disk", doctor.GroupMachine, M1, false, machineDiskFloor),
		doctor.NewCheck("cache.disk", doctor.GroupMachine, M1, false, cacheDisk),
		doctor.NewCheck("runner.detected", doctor.GroupRunner, M1, false, runnerDetected),
		doctor.NewCheck("runner.service", doctor.GroupRunner, M1, false, runnerService),
		doctor.NewCheck("runner.registered", doctor.GroupRunner, M1, false, runnerRegistered),
		doctor.NewCheck("runner.orphan", doctor.GroupRunner, M1, false, runnerOrphan),
		doctor.NewCheck("runner.hooks", doctor.GroupRunner, M1, true, runnerHooks),
		doctor.NewCheck("runner.hookrun", doctor.GroupRunner, M1, false, runnerHookRun),
		doctor.NewCheck("runner.listener", doctor.GroupRunner, M1, false, runnerListener),
		doctor.NewCheck("runner.toolcache", doctor.GroupRunner, M1, false, runnerToolCache),
		doctor.NewCheck("runner.cachewrite", doctor.GroupRunner, M1, false, runnerCacheWrite),
		doctor.NewCheck("runner.symlink", doctor.GroupRunner, M1, false, runnerSymlink),
		doctor.NewCheck("runner.logon", doctor.GroupRunner, M1, false, runnerLogon),
		doctor.NewCheck("runner.labels.generic", doctor.GroupRunner, M1, false, runnerLabelsGeneric),
		doctor.NewCheck("runner.registrations", doctor.GroupRunner, M1, false, runnerRegistrations),
		doctor.NewCheck("runner.slots", doctor.GroupRunner, M1, false, runnerSlots),
		doctor.NewCheck("machine.reset", doctor.GroupMachine, M1, false, machineReset),
		doctor.NewCheck("runner.tools.same", doctor.GroupRunner, M1, false, runnerToolsSame),
		doctor.NewCheck("github.app.installation", doctor.GroupGitHub, M1, false, appInstallation),
		doctor.NewCheck("github.app.permissions", doctor.GroupGitHub, M1, false, appPermissions),
		doctor.NewCheck("github.webhook", doctor.GroupGitHub, M1, false, githubWebhook),
		doctor.NewCheck("demand.quiet", doctor.GroupRepository, M1, false, demandQuiet),
		doctor.NewCheck("repo.vars", doctor.GroupRepository, M1, false, repoVars),
		doctor.NewCheck("repo.vars.runner", doctor.GroupRepository, M1, false, repoVarsRunner),
		doctor.NewCheck("repo.waiting", doctor.GroupRepository, M1, false, repoWaiting),
		doctor.NewCheck("repo.fallback", doctor.GroupRepository, M1, false, repoFallback),
		doctor.NewCheck("repo.ephemeral_capacity", doctor.GroupRepository, M1, false, repoEphemeralCapacity),
		doctor.NewCheck("runner.unclean", doctor.GroupRunner, M1, false, runnerUnclean),
		doctor.NewCheck("runner.postgres_logins", doctor.GroupRunner, M1, false, runnerPostgresLogins),
		doctor.NewCheck("runner.light_leftover", doctor.GroupRunner, M1, false, runnerLightLeftover),
		doctor.NewCheck("repo.coverage", doctor.GroupRepository, M1, false, repoCoverage),
		doctor.NewCheck("workflow.labels", doctor.GroupWorkflow, M1, false, workflowLabels),
		doctor.NewCheck("workflow.generic", doctor.GroupWorkflow, M1, false, workflowGeneric),
		doctor.NewCheck("workflow.unresolved", doctor.GroupWorkflow, M1, false, workflowUnresolved),
	} {
		reg.Register(c)
	}
	registerWorkflowChecks(reg)
}

// controllerSubject is the subject of findings that concern the whole controller.
var controllerSubject = doctor.Subject{Kind: "controller", ID: "controller", Name: "Controller"}

func machineSubject(m doctor.Machine) doctor.Subject {
	return doctor.Subject{Kind: "machine", ID: m.ID, Name: m.Name}
}

func machineLink(m doctor.Machine) string { return "/machines/" + m.ID }

func skip(check string, s doctor.Subject, title, detail string) doctor.Finding {
	return doctor.Finding{Check: check, Subject: s, Severity: doctor.Skip, Title: title, Detail: detail,
		Fix: doctor.Fix{Text: "Nothing to do now. Run the doctor again once this is reachable."}}
}

// skipFor turns a source error into a skip finding (R5.5). A rate limit names its reset time (R6.2).
func skipFor(check string, s doctor.Subject, what string, err error) doctor.Finding {
	var rl *github.RateLimitError
	switch {
	case errors.As(err, &rl):
		return skip(check, s, "GitHub rate limit reached", fmt.Sprintf("%s was not checked. The limit resets at %s.", what, rl.ResetAt.UTC().Format("2006-01-02 15:04 MST")))
	case errors.Is(err, github.ErrUnavailable), errors.Is(err, doctor.ErrUnreachable), errors.Is(err, context.DeadlineExceeded):
		return skip(check, s, what+" could not be reached", "It did not answer, so it was not checked.")
	case errors.Is(err, github.ErrForbidden):
		return skip(check, s, what+" is not allowed", "GitHub refused the read; see the github.app.permissions finding.")
	}
	return skip(check, s, what+" could not be read", err.Error())
}

func quote(labels []string) string { return "[" + strings.Join(labels, ", ") + "]" }

func osTitle(os string) string {
	switch os {
	case "linux":
		return "Linux"
	case "windows":
		return "Windows"
	case "macos":
		return "macOS"
	}
	return os
}
