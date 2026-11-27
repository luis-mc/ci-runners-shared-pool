package checks

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

type vmFake struct {
	st  doctor.VMState
	err error
}

func (v vmFake) VM(context.Context) (doctor.VMState, error) { return v.st, v.err }

const gibB = int64(1) << 30

// goodVM is a machine where every VM check is ok.
func goodVM() doctor.VMState {
	return doctor.VMState{
		Machine:     doctor.Machine{ID: "local", Name: "box", OS: "linux"},
		Accelerator: "KVM", AccelKnown: true, AccelOK: true,
		Tools:    []doctor.VMTool{{Name: "qemu-system-x86_64", Found: true}, {Name: "qemu-img", Found: true}},
		SeedTool: doctor.VMTool{Name: "cloud-localds", Found: true},
		Disks: []doctor.VMDisk{{Distro: "rocky-9", ImagePresent: true, ImageChecked: true, ImageOK: true, BasePresent: true,
			Packages: []string{"git", "make"}}},
		CacheDir:  "/c",
		FreeKnown: true, FreeBytes: 100 * gibB, FloorBytes: 20 * gibB,
		Bridge: "virbr0", BridgeKnown: true, BridgePresent: true,
		Needed: []string{"git"},
	}
}

func vmRun(t *testing.T, src doctor.VMSource, check string) []doctor.Finding {
	t.Helper()
	reg := doctor.NewRegistry()
	RegisterVM(reg)
	e := &doctor.Engine{Registry: reg, Store: doctor.NewMemStore(), Now: func() time.Time { return now },
		Env: func(context.Context) (doctor.Env, error) { return doctor.Env{VM: src}, nil }}
	r, err := e.Run(context.Background(), doctor.Scope{Checks: []string{check}}, doctor.TriggerCLI, "test")
	if err != nil {
		t.Fatal(err)
	}
	return r.Findings
}

func one(t *testing.T, fs []doctor.Finding) doctor.Finding {
	t.Helper()
	if len(fs) != 1 {
		t.Fatalf("want one finding, got %+v", fs)
	}
	return fs[0]
}

func TestVMEveryCheckIsOkOnAGoodMachine(t *testing.T) {
	for _, id := range []string{"vm.accel", "vm.tools", "vm.seedtool", "vm.basedisk", "vm.disk", "vm.network", "vm.packages"} {
		f := one(t, vmRun(t, vmFake{st: goodVM()}, id))
		if f.Severity != doctor.OK || f.Check != id {
			t.Errorf("%s: %+v", id, f)
		}
	}
}

func TestVMChecksSkipWithoutASourceOrWhenItFails(t *testing.T) {
	for _, src := range []doctor.VMSource{nil, vmFake{err: doctor.ErrUnreachable}, vmFake{err: errors.New("boom")}} {
		f := one(t, vmRun(t, src, "vm.accel"))
		if f.Severity != doctor.Skip || f.Fix.Text == "" {
			t.Errorf("%T: %+v", src, f)
		}
	}
}

func TestVMAccelFixPerOS(t *testing.T) {
	cases := map[string]string{"KVM": "modprobe", "Hyper-V": "Enable-WindowsOptionalFeature", "Virtualization.framework": "kern.hv_support"}
	for accel, want := range cases {
		st := goodVM()
		st.Accelerator, st.AccelOK, st.AccelDetail = accel, false, "no"
		f := one(t, vmRun(t, vmFake{st: st}, "vm.accel"))
		if f.Severity != doctor.Fail || !f.NeedsAdmin || !strings.Contains(f.Fix.Command+f.Fix.Text, want) {
			t.Errorf("%s: %+v", accel, f)
		}
	}
	st := goodVM()
	st.AccelKnown = false
	if f := one(t, vmRun(t, vmFake{st: st}, "vm.accel")); f.Severity != doctor.Skip {
		t.Errorf("unknown: %+v", f)
	}
}

func TestVMToolsNamesWhatIsMissing(t *testing.T) {
	st := goodVM()
	st.Tools[1].Found = false
	f := one(t, vmRun(t, vmFake{st: st}, "vm.tools"))
	if f.Severity != doctor.Fail || !strings.Contains(f.Title, "qemu-img") || strings.Contains(f.Title, "qemu-system") || f.Fix.Text == "" {
		t.Errorf("%+v", f)
	}
}

func TestVMSeedToolMissing(t *testing.T) {
	st := goodVM()
	st.SeedTool.Found = false
	f := one(t, vmRun(t, vmFake{st: st}, "vm.seedtool"))
	if f.Severity != doctor.Fail || !strings.Contains(f.Title, "cloud-localds") || f.Fix.Command == "" {
		t.Errorf("%+v", f)
	}
}

