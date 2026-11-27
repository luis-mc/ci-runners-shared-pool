package checks

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

// machine.engine: which container engine a machine uses and whether it
// answers (runners R5.47, machines R5.18). On a Windows PC that is Docker in
// WSL2 reached through the Windows docker command, `wsl docker`, or Docker
// Desktop. A machine without an engine is a warning, not a failure: it only
// cannot run container runners (container.engine fails for a machine that has
// a container pool). The check reads; it never starts or changes an engine.

// engineName is how a console and the doctor name an engine kind.
func engineName(kind string) string {
	switch kind {
	case "docker-wsl2-windows-cli":
		return "Docker in WSL2, through the Windows docker command"
	case "docker-wsl2-wsl":
		return "Docker in WSL2, through `wsl docker`"
	case "docker-desktop":
		return "Docker Desktop"
	case "docker-native":
		return "Docker"
	}
	return "no engine"
}

func machineEngine(ctx context.Context, env doctor.Env) []doctor.Finding {
	if env.Engines == nil {
		return []doctor.Finding{skip("machine.engine", controllerSubject, "The container engine is not reported", "No agent reports its container engine, so it was not checked.")}
	}
	sts, err := env.Engines.Engines(ctx)
	if err != nil {
		return []doctor.Finding{skipFor("machine.engine", controllerSubject, "The container engines", err)}
	}
	var out []doctor.Finding
	for _, s := range sts {
		if !env.InScopeMachine(s.Machine) {
			continue
		}
		f := doctor.Finding{Check: "machine.engine", Subject: machineSubject(s.Machine), Milestone: M1}
		name := engineName(s.Kind)
		switch {
		case s.Usable && s.Kind == "docker-desktop":
			f.Severity, f.Title = doctor.OK, s.Machine.Name+" uses Docker Desktop "+s.Version
			f.Detail = "Docker Desktop answers. Container runners use it as it is; crsp never changes its settings."
		case s.Usable:
			f.Severity, f.Title = doctor.OK, s.Machine.Name+" uses "+name+" "+s.Version
			f.Detail = "The engine answers."
		default:
			f.Severity, f.Title = doctor.Warn, "The container engine is not usable on "+s.Machine.Name
			f.Detail = s.Reason + "."
			if s.Reason == "" {
				f.Detail = "The engine does not answer."
			}
			f.Detail += " Container runners cannot start here."
			f.Fix = engineFix(s)
			// DEP14: the wizard "Get this machine ready" opens from the machine page; the doctor still only reads.
			f.Fix.Text += " Or use Get this machine ready on the machine page, and crsp installs it with your yes."
			f.Fix.Link = machineLink(s.Machine) + EngineSetupQuery
		}
		out = append(out, f)
	}
	return out
}

// EngineSetupQuery opens the dependency setup wizard on the machine page (dependency-setup DEP14).
const EngineSetupQuery = "?setup=deps"

func engineFix(s doctor.EngineState) doctor.Fix {
	switch {
	case s.Machine.OS == "windows" && s.Kind == "docker-desktop":
		return doctor.Fix{Text: "Start Docker Desktop and wait until it says the engine is running."}
	case s.Machine.OS == "windows":
		return doctor.Fix{Text: "Start the engine: open Docker Desktop, or in a WSL2 distro run `sudo service docker start`. If neither is installed, install Docker in WSL2 (preferred) or Docker Desktop."}
	case s.Machine.OS == "darwin":
		return doctor.Fix{Text: "Start Docker Desktop and wait until it says the engine is running. If it is not installed, install Docker Desktop (check its licence first)."}
	}
	return doctor.Fix{Text: "Install Docker and start its service (for example `sudo systemctl enable --now docker`)."}
}
