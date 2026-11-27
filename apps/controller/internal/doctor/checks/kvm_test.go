package checks

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

type kvmFake struct {
	states []doctor.KVMState
	err    error
}

func (k kvmFake) KVM(context.Context) ([]doctor.KVMState, error) { return k.states, k.err }

// kvmRun runs one KVM check and returns its findings in the order of the
// states given (the run sorts findings, so they are matched by machine ID and
// position among equal IDs).
func kvmRun(t *testing.T, src doctor.KVMSource, check string) []doctor.Finding {
	t.Helper()
	reg := doctor.NewRegistry()
	RegisterKVM(reg)
	e := &doctor.Engine{Registry: reg, Store: doctor.NewMemStore(), Now: func() time.Time { return now },
		Env: func(context.Context) (doctor.Env, error) { return doctor.Env{KVM: src}, nil }}
	r, err := e.Run(context.Background(), doctor.Scope{Checks: []string{check}}, doctor.TriggerCLI, "test")
	if err != nil {
		t.Fatal(err)
	}
	return r.Findings
}

// byName distinguishes machines in a test: each state gets its own name.
func named(m doctor.Machine, name string) doctor.Machine { m.ID, m.Name = name, name; return m }

func find(t *testing.T, fs []doctor.Finding, name string) doctor.Finding {
	t.Helper()
	for _, f := range fs {
		if f.Subject.Name == name {
			return f
		}
	}
	t.Fatalf("no finding for %s in %+v", name, fs)
	return doctor.Finding{}
}

func TestRegisterKVMIsSeparate(t *testing.T) {
	reg := doctor.NewRegistry()
	RegisterKVM(reg)
	if len(reg.All()) != 5 {
		t.Errorf("checks %v", reg.SortedIDs())
	}
}

func win() doctor.Machine   { return doctor.Machine{ID: "w1", Name: "winbox", OS: "windows"} }
func linux() doctor.Machine { return doctor.Machine{ID: "l1", Name: "linbox", OS: "linux"} }

// TestKvmHostFailOnlyWhenOn: doctor R5.32.
func TestKvmHostFailOnlyWhenOn(t *testing.T) {
	fs := kvmRun(t, kvmFake{states: []doctor.KVMState{
		{Machine: named(linux(), "has"), HostDevice: true},
		{Machine: named(linux(), "lacks"), HostDevice: false},
		{Machine: named(win(), "waited"), HostDevice: false, WaitedOut: true},
		{Machine: named(win(), "loaded"), HostDevice: false, LoaderRunning: true},
	}}, "kvm.host")
	if len(fs) != 4 {
		t.Fatalf("%d findings", len(fs))
	}
	if find(t, fs, "has").Severity != doctor.OK || find(t, fs, "lacks").Severity != doctor.Fail {
		t.Errorf("severities %+v", fs)
	}
	w := find(t, fs, "waited")
	if !strings.Contains(w.Detail, "started without KVM") || !strings.Contains(w.Detail, "loader service is not running") ||
		!strings.Contains(w.Fix.Command, "compose.kvm.yaml") {
		t.Errorf("waited out: %+v", w)
	}
	if d := find(t, fs, "loaded").Detail; strings.Contains(d, "loader service") {
		t.Errorf("loader running but reported missing: %s", d)
	}
	// No KVM source: skip, never fail.
	got := kvmRun(t, nil, "kvm.host")
	if len(got) != 1 || got[0].Severity != doctor.Skip {
		t.Errorf("no source: %+v", got)
	}
	got = kvmRun(t, kvmFake{err: doctor.ErrUnreachable}, "kvm.host")
	if len(got) != 1 || got[0].Severity != doctor.Skip {
		t.Errorf("unreachable: %+v", got)
	}
}

