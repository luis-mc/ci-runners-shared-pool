package checks

import (
	"context"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

// The extra packages check of the doctor (runners R5.86): a package set that
// an agent could not install into a runner environment is a failure, with the
// names and the tool's last words.

// PackageFailure is one failed package set of one machine.
type PackageFailure struct {
	MachineID, MachineName string
	SetHash                string
	Names                  []string
	Error                  string
	At                     time.Time
}

// PackageSource gives the check the failures the agents reported in their
// machine reports. It returns doctor.ErrUnreachable-style errors as usual.
type PackageSource interface {
	PackageFailures(ctx context.Context) ([]PackageFailure, error)
}

// RegisterPackages adds the extra packages check.
func RegisterPackages(reg *doctor.Registry, src PackageSource) {
	reg.Register(doctor.NewCheck("runner.packages", doctor.GroupRunner, M2, false, func(ctx context.Context, env doctor.Env) []doctor.Finding {
		fs, err := src.PackageFailures(ctx)
		if err != nil {
			return []doctor.Finding{skipFor("runner.packages", controllerSubject, "The extra package reports", err)}
		}
		var out []doctor.Finding
		for _, f := range fs {
			if !env.Scope.MatchMachine(f.MachineID, f.MachineName) {
				continue
			}
			out = append(out, doctor.Finding{
				Check:    "runner.packages",
				Subject:  doctor.Subject{Kind: "machine", ID: f.MachineID, Name: f.MachineName},
				Severity: doctor.Fail,
				Title:    "Extra packages did not install on " + f.MachineName,
				Detail:   "Packages " + strings.Join(f.Names, ", ") + " (set " + f.SetHash + "): " + f.Error,
				Fix: doctor.Fix{Text: "Correct the package names of the repository's pool, or install what the package tool needs, then add the runner again.",
					Command: "crsp repo packages OWNER/NAME"},
			})
		}
		return out
	}))
}
