package repos

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type sizes struct{ ms []MachineSize }

func (s *sizes) MachineSizes(context.Context) ([]MachineSize, error) { return s.ms, nil }

func sizedEnv(t *testing.T, ms ...MachineSize) *env {
	t.Helper()
	e := newEnv(t)
	e.cap.m = map[string][]string{"Linux": {"X64", "crsp-big", "crsp-small"}}
	e.svc.cfg.Sizes = &sizes{ms: ms}
	return e
}

func labelsOf(t *testing.T, v string) []string {
	t.Helper()
	var l []string
	if err := json.Unmarshal([]byte(v), &l); err != nil {
		t.Fatal(err)
	}
	return l
}

// R5.67: a machine too small is left out of the value; the plain variable stays.
func TestSizeVariableListsOnlyMachinesWithRoom(t *testing.T) {
	e := sizedEnv(t,
		MachineSize{OS: "linux", Labels: []string{"crsp-small"}, CPUs: 4, MemoryGiB: 8},
		MachineSize{OS: "linux", Labels: []string{"crsp-big"}, CPUs: 16, MemoryGiB: 32})
	got, err := e.svc.Desired(context.Background(), DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if got["CRSP_RUNS_ON_LINUX"] != `["self-hosted","linux","x64","crsp-big","crsp-small"]` {
		t.Fatalf("plain variable changed: %v", got)
	}
	if v := got["CRSP_RUNS_ON_LINUX_LARGE"]; strings.Contains(v, "crsp-small") || !strings.Contains(v, "crsp-big") {
		t.Fatalf("large must list only the big machine: %s", v)
	}
	if v := got["CRSP_RUNS_ON_LINUX_MEDIUM"]; !strings.Contains(v, "crsp-small") || !strings.Contains(v, "crsp-big") {
		t.Fatalf("medium fits both: %s", v)
	}
	if len(labelsOf(t, got["CRSP_RUNS_ON_LINUX_XL"])) == 0 {
		t.Fatal("xl is served by the big machine")
	}
}

// R5.67: a size no machine can serve is not written, and the doctor says so.
func TestUnservableSizeIsNotWritten(t *testing.T) {
	e := sizedEnv(t, MachineSize{OS: "linux", Labels: []string{"crsp-small"}, CPUs: 4, MemoryGiB: 8})
	ctx := context.Background()
	got, _ := e.svc.Desired(ctx, DefaultPolicy())
	if _, ok := got["CRSP_RUNS_ON_LINUX_LARGE"]; ok {
		t.Fatalf("large must not be written: %v", got)
	}
	if _, ok := got["CRSP_RUNS_ON_LINUX_MEDIUM"]; !ok {
		t.Fatalf("medium is served: %v", got)
	}
	un, err := e.svc.UnservedSizes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(un) != 2 || !strings.HasPrefix(un[0], "CRSP_RUNS_ON_LINUX_LARGE: no machine has 8 CPUs and 16 GiB") {
		t.Fatalf("%v", un)
	}
	// A workflow that reads it gets a finding with the reason.
	jobs := ParseRunsOn([]byte("jobs:\n  a:\n    runs-on: ${{ fromJSON(vars.CRSP_RUNS_ON_LINUX_LARGE) }}\n"))
	f := Check("w.yml", jobs, map[string]string{}, nil)
	if len(f) != 1 || !strings.Contains(f[0].Text, "no machine has that much room") {
		t.Fatalf("%+v", f)
	}
}

func TestSizeLabelMapsToClass(t *testing.T) {
	for in, want := range map[string]string{"small": "small", "Large": "large", " XL ": "xl", "medium": "medium"} {
		c, err := SizeClassForLabel(in)
		if err != nil || string(c) != want {
			t.Fatalf("%q: %q %v", in, c, err)
		}
	}
	if os, size, ok := ParseSizeVariable("CRSP_RUNS_ON_MACOS_LARGE"); !ok || os != "macos" || size != "large" {
		t.Fatal("parse")
	}
	if _, _, ok := ParseSizeVariable("CRSP_RUNS_ON_LINUX"); ok {
		t.Fatal("the plain variable is not a size variable")
	}
}

func TestUnknownSizeLabelIsRefusedClearly(t *testing.T) {
	_, err := SizeClassForLabel("huge")
	if err == nil || !strings.Contains(err.Error(), `size label "huge" is not known; use one of small, medium, large, xl`) {
		t.Fatalf("%v", err)
	}
	if err := CheckSizeFloors(map[string]SizeFloor{"Big!": {CPUs: 4, MemoryGiB: 8}}); err == nil || !strings.Contains(err.Error(), `"Big!"`) {
		t.Fatalf("%v", err)
	}
	if err := CheckSizeFloors(map[string]SizeFloor{"big": {CPUs: 0, MemoryGiB: 8}}); err == nil {
		t.Fatal("a floor needs a CPU")
	}
	jobs := ParseRunsOn([]byte("jobs:\n  a:\n    runs-on: ${{ fromJSON(vars.CRSP_RUNS_ON_LINUX_HUGE) }}\n"))
	f := Check("w.yml", jobs, map[string]string{}, nil)
	if len(f) != 1 || !strings.Contains(f[0].Text, `size "huge" is not one of small, medium, large, xl`) {
		t.Fatalf("%+v", f)
	}
}

// A stale size variable is deleted only when the controller wrote it.
func TestSizeVariablesSyncAndOwnerVariableIsKept(t *testing.T) {
	e := sizedEnv(t, MachineSize{OS: "linux", Labels: []string{"crsp-big"}, CPUs: 8, MemoryGiB: 16})
	e.world()
	e.turnOn(101)
	ctx := context.Background()
	if v, ok := e.fake.Variable("alice/secret", "CRSP_RUNS_ON_LINUX_LARGE"); !ok || !strings.Contains(v, "crsp-big") {
		t.Fatalf("size variable written: %q %v", v, ok)
	}
	if _, ok := e.fake.Variable("alice/secret", "CRSP_RUNS_ON_LINUX_XL"); ok {
		t.Fatal("xl is not served")
	}
	e.fake.SetVariable("alice/secret", "CRSP_RUNS_ON_LINUX_MINE", "x") // owner's own, not ours
	e.svc.cfg.Sizes = &sizes{}                                         // the machine is gone
	if err := e.svc.ReconcileVariables(ctx, 101); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.fake.Variable("alice/secret", "CRSP_RUNS_ON_LINUX_LARGE"); ok {
		t.Fatal("stale size variable deleted")
	}
	if _, ok := e.fake.Variable("alice/secret", "CRSP_RUNS_ON_LINUX_MINE"); !ok {
		t.Fatal("the owner's variable stays")
	}
	if _, ok := e.fake.Variable("alice/secret", "CRSP_RUNS_ON_LINUX"); !ok {
		t.Fatal("plain variable stays")
	}
}
