package checks

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

// runnerLightLeftover: a light runner an older version made could not be
// removed. The reconcile removes each one (runners R5.57, decision D110) and
// tries a failed one again, at most 5 times; this shows the reason. Nothing
// is shown while no removal failed. New check.
func runnerLightLeftover(ctx context.Context, env doctor.Env) []doctor.Finding {
	rs, err := env.Fleet.Runners(ctx)
	if err != nil {
		return []doctor.Finding{skipFor("runner.light_leftover", controllerSubject, "The runner list", err)}
	}
	var out []doctor.Finding
	for _, r := range rs {
		if !r.LegacyLight || r.LightRemovalProblem == "" {
			continue
		}
		if r.MachineID != "" && !env.Scope.MatchMachine(r.MachineID, r.MachineName) {
			continue
		}
		out = append(out, doctor.Finding{Check: "runner.light_leftover", Subject: runnerSubject(r), Severity: doctor.Warn,
			Title:  "The old light runner " + r.Name + " could not be removed",
			Detail: "Light runners are gone: jobs run on the pool runners. The removal failed: " + r.LightRemovalProblem + ". The reconcile that runs every 10 minutes tries again, up to 5 times.",
			Fix:    doctor.Fix{Text: "Fix the cause named above. If it is still here after 5 tries, remove the runner by hand.", Link: "/runners"}})
	}
	return out
}
