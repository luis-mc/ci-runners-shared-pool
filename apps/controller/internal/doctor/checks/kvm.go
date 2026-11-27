package checks

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

// KVMReportedChecks are the two KVM checks the agent's machine report feeds:
// kvm.wslconfig and kvm.module (doctor R5.32, machines R5.53). The controller
// registers them with the live doctor. The other three need the pool's
// container and label, which the agent does not report yet.
func KVMReportedChecks() []doctor.Check {
	return []doctor.Check{
		doctor.NewCheck("kvm.wslconfig", doctor.GroupBackend, M2, false, kvmWslconfig),
		doctor.NewCheck("kvm.module", doctor.GroupBackend, M2, false, kvmModule),
	}
}

// RegisterKVM adds all five KVM checks (doctor R5.32), for a source that knows
// the pool's container and label too. The live doctor registers only
// KVMReportedChecks.
func RegisterKVM(reg *doctor.Registry) {
	for _, c := range []doctor.Check{
		doctor.NewCheck("kvm.host", doctor.GroupBackend, M2, true, kvmHost),
		doctor.NewCheck("kvm.container", doctor.GroupBackend, M2, false, kvmContainer),
		doctor.NewCheck("kvm.label", doctor.GroupBackend, M2, false, kvmLabel),
	} {
		reg.Register(c)
	}
	for _, c := range KVMReportedChecks() {
		reg.Register(c)
	}
}

func kvmStates(ctx context.Context, env doctor.Env, check string) ([]doctor.KVMState, []doctor.Finding) {
	if env.KVM == nil {
		return nil, []doctor.Finding{skip(check, controllerSubject, "KVM is not reported", "No agent reports KVM for a pool, so it was not checked.")}
	}
	sts, err := env.KVM.KVM(ctx)
	if err != nil {
		return nil, []doctor.Finding{skipFor(check, controllerSubject, "The KVM state", err)}
	}
	var out []doctor.KVMState
	for _, s := range sts {
		if env.InScopeMachine(s.Machine) {
			out = append(out, s)
		}
	}
	return out, nil
}

// kvmHost: the device check. Fail only when KVM is on and the host has none
// (doctor R5.32; port of pool.<name>.kvm-host). A pool that waited out and
// started without KVM says why.
func kvmHost(ctx context.Context, env doctor.Env) []doctor.Finding {
	sts, out := kvmStates(ctx, env, "kvm.host")
	for _, s := range sts {
		f := doctor.Finding{Check: "kvm.host", Subject: machineSubject(s.Machine)}
		switch {
		case s.HostDevice:
			f.Severity, f.Title = doctor.OK, "/dev/kvm is there on "+s.Machine.Name
		default:
			f.Severity, f.Title = doctor.Fail, "/dev/kvm is missing on "+s.Machine.Name
			f.Detail = "KVM is on for the pool but the host has no /dev/kvm."
			if s.WaitedOut {
				f.Detail += " The pool waited for it, gave up, and started without KVM (no kvm label)."
			}
			if s.Machine.OS == "windows" && !s.LoaderRunning {
				f.Detail += " The kvm loader service is not running, so nothing loads kvm_intel or kvm_amd after a WSL or Docker restart."
				f.Fix = doctor.Fix{Text: "Start the pool with the KVM compose file, which holds the loader service.", Command: "docker compose -f compose.yaml -f compose.kvm.yaml up -d"}
			} else {
				f.Fix = doctor.Fix{Text: "Load the module (kvm_intel or kvm_amd) on the host, or turn KVM off for the pool."}
			}
			f.NeedsAdmin = true
		}
		f.Milestone = M2
		out = append(out, f)
	}
	return out
}

// kvmContainer: /dev/kvm usable inside a pool container.
func kvmContainer(ctx context.Context, env doctor.Env) []doctor.Finding {
	sts, out := kvmStates(ctx, env, "kvm.container")
	for _, s := range sts {
		f := doctor.Finding{Check: "kvm.container", Subject: machineSubject(s.Machine), Milestone: M2}
		switch {
		case !s.HostDevice:
			f.Severity, f.Title = doctor.Skip, "Container KVM was not checked on "+s.Machine.Name
			f.Detail = "The host has no /dev/kvm; see kvm.host."
			f.Fix = doctor.Fix{Text: "Fix kvm.host first."}
		case s.ContainerUsable:
			f.Severity, f.Title = doctor.OK, "/dev/kvm opens inside a container on "+s.Machine.Name
		default:
			f.Severity, f.Title = doctor.Fail, "/dev/kvm does not open inside a container on "+s.Machine.Name
			f.Detail = "The device is on the host but a job container cannot use it. It is passed with --device only, never --privileged; the container user needs the device's group."
			f.Fix = doctor.Fix{Text: "Rebuild the pool container so it gets --device /dev/kvm and the device group."}
		}
		out = append(out, f)
	}
	return out
}

