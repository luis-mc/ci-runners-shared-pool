package checks

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

// BaseImageProbe is the runner base image of one backend on one machine: a
// container image (digest or tag) or a VM base disk (runners R5.74).
type BaseImageProbe struct {
	MachineID, MachineName string
	Backend                string
	Ref                    string
	Present                bool
	// Fix is the command or step that makes the image again.
	Fix string
}

// BaseImageSource lists the base image probes of every machine, or
// doctor.ErrUnreachable when a machine cannot be asked.
type BaseImageSource interface {
	BaseImages(ctx context.Context) ([]BaseImageProbe, error)
}

// RegisterBaseImage adds `backend.baseimage`: each backend's runner base image
// exists on its machine. A missing one is a failure: the machine takes no job
// (pause reason base_image_missing) until the image is back.
func RegisterBaseImage(reg *doctor.Registry, src BaseImageSource) {
	reg.Register(doctor.NewCheck("backend.baseimage", doctor.GroupBackend, M2, false, func(ctx context.Context, env doctor.Env) []doctor.Finding {
		const id = "backend.baseimage"
		ps, err := src.BaseImages(ctx)
		if err != nil {
			return []doctor.Finding{skipFor(id, controllerSubject, "The runner base images", err)}
		}
		var out []doctor.Finding
		for _, p := range ps {
			if !env.Scope.MatchMachine(p.MachineID, p.MachineName) {
				continue
			}
			s := doctor.Subject{Kind: "machine", ID: p.MachineID, Name: p.MachineName}
			if p.Present {
				out = append(out, doctor.Finding{Check: id, Subject: s, Severity: doctor.OK,
					Title: "The " + p.Backend + " base image is on " + p.MachineName, Detail: p.Ref + " exists."})
				continue
			}
			out = append(out, doctor.Finding{Check: id, Subject: s, Severity: doctor.Fail,
				Title:  "The " + p.Backend + " base image is missing on " + p.MachineName,
				Detail: p.Ref + " is not there. The machine takes no new jobs (paused: base image missing) until it is back.",
				Fix:    doctor.Fix{Text: p.Fix, Link: machineLink(doctor.Machine{ID: p.MachineID})}})
		}
		return out
	}))
}
