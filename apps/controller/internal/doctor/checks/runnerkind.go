package checks

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

// addRunnersLink is the page where the owner adds runners.
const addRunnersLink = "/pool/runners/add"

// repoEphemeralCapacity: a repository whose jobs go to ephemeral runners only
// uses the pool, and the pool has no machine that can make an ephemeral runner,
// so its jobs would wait (doctor R5.47, github-integration R5.60). New check.
func repoEphemeralCapacity(ctx context.Context, env doctor.Env) []doctor.Finding {
	repos, out := poolRepos(ctx, env, "repo.ephemeral_capacity")
	if len(out) > 0 {
		return out
	}
	var ephemeral []doctor.Repository
	for _, r := range repos {
		if r.EphemeralOnly {
			ephemeral = append(ephemeral, r)
		}
	}
	if len(ephemeral) == 0 {
		return nil
	}
	if env.Ephemeral == nil {
		return []doctor.Finding{skip("repo.ephemeral_capacity", controllerSubject, "Ephemeral capacity is not read", "The pool did not say whether it can make an ephemeral runner.")}
	}
	has, err := env.Ephemeral.EphemeralCapacity(ctx)
	if err != nil {
		return []doctor.Finding{skipFor("repo.ephemeral_capacity", controllerSubject, "The ephemeral capacity of the pool", err)}
	}
	for _, r := range ephemeral {
		f := doctor.Finding{Check: "repo.ephemeral_capacity", Subject: repoSubject(r)}
		if has {
			f.Severity, f.Title = doctor.OK, "The pool can make an ephemeral runner for "+r.FullName
		} else {
			f.Severity = doctor.Warn
			f.Title = r.FullName + " runs on ephemeral runners only and the pool has none to make"
			f.Detail = "Its jobs would wait. No online machine has room set aside for ephemeral runners."
			f.Fix = doctor.Fix{Text: "Add runners and choose Ephemeral, or set the repository to Any runner if it may use a persistent runner.", Link: addRunnersLink}
		}
		out = append(out, f)
	}
	return out
}

// runnerUnclean: a persistent runner whose clean-up after a job failed takes no
// new job until it is clean (doctor R5.46, runners R5.90). New check.
func runnerUnclean(ctx context.Context, env doctor.Env) []doctor.Finding {
	var rs []doctor.Runner
	var err error
	if l, ok := env.Fleet.(doctor.UncleanLister); ok {
		rs, err = l.UncleanRunners(ctx)
	} else {
		rs, err = env.Fleet.Runners(ctx)
	}
	if err != nil {
		return []doctor.Finding{skipFor("runner.unclean", controllerSubject, "The runner list", err)}
	}
	var out []doctor.Finding
	for _, r := range rs {
		if r.Kind != "persistent" || r.UncleanReason == "" {
			continue
		}
		if r.MachineID != "" && !env.Scope.MatchMachine(r.MachineID, r.MachineName) {
			continue
		}
		out = append(out, doctor.Finding{Check: "runner.unclean", Subject: runnerSubject(r), Severity: doctor.Fail,
			Title:  r.Name + " is not clean and takes no new job",
			Detail: "The clean-up of its last job failed: " + r.UncleanReason + ". It takes no new job until it is clean.",
			Fix:    doctor.Fix{Text: "Open the runner, read why, and use Clear now once the cause is gone. The pool cleans it again before the next job.", Link: "/pool/runners"}})
	}
	if len(out) == 0 && len(rs) > 0 {
		return []doctor.Finding{{Check: "runner.unclean", Subject: controllerSubject, Severity: doctor.OK, Title: "Every persistent runner is clean"}}
	}
	return out
}
