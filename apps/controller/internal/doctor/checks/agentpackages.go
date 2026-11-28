package checks

import (
	"context"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

// agentPackages says whether the release this controller was built from holds every agent installer: the six
// archives the controller hands out, and the native packages (msi, pkg, deb) next to them (operations R5.134,
// CRSP348). A missing archive stops the agent install for that system, so it fails; a missing native package
// only warns, because the archives still work.
func agentPackages(ctx context.Context, env doctor.Env) []doctor.Finding {
	const id = "install.agent_packages"
	if env.AgentPackages == nil {
		return []doctor.Finding{skip(id, controllerSubject, "The agent installers are not checked", "Nothing was checked.")}
	}
	st, err := env.AgentPackages.AgentPackages(ctx)
	if err != nil {
		return []doctor.Finding{skipFor(id, controllerSubject, "The agent installers of this release", err)}
	}
	f := doctor.Finding{Check: id, Subject: controllerSubject, Severity: doctor.OK,
		Title: "Release " + st.Version + " holds every agent installer"}
	switch {
	case len(st.MissingArchives) > 0:
		f.Severity = doctor.Fail
		f.Title = "Release " + st.Version + " lacks agent installers for " + strings.Join(st.MissingArchives, ", ")
		f.Detail = "A machine of that system cannot get the agent from this controller."
		f.Fix = doctor.Fix{Text: "Update the controller to a release that has them, or import the file in Machines.", Link: "/settings/updates"}
	case len(st.MissingNative) > 0:
		f.Severity = doctor.Warn
		f.Title = "Release " + st.Version + " lacks native agent packages: " + strings.Join(st.MissingNative, ", ")
		f.Detail = "The archives still install the agent; only the native packages are missing."
		f.Fix = doctor.Fix{Text: "Update the controller to a newer release.", Link: "/settings/updates"}
	}
	return []doctor.Finding{f}
}
