package checks

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

// RegisterVM adds the VM backend checks (caches R5.46, `crsp vm doctor`). The
// live controller does not register them: no agent reports the VM state yet,
// so only `crsp vm doctor` runs them, on the machine it is started on.
func RegisterVM(reg *doctor.Registry) {
	for _, c := range []doctor.Check{
		doctor.NewCheck("vm.accel", doctor.GroupBackend, M2, false, vmAccel),
		doctor.NewCheck("vm.tools", doctor.GroupBackend, M2, false, vmTools),
		doctor.NewCheck("vm.seedtool", doctor.GroupBackend, M2, false, vmSeedTool),
		doctor.NewCheck("vm.basedisk", doctor.GroupBackend, M2, false, vmBaseDisk),
		doctor.NewCheck("vm.disk", doctor.GroupBackend, M2, false, vmDisk),
		doctor.NewCheck("vm.network", doctor.GroupBackend, M2, false, vmNetwork),
		doctor.NewCheck("vm.packages", doctor.GroupBackend, M2, false, vmPackages),
	} {
		reg.Register(c)
	}
}

// vmState reads the VM state once per run. A nil source or an error is a skip
// finding for the check, never a fail (R5.5).
func vmState(ctx context.Context, env doctor.Env, check string) (doctor.VMState, []doctor.Finding, bool) {
	if env.VM == nil {
		return doctor.VMState{}, []doctor.Finding{skip(check, controllerSubject, "The VM backend is not read", "No source reports the VM backend, so it was not checked. Run `crsp vm doctor` on the machine.")}, false
	}
	v, err := env.Memo("vm.state", func() (any, error) { return env.VM.VM(ctx) })
	if err != nil {
		return doctor.VMState{}, []doctor.Finding{skipFor(check, controllerSubject, "The VM backend", err)}, false
	}
	st, ok := v.(doctor.VMState)
	if !ok {
		return doctor.VMState{}, []doctor.Finding{skip(check, controllerSubject, "The VM backend is not read", "The source gave an unexpected answer.")}, false
	}
	if !env.InScopeMachine(st.Machine) {
		return doctor.VMState{}, nil, false
	}
	return st, nil, true
}

func vmSubject(st doctor.VMState) doctor.Subject { return machineSubject(st.Machine) }

func vmDistroSubject(d string) doctor.Subject {
	return doctor.Subject{Kind: "vm-image", ID: d, Name: d}
}

// vmAccel: KVM, Hyper-V or Virtualization.framework is there (caches R5.46).
func vmAccel(ctx context.Context, env doctor.Env) []doctor.Finding {
	st, out, ok := vmState(ctx, env, "vm.accel")
	if !ok {
		return out
	}
	f := doctor.Finding{Check: "vm.accel", Subject: vmSubject(st), Milestone: M2}
	name := st.Accelerator
	if name == "" {
		name = "The hypervisor"
	}
	switch {
	case !st.AccelKnown:
		f.Severity, f.Title = doctor.Skip, name+" was not asked on "+st.Machine.Name
		f.Detail = st.AccelDetail
		f.Fix = doctor.Fix{Text: "Nothing to do now. Run `crsp vm doctor` again."}
	case st.AccelOK:
		f.Severity, f.Title = doctor.OK, name+" is available on "+st.Machine.Name
	default:
		f.Severity, f.Title = doctor.Fail, name+" is not available on "+st.Machine.Name
		f.Detail = st.AccelDetail
		f.Fix = vmAccelFix(st.Accelerator)
		f.NeedsAdmin = true
	}
	return []doctor.Finding{f}
}

func vmAccelFix(accel string) doctor.Fix {
	switch accel {
	case "Hyper-V":
		return doctor.Fix{Text: "Turn on Hyper-V in an administrator PowerShell, then restart the PC.", Command: "Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V-All"}
	case "Virtualization.framework":
		return doctor.Fix{Text: "Use a Mac with Apple silicon or an Intel Mac on macOS 11 or newer; `sysctl kern.hv_support` must print 1. Virtual machines inside another VM do not work."}
	}
	return doctor.Fix{Text: "Load the module (kvm_intel or kvm_amd), turn on virtualization in the firmware, and add the runner user to the kvm group.", Command: "sudo modprobe kvm_intel || sudo modprobe kvm_amd"}
}