func TestVMBaseDiskStates(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*doctor.VMDisk)
		sev  doctor.Severity
		fix  string
	}{
		{"image missing", func(d *doctor.VMDisk) { d.ImagePresent = false }, doctor.Fail, "crsp vm prepare rocky-9"},
		{"unreadable", func(d *doctor.VMDisk) { d.ImageChecked = false }, doctor.Skip, "readable"},
		{"sha differs", func(d *doctor.VMDisk) { d.ImageOK = false }, doctor.Fail, "crsp vm prepare rocky-9"},
		{"base missing", func(d *doctor.VMDisk) { d.BasePresent = false }, doctor.Warn, "crsp vm prepare rocky-9"},
	}
	for _, c := range cases {
		st := goodVM()
		c.mut(&st.Disks[0])
		f := one(t, vmRun(t, vmFake{st: st}, "vm.basedisk"))
		if f.Severity != c.sev || !strings.Contains(f.Fix.Command+f.Fix.Text, c.fix) || f.Subject.ID != "rocky-9" {
			t.Errorf("%s: %+v", c.name, f)
		}
	}
}

func TestVMBaseDiskOneLinePerDistroAndInfoWhenCacheIsEmpty(t *testing.T) {
	st := goodVM()
	st.Disks = append(st.Disks, doctor.VMDisk{Distro: "alma-9"})
	fs := vmRun(t, vmFake{st: st}, "vm.basedisk")
	if len(fs) != 2 {
		t.Fatalf("%+v", fs)
	}
	st.Disks = nil
	f := one(t, vmRun(t, vmFake{st: st}, "vm.basedisk"))
	if f.Severity != doctor.Info || f.Fix.Command == "" {
		t.Errorf("%+v", f)
	}
}

func TestVMDiskFloor(t *testing.T) {
	st := goodVM()
	st.FreeBytes = 5 * gibB
	f := one(t, vmRun(t, vmFake{st: st}, "vm.disk"))
	if f.Severity != doctor.Fail || !strings.Contains(f.Title, "5 GiB") || f.Fix.Command != "crsp vm prune --keep 1" {
		t.Errorf("%+v", f)
	}
	st.FreeBytes = 20 * gibB // exactly at the floor is enough
	if f := one(t, vmRun(t, vmFake{st: st}, "vm.disk")); f.Severity != doctor.OK {
		t.Errorf("at floor: %+v", f)
	}
	st.FreeKnown = false
	if f := one(t, vmRun(t, vmFake{st: st}, "vm.disk")); f.Severity != doctor.Skip {
		t.Errorf("unknown: %+v", f)
	}
}

func TestVMNetworkBridge(t *testing.T) {
	st := goodVM()
	st.BridgePresent = false
	f := one(t, vmRun(t, vmFake{st: st}, "vm.network"))
	if f.Severity != doctor.Warn || !strings.Contains(f.Title, "virbr0") || f.Fix.Text == "" {
		t.Errorf("%+v", f)
	}
	st.BridgeKnown = false
	if f := one(t, vmRun(t, vmFake{st: st}, "vm.network")); f.Severity != doctor.Skip {
		t.Errorf("unknown: %+v", f)
	}
}

func TestVMPackagesDriftNamesTheMissingOnes(t *testing.T) {
	st := goodVM()
	st.Needed = []string{"zip", "git", "libfoo"}
	f := one(t, vmRun(t, vmFake{st: st}, "vm.packages"))
	if f.Severity != doctor.Warn || !strings.Contains(f.Title, "libfoo, zip") || strings.Contains(f.Title, "git") || f.Subject.ID != "rocky-9" {
		t.Errorf("%+v", f)
	}
}

func TestVMPackagesInfoWithoutNeedOrBase(t *testing.T) {
	st := goodVM()
	st.Needed = nil
	if f := one(t, vmRun(t, vmFake{st: st}, "vm.packages")); f.Severity != doctor.Info {
		t.Errorf("no need: %+v", f)
	}
	st = goodVM()
	st.Disks[0].BasePresent = false
	if f := one(t, vmRun(t, vmFake{st: st}, "vm.packages")); f.Severity != doctor.Info {
		t.Errorf("no base: %+v", f)
	}
}

func TestVMChecksAreNotInTheLiveRegistry(t *testing.T) {
	reg := doctor.NewRegistry()
	Register(reg)
	for _, id := range reg.SortedIDs() {
		if strings.HasPrefix(id, "vm.") {
			t.Errorf("%s is registered in the live doctor", id)
		}
	}
}