// kvmLabel: the kvm label matches what works (runners R5.36).
func kvmLabel(ctx context.Context, env doctor.Env) []doctor.Finding {
	sts, out := kvmStates(ctx, env, "kvm.label")
	for _, s := range sts {
		f := doctor.Finding{Check: "kvm.label", Subject: machineSubject(s.Machine), Milestone: M2}
		works := s.HostDevice && s.ContainerUsable
		switch {
		case works == s.LabelPresent:
			f.Severity, f.Title = doctor.OK, "The kvm label matches on "+s.Machine.Name
		case s.LabelPresent:
			f.Severity, f.Title = doctor.Fail, "The kvm label is set on "+s.Machine.Name+" but KVM does not work"
			f.Detail = "A job that asks for kvm would start and fail."
			f.Fix = doctor.Fix{Text: "Remove the kvm label from the pool's runners until kvm.host and kvm.container are ok."}
		default:
			f.Severity, f.Title = doctor.Warn, "KVM works on "+s.Machine.Name+" but the runners have no kvm label"
			f.Detail = "Jobs that ask for kvm wait for a runner that has it."
			f.Fix = doctor.Fix{Text: "Add the kvm label to the pool's runners."}
		}
		out = append(out, f)
	}
	return out
}

// kvmWslconfig: on Windows, nestedVirtualization=true under [wsl2] (machines R5.53).
func kvmWslconfig(ctx context.Context, env doctor.Env) []doctor.Finding {
	sts, out := kvmStates(ctx, env, "kvm.wslconfig")
	for _, s := range sts {
		if s.Machine.OS != "windows" {
			continue
		}
		f := doctor.Finding{Check: "kvm.wslconfig", Subject: machineSubject(s.Machine), Milestone: M2}
		switch {
		case !s.NestedKnown:
			f.Severity, f.Title = doctor.Skip, ".wslconfig of "+s.Machine.Name+" was not read"
			f.Detail = "The agent could not read the file."
			f.Fix = doctor.Fix{Text: "Nothing to do now. Run the doctor again."}
		case s.Nested:
			f.Severity, f.Title = doctor.OK, "Nested virtualization is on in .wslconfig on "+s.Machine.Name
		default:
			f.Severity, f.Title = doctor.Fail, "Nested virtualization is off in .wslconfig on "+s.Machine.Name
			f.Detail = "WSL2 cannot offer /dev/kvm without it. crsp never edits this file without a yes."
			f.Fix = doctor.Fix{Text: "Add these lines to %UserProfile%\\.wslconfig, then run `wsl --shutdown` (this stops every WSL distro):\n[wsl2]\nnestedVirtualization=true", Command: "wsl --shutdown"}
		}
		out = append(out, f)
	}
	return out
}

// kvmModule: on Windows, kvm_intel or kvm_amd is loaded in the WSL VM (machines
// R5.53). After `wsl --shutdown` or a reboot /dev/kvm is gone until it is loaded
// again. The doctor reads this and says how to fix it; it never loads the module.
func kvmModule(ctx context.Context, env doctor.Env) []doctor.Finding {
	sts, out := kvmStates(ctx, env, "kvm.module")
	for _, s := range sts {
		if s.Machine.OS != "windows" {
			continue
		}
		f := doctor.Finding{Check: "kvm.module", Subject: machineSubject(s.Machine), Milestone: M2}
		switch {
		case !s.ModuleKnown:
			f.Severity, f.Title = doctor.Skip, "The WSL VM of "+s.Machine.Name+" was not asked about the kvm module"
			f.Detail = "The agent could not run wsl.exe."
			f.Fix = doctor.Fix{Text: "Nothing to do now. Run the doctor again."}
		case s.ModuleLoaded:
			f.Severity, f.Title = doctor.OK, "The kvm module is loaded in the WSL VM on "+s.Machine.Name
		case s.NestedKnown && !s.Nested:
			f.Severity, f.Title = doctor.Info, "The kvm module is not loaded in the WSL VM on "+s.Machine.Name
			f.Detail = "It cannot load while nested virtualization is off in .wslconfig; see kvm.wslconfig."
			f.Fix = doctor.Fix{Text: "Turn on nestedVirtualization first (kvm.wslconfig)."}
		default:
			f.Severity, f.Title = doctor.Fail, "The kvm module is not loaded in the WSL VM on "+s.Machine.Name
			f.Detail = "kvm_intel or kvm_amd is gone after every `wsl --shutdown` or reboot, and /dev/kvm with it. The pool's kvm loader service loads it at each start."
			f.Fix = doctor.Fix{Text: "Start the pool with the KVM compose file, which holds the loader service. To load it by hand once: `wsl -u root modprobe kvm_intel` (kvm_amd on an AMD CPU). crsp changes nothing by itself.", Command: "docker compose -f compose.yaml -f compose.kvm.yaml up -d"}
			f.NeedsAdmin = true
		}
		out = append(out, f)
	}
	return out
}