// vmTools: qemu, ssh and their helpers are on the PATH.
func vmTools(ctx context.Context, env doctor.Env) []doctor.Finding {
	st, out, ok := vmState(ctx, env, "vm.tools")
	if !ok {
		return out
	}
	f := doctor.Finding{Check: "vm.tools", Subject: vmSubject(st), Milestone: M2}
	var missing []string
	for _, t := range st.Tools {
		if !t.Found {
			missing = append(missing, t.Name)
		}
	}
	if len(missing) == 0 {
		f.Severity, f.Title = doctor.OK, "The VM tools are installed on "+st.Machine.Name
		return []doctor.Finding{f}
	}
	f.Severity, f.Title = doctor.Fail, "VM tools are missing on "+st.Machine.Name+": "+strings.Join(missing, ", ")
	f.Detail = "`crsp vm` starts these programs and cannot run without them."
	f.Fix = doctor.Fix{Text: "Install the qemu and openssh packages of the distribution (the kvm layer of the image has them).", Command: "sudo apt-get install -y qemu-system-x86 qemu-utils openssh-client"}
	return []doctor.Finding{f}
}

// vmSeedTool: the cloud-init seed tool (cloud-localds) is there.
func vmSeedTool(ctx context.Context, env doctor.Env) []doctor.Finding {
	st, out, ok := vmState(ctx, env, "vm.seedtool")
	if !ok {
		return out
	}
	f := doctor.Finding{Check: "vm.seedtool", Subject: vmSubject(st), Milestone: M2}
	name := st.SeedTool.Name
	if name == "" {
		name = "cloud-localds"
	}
	if st.SeedTool.Found {
		f.Severity, f.Title = doctor.OK, name+" is installed on "+st.Machine.Name
		return []doctor.Finding{f}
	}
	f.Severity, f.Title = doctor.Fail, name+" is missing on "+st.Machine.Name
	f.Detail = "`crsp vm run` builds the cloud-init seed disk with it; without it no guest gets its user and key."
	f.Fix = doctor.Fix{Text: "Install the cloud-init image tools (cloud-image-utils on Debian and Ubuntu, cloud-utils on Fedora and Rocky).", Command: "sudo apt-get install -y cloud-image-utils"}
	return []doctor.Finding{f}
}

// vmBaseDisk: for each distro in the cache or asked for, the image is there
// with the digest its name says and the prepared base disk is there.
func vmBaseDisk(ctx context.Context, env doctor.Env) []doctor.Finding {
	st, out, ok := vmState(ctx, env, "vm.basedisk")
	if !ok {
		return out
	}
	if len(st.Disks) == 0 {
		return []doctor.Finding{{Check: "vm.basedisk", Subject: vmSubject(st), Severity: doctor.Info, Milestone: M2,
			Title:  "No base disk in the VM cache on " + st.Machine.Name,
			Detail: "The cache " + st.CacheDir + " holds no image yet. The first `crsp vm run` downloads one.",
			Fix:    doctor.Fix{Text: "Nothing to do now. To have one ready, prepare a distro.", Command: "crsp vm prepare <distro>"}}}
	}
	disks := append([]doctor.VMDisk(nil), st.Disks...)
	sort.Slice(disks, func(i, j int) bool { return disks[i].Distro < disks[j].Distro })
	for _, d := range disks {
		f := doctor.Finding{Check: "vm.basedisk", Subject: vmDistroSubject(d.Distro), Milestone: M2}
		prepare := doctor.Fix{Text: "Download the image again and make the base disk.", Command: "crsp vm prepare " + d.Distro}
		switch {
		case !d.ImagePresent:
			f.Severity, f.Title = doctor.Fail, "The "+d.Distro+" image is not in the VM cache"
			f.Detail = "A run of this distro would download it first."
			f.Fix = prepare
		case !d.ImageChecked:
			f.Severity, f.Title = doctor.Skip, "The "+d.Distro+" image could not be read"
			f.Fix = doctor.Fix{Text: "Check that the cache folder " + st.CacheDir + " is readable, then run the doctor again."}
		case !d.ImageOK:
			f.Severity, f.Title = doctor.Fail, "The "+d.Distro+" image does not match its digest"
			f.Detail = "The file is damaged or was changed. A VM on it would not be the image that was published."
			f.Fix = doctor.Fix{Text: "Remove the image file and the base disk of this distro from the cache, then prepare it again.", Command: "crsp vm prepare " + d.Distro}
		case !d.BasePresent:
			f.Severity, f.Title = doctor.Warn, "The "+d.Distro+" base disk is not made yet"
			f.Detail = "The image is there and its digest matches; the first run makes the base disk."
			f.Fix = prepare
		default:
			f.Severity, f.Title = doctor.OK, "The "+d.Distro+" image and base disk are there and the digest matches"
		}
		out = append(out, f)
	}
	return out
}

