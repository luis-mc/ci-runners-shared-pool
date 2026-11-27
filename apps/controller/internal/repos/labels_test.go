package repos

import (
	"strings"
	"testing"
)

func TestGenericLabels(t *testing.T) { // R5.25, R5.27
	for _, ok := range []string{"self-hosted", "Linux", "windows", "macOS", "X64", "ARM64", "docker", "kvm", "gpu", "crsp-home", "crsp-lab-2"} {
		if !IsGenericLabel(ok) {
			t.Errorf("%q should be generic", ok)
		}
	}
	for _, bad := range []string{"", "my-project", "dev-standards", "windows-delivery", "crsp-", "crsp_home", "Acme"} {
		if IsGenericLabel(bad) {
			t.Errorf("%q must not be generic", bad)
		}
	}
}

func TestCheckLabelsNamesEach(t *testing.T) { // R5.27
	err := CheckLabels([]string{"docker", "proj-a", "proj-b"})
	if err == nil || !strings.Contains(err.Error(), `"proj-a"`) || !strings.Contains(err.Error(), `"proj-b"`) || strings.Contains(err.Error(), `"docker"`) {
		t.Fatalf("%v", err)
	}
	if CheckLabels([]string{"docker", "kvm"}) != nil {
		t.Fatal("generic labels pass")
	}
}

func TestLeftoverLabelOnlyWhenNoWorkflowNamesIt(t *testing.T) { // R5.27
	if got := NonGeneric([]string{"self-hosted", "Linux", "legacy-proj", "docker"}); len(got) != 1 || got[0] != "legacy-proj" {
		t.Fatalf("%v", got)
	}
	files := map[string][]byte{
		".github/workflows/a.yml": []byte("jobs:\n  a:\n    runs-on: [self-hosted, legacy-proj]\n"),
		".github/workflows/b.yml": []byte("jobs:\n  b:\n    runs-on: ${{ fromJSON(vars.CRSP_RUNS_ON_LINUX) }}\n"),
	}
	if got := WorkflowsNaming("legacy-proj", files); len(got) != 1 || got[0] != ".github/workflows/a.yml" {
		t.Fatalf("a workflow still names it, so the label stays: %v", got)
	}
	if got := WorkflowsNaming("other-label", files); len(got) != 0 {
		t.Fatalf("no workflow names it: the label may go after a yes: %v", got)
	}
}