func TestKvmContainerAndLabel(t *testing.T) {
	sts := []doctor.KVMState{
		{Machine: named(linux(), "good"), HostDevice: true, ContainerUsable: true, LabelPresent: true},
		{Machine: named(linux(), "stale-label"), HostDevice: true, ContainerUsable: false, LabelPresent: true},
		{Machine: named(linux(), "no-label"), HostDevice: true, ContainerUsable: true, LabelPresent: false},
		{Machine: named(linux(), "no-host"), HostDevice: false, LabelPresent: false},
	}
	c := kvmRun(t, kvmFake{states: sts}, "kvm.container")
	for name, want := range map[string]doctor.Severity{"good": doctor.OK, "stale-label": doctor.Fail, "no-label": doctor.OK, "no-host": doctor.Skip} {
		if got := find(t, c, name).Severity; got != want {
			t.Errorf("container %s: %v, want %v", name, got, want)
		}
	}
	l := kvmRun(t, kvmFake{states: sts}, "kvm.label")
	for name, want := range map[string]doctor.Severity{"good": doctor.OK, "stale-label": doctor.Fail, "no-label": doctor.Warn, "no-host": doctor.OK} {
		if got := find(t, l, name).Severity; got != want {
			t.Errorf("label %s: %v, want %v", name, got, want)
		}
	}
	if !strings.Contains(find(t, l, "stale-label").Fix.Text, "Remove the kvm label") || !strings.Contains(find(t, l, "no-label").Fix.Text, "Add the kvm label") {
		t.Errorf("label fixes: %+v", l)
	}
}

// TestWslconfigNested: doctor R5.32, machines R5.53.
func TestWslconfigNested(t *testing.T) {
	fs := kvmRun(t, kvmFake{states: []doctor.KVMState{
		{Machine: named(win(), "on"), NestedKnown: true, Nested: true},
		{Machine: named(win(), "off"), NestedKnown: true, Nested: false},
		{Machine: named(win(), "unread"), NestedKnown: false},
		{Machine: named(linux(), "linux"), NestedKnown: true, Nested: false},
	}}, "kvm.wslconfig")
	if len(fs) != 3 {
		t.Fatalf("Linux machine must not be checked: %d findings", len(fs))
	}
	if find(t, fs, "on").Severity != doctor.OK || find(t, fs, "off").Severity != doctor.Fail || find(t, fs, "unread").Severity != doctor.Skip {
		t.Errorf("severities %+v", fs)
	}
	for _, want := range []string{"[wsl2]", "nestedVirtualization=true", "wsl --shutdown"} {
		if !strings.Contains(find(t, fs, "off").Fix.Text, want) {
			t.Errorf("fix lacks %q", want)
		}
	}
}

// TestKvmModuleLoadedInTheWSLVM: machines R5.53. The doctor says
// how to fix it and never loads the module.
func TestKvmModuleLoadedInTheWSLVM(t *testing.T) {
	fs := kvmRun(t, kvmFake{states: []doctor.KVMState{
		{Machine: named(win(), "loaded"), ModuleKnown: true, ModuleLoaded: true, NestedKnown: true, Nested: true},
		{Machine: named(win(), "gone"), ModuleKnown: true, NestedKnown: true, Nested: true},
		{Machine: named(win(), "gone-unset"), ModuleKnown: true},
		{Machine: named(win(), "nested-off"), ModuleKnown: true, NestedKnown: true, Nested: false},
		{Machine: named(win(), "unread"), ModuleKnown: false},
		{Machine: named(linux(), "linux"), ModuleKnown: true},
	}}, "kvm.module")
	if len(fs) != 5 {
		t.Fatalf("Linux machine must not be checked: %d findings", len(fs))
	}
	want := map[string]doctor.Severity{"loaded": doctor.OK, "gone": doctor.Fail, "gone-unset": doctor.Fail, "nested-off": doctor.Info, "unread": doctor.Skip}
	for name, sev := range want {
		if got := find(t, fs, name).Severity; got != sev {
			t.Errorf("%s: %v, want %v", name, got, sev)
		}
	}
	g := find(t, fs, "gone")
	if !strings.Contains(g.Fix.Text, "modprobe kvm_intel") || !strings.Contains(g.Fix.Text, "kvm_amd") || !strings.Contains(g.Fix.Command, "compose.kvm.yaml") || !g.NeedsAdmin {
		t.Errorf("fix: %+v", g)
	}
	if !strings.Contains(find(t, fs, "nested-off").Fix.Text, "kvm.wslconfig") {
		t.Errorf("nested off must point to kvm.wslconfig: %+v", find(t, fs, "nested-off"))
	}
}