// vmDisk: free space of the cache volume is above the floor.
func vmDisk(ctx context.Context, env doctor.Env) []doctor.Finding {
	st, out, ok := vmState(ctx, env, "vm.disk")
	if !ok {
		return out
	}
	f := doctor.Finding{Check: "vm.disk", Subject: vmSubject(st), Milestone: M2}
	const gib = int64(1) << 30
	switch {
	case !st.FreeKnown:
		f.Severity, f.Title = doctor.Skip, "The free disk of the VM cache on "+st.Machine.Name+" was not read"
		f.Fix = doctor.Fix{Text: "Nothing to do now. Run `crsp vm doctor` again."}
	case st.FreeBytes < st.FloorBytes:
		f.Severity, f.Title = doctor.Fail, fmt.Sprintf("Only %d GiB are free for the VM cache on %s, the floor is %d GiB", st.FreeBytes/gib, st.Machine.Name, st.FloorBytes/gib)
		f.Detail = "A run's overlay and a new image need room; a full disk stops the guest in the middle of a job."
		f.Fix = doctor.Fix{Text: "Remove old images and base disks, or move the cache with CRSP_VM_CACHE to a bigger disk.", Command: "crsp vm prune --keep 1"}
	default:
		f.Severity, f.Title = doctor.OK, fmt.Sprintf("%d GiB are free for the VM cache on %s (floor %d GiB)", st.FreeBytes/gib, st.Machine.Name, st.FloorBytes/gib)
	}
	return []doctor.Finding{f}
}

// vmNetwork: the bridge the VMs may use exists. VMs also boot without it on
// user-mode networking, so a missing bridge is a warning.
func vmNetwork(ctx context.Context, env doctor.Env) []doctor.Finding {
	st, out, ok := vmState(ctx, env, "vm.network")
	if !ok {
		return out
	}
	f := doctor.Finding{Check: "vm.network", Subject: vmSubject(st), Milestone: M2}
	switch {
	case !st.BridgeKnown:
		f.Severity, f.Title = doctor.Skip, "The network bridge of "+st.Machine.Name+" was not read"
		f.Detail = "This OS has no bridge to ask about, or it did not answer."
		f.Fix = doctor.Fix{Text: "Nothing to do now. Run `crsp vm doctor` again."}
	case st.BridgePresent:
		f.Severity, f.Title = doctor.OK, "The bridge "+st.Bridge+" is there on "+st.Machine.Name
	default:
		f.Severity, f.Title = doctor.Warn, "The bridge "+st.Bridge+" is missing on "+st.Machine.Name
		f.Detail = "Guests still start with user-mode networking (`-nic user`), which reaches out but cannot be reached from the network."
		f.Fix = doctor.Fix{Text: "Make the bridge (libvirt makes virbr0 with its default network), or set CRSP_VM_BRIDGE to the bridge you have."}
	}
	return []doctor.Finding{f}
}

// vmPackages: the packages the runners need are in the manifest of each base
// disk. A missing one is a drift (caches R5.46).
func vmPackages(ctx context.Context, env doctor.Env) []doctor.Finding {
	st, out, ok := vmState(ctx, env, "vm.packages")
	if !ok {
		return out
	}
	if len(st.Needed) == 0 {
		return []doctor.Finding{{Check: "vm.packages", Subject: vmSubject(st), Severity: doctor.Info, Milestone: M2,
			Title:  "No package is asked for in the VM base disks on " + st.Machine.Name,
			Detail: "No needed package was given, so there is nothing to compare with the base disk manifests.",
			Fix:    doctor.Fix{Text: "Nothing to do. List the packages with --need or CRSP_VM_PACKAGES to have them compared."}}}
	}
	var disks []doctor.VMDisk
	for _, d := range st.Disks {
		if d.BasePresent {
			disks = append(disks, d)
		}
	}
	if len(disks) == 0 {
		return []doctor.Finding{{Check: "vm.packages", Subject: vmSubject(st), Severity: doctor.Info, Milestone: M2,
			Title: "No base disk to compare packages with on " + st.Machine.Name,
			Fix:   doctor.Fix{Text: "Nothing to do now. See vm.basedisk."}}}
	}
	sort.Slice(disks, func(i, j int) bool { return disks[i].Distro < disks[j].Distro })
	for _, d := range disks {
		have := map[string]bool{}
		for _, p := range d.Packages {
			have[p] = true
		}
		var missing []string
		for _, p := range st.Needed {
			if !have[p] {
				missing = append(missing, p)
			}
		}
		sort.Strings(missing)
		f := doctor.Finding{Check: "vm.packages", Subject: vmDistroSubject(d.Distro), Milestone: M2}
		if len(missing) == 0 {
			f.Severity, f.Title = doctor.OK, "The "+d.Distro+" base disk has every package the runners need"
		} else {
			f.Severity, f.Title = doctor.Warn, "The "+d.Distro+" base disk drifted: "+strings.Join(missing, ", ")+" missing"
			f.Detail = "The manifest of the base disk does not list " + strings.Join(missing, ", ") + ". Jobs that need it install it in every run."
			f.Fix = doctor.Fix{Text: "crsp does not install packages into a base disk yet. Install the package in the job with `crsp vm run " + d.Distro + " --install <command>`, or take it off the needed list."}
		}
		out = append(out, f)
	}
	return out
}
