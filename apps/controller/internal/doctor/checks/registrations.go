package checks

import (
	"context"
	"fmt"
	"sort"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

// MaxRegistrationsPerMachine is how many GitHub registrations one machine may
// hold before the Doctor says so. A long-lived pool runner has one hidden
// registration for each repository that uses the pool, and each is a service
// and a folder on the machine (runners R5.88). GitHub also limits the runners
// of a repository and of an organization.
const MaxRegistrationsPerMachine = 20

// runnerRegistrations: a machine holds at most MaxRegistrationsPerMachine
// registrations. New check.
func runnerRegistrations(ctx context.Context, env doctor.Env) []doctor.Finding {
	rs, byID, out := persistentRunners(ctx, env, "runner.registrations")
	if byID == nil {
		return out
	}
	count := map[string]int{}
	for _, r := range rs {
		count[r.MachineID]++
	}
	ids := make([]string, 0, len(count))
	for id := range count {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		m := byID[id]
		f := doctor.Finding{Check: "runner.registrations", Subject: machineSubject(m)}
		n := count[id]
		if n > MaxRegistrationsPerMachine {
			f.Severity = doctor.Warn
			f.Title = fmt.Sprintf("%s holds %d runner registrations, more than %d", m.Name, n, MaxRegistrationsPerMachine)
			f.Detail = "Each repository that uses the pool adds one hidden registration to every long-lived runner of this machine. Many registrations slow the machine and can reach GitHub's runner limits."
			f.Fix = doctor.Fix{Text: "Remove long-lived runners from " + m.Name + ", or stop using the pool for repositories that do not need them.", Link: "/runners"}
		} else {
			f.Severity = doctor.OK
			f.Title = fmt.Sprintf("%s holds %d runner registrations (at most %d)", m.Name, n, MaxRegistrationsPerMachine)
		}
		out = append(out, f)
	}
	return out
}
