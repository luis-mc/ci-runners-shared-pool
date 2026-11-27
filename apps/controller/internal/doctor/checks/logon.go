package checks

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

// runnerLogon is R5.45: each Windows runner's service account holds the right
// to log on as a service, and its service runs. Without the right Windows
// refuses the start ("Logon failure: the user has not been granted the
// requested logon type at this computer") and the runner stays offline. The
// agent gives the right back by itself; this check shows what it saw. The
// fix is the console's Start button, which makes the agent give
// the right again and start the service. The check reads only.
func runnerLogon(ctx context.Context, env doctor.Env) []doctor.Finding {
	rs, ms, out := persistentRunners(ctx, env, "runner.logon")
	for _, r := range rs {
		if r.OS != "windows" {
			continue
		}
		m := ms[r.MachineID]
		f := doctor.Finding{Check: "runner.logon", Subject: runnerSubject(r)}
		p := r.Logon
		fix := doctor.Fix{
			Text: "Press Start. The agent gives the account the right to log on as a service again and starts the service on " + m.Name + ".",
			Link: machineLink(m),
		}
		switch {
		case !isOnline(env, m):
			out = append(out, offlineSkip("runner.logon", m))
			continue
		case p == nil:
			f.Severity, f.Title = doctor.Skip, "The logon right of "+r.Name+" was not looked at"
			f.Detail = "The agent on " + m.Name + " did not read the rights of the runner's account."
			f.Fix = doctor.Fix{Text: "Update the agent on " + m.Name + " and run the checks again.", Command: "crsp doctor --check runner.logon"}
		case !p.Known:
			f.Severity, f.Title = doctor.Skip, "The logon right of "+r.Name+" could not be read"
			f.Detail = "The agent on " + m.Name + " could not read the rights of " + p.Account + "."
			f.Fix = doctor.Fix{Text: "Run the check again from an agent with administrator rights on " + m.Name + ".", Command: "crsp doctor --check runner.logon"}
		case !p.Held:
			f.Severity, f.Title = doctor.Fail, "The account "+p.Account+" of "+r.Name+" cannot log on as a service"
			f.Detail = "Service " + p.Service + " cannot start: " + p.Account + " does not hold the right to log on as a service on " + m.Name + ", so the runner stays offline."
			f.Fix = fix
		case !p.Running:
			f.Severity, f.Title = doctor.Fail, "The service "+p.Service+" of "+r.Name+" is not running"
			f.Detail = p.Account + " holds the right to log on as a service, but the service is stopped."
			if p.Problem != "" {
				f.Detail += " Windows said: " + p.Problem
			}
			f.Fix = fix
		default:
			f.Severity, f.Title = doctor.OK, p.Account+" can log on as a service and "+r.Name+" runs"
		}
		out = append(out, f)
	}
	return out
}
