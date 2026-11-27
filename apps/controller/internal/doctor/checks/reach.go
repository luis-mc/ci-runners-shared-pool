package checks

import (
	"context"
	"fmt"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

// security.reach (R5.38): one info line per served repository that links to
// the section "What a job can reach" (runners R5.64), and a warning when a host
// service listens on the loopback address of a machine that runs container
// jobs. The check reads; it never closes a port.

// ReachLink is where the "What a job can reach" section is read.
const ReachLink = "/docs/security#what-a-job-can-reach"

// LoopbackListener is a host service bound to the loopback address.
type LoopbackListener struct {
	Machine  doctor.Machine
	Port     int
	Process  string
	RunsJobs bool // the machine runs container jobs
}

// ReachSource gives the loopback listeners of the machines, read by each agent.
// Windows, Linux and macOS agents each fill it with their own socket table.
type ReachSource interface {
	Loopback(ctx context.Context) ([]LoopbackListener, error)
}

// RegisterReach adds security.reach. A nil src still gives the link lines.
func RegisterReach(reg *doctor.Registry, src ReachSource) {
	reg.Register(doctor.NewCheck("security.reach", doctor.GroupMachine, M2, false, func(ctx context.Context, env doctor.Env) []doctor.Finding {
		return securityReach(ctx, env, src)
	}))
}

func securityReach(ctx context.Context, env doctor.Env, src ReachSource) []doctor.Finding {
	var out []doctor.Finding
	repos, err := env.Fleet.Repositories(ctx)
	if err != nil {
		return []doctor.Finding{skipFor("security.reach", controllerSubject, "The repositories", err)}
	}
	for _, r := range repos {
		if !env.InScopeRepository(r) {
			continue
		}
		out = append(out, doctor.Finding{Check: "security.reach", Subject: doctor.Subject{Kind: "repository", ID: fmt.Sprint(r.ID), Name: r.FullName},
			Severity: doctor.Info, Title: "What a job of " + r.FullName + " can reach",
			Detail: "Read what a job can reach on the machine before you trust a repository with the pool.",
			Fix:    doctor.Fix{Text: "Read the section What a job can reach.", Link: ReachLink}})
	}
	if src == nil {
		return out
	}
	ls, err := src.Loopback(ctx)
	if err != nil {
		return append(out, skipFor("security.reach", controllerSubject, "The listening ports", err))
	}
	for _, l := range ls {
		if !l.RunsJobs || !env.InScopeMachine(l.Machine) {
			continue
		}
		out = append(out, doctor.Finding{Check: "security.reach", Subject: machineSubject(l.Machine), Severity: doctor.Warn,
			Title:  fmt.Sprintf("A service on %s listens on loopback port %d", l.Machine.Name, l.Port),
			Detail: "A container job can reach host loopback services (" + l.Process + "). Jobs may read or call it.",
			Fix:    doctor.Fix{Text: "Stop the service, bind it to a private socket, or keep container jobs off this machine. See What a job can reach.", Link: ReachLink}})
	}
	return out
}
