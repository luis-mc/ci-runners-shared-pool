package checks

import (
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

const covWorkflow = `name: ci
jobs:
  win-test:
    runs-on: ${{ fromJSON(vars.CRSP_RUNS_ON_WINDOWS) }}
    steps:
      - run: cargo test
  win-old-name:
    runs-on: ${{ fromJSON(vars.RUNS_ON_WINDOWS) }}
    steps:
      - run: cargo build
  lint:
    runs-on: ${{ fromJSON(vars.CRSP_RUNS_ON_LINUX) }}
    steps:
      - run: pnpm lint
  e2e:
    runs-on: [self-hosted, linux]
    steps:
      - run: pnpm exec playwright test
  xwin:
    runs-on: [self-hosted, linux]
    steps:
      - run: cargo xwin build
      - run: wine installer.exe
  vm:
    runs-on: [self-hosted, linux, kvm]
    steps:
      - run: crsp vm selinux
  hosted:
    runs-on: ubuntu-latest
    steps:
      - run: playwright install
  matrixed:
    runs-on: ${{ matrix.os }}
    steps:
      - run: echo
`

func needsOf(js []JobNeeds, job string) JobNeeds {
	for _, j := range js {
		if j.Job == job {
			return j
		}
	}
	return JobNeeds{Job: "NOT FOUND"}
}

// R5.33: needs come from the job text, per job.
func TestCapabilitiesFromWorkflowText(t *testing.T) {
	js, err := CapabilitiesFromWorkflow(".github/workflows/ci.yml", []byte(covWorkflow), nil)
	if err != nil {
		t.Fatal(err)
	}
	if j := needsOf(js, "win-test"); j.OS != "windows" || len(j.Missing) != 0 {
		t.Errorf("windows is covered: %+v", j)
	}
	if j := needsOf(js, "win-old-name"); j.OS != "windows" || len(j.Missing) != 0 {
		t.Errorf("old variable name is read: %+v", j)
	}
	// CI_RUNS_ON and CI_RUNS_ON_WINDOWS are not crsp's names (R5.39 removed, CRSP229).
	if osOfVariable("CI_RUNS_ON") != "" || osOfVariable("CI_RUNS_ON_WINDOWS") != "" {
		t.Error("the old dev-standards names are read as crsp's")
	}
	if j := needsOf(js, "lint"); j.OS != "linux" || strings.Join(j.Missing, ",") != "linux-container (v0.3.0)" {
		t.Errorf("lint: %+v", j)
	}
	if j := needsOf(js, "e2e"); !strings.Contains(strings.Join(j.Missing, ","), "browsers (v0.4.0)") {
		t.Errorf("e2e: %+v", j)
	}
	x := strings.Join(needsOf(js, "xwin").Missing, ",")
	if !strings.Contains(x, "llvm (v0.5.0)") || !strings.Contains(x, "windows-packaging (v0.5.0)") {
		t.Errorf("xwin: %s", x)
	}
	if !strings.Contains(strings.Join(needsOf(js, "vm").Missing, ","), "kvm (v0.6.0)") {
		t.Errorf("vm: %+v", needsOf(js, "vm"))
	}
	// A hosted job needs nothing from the pool, and a word in another job's text does not leak.
	if j := needsOf(js, "hosted"); j.Job != "NOT FOUND" {
		t.Errorf("hosted job listed: %+v", j)
	}
	if strings.Contains(strings.Join(needsOf(js, "lint").Needs, ","), "browsers") {
		t.Error("playwright of another job leaked into lint")
	}
}

// R5.33: a job the doctor cannot classify is info "not checked", never ok.
func TestUnknownJobIsInfo(t *testing.T) {
	js, _ := CapabilitiesFromWorkflow("w.yml", []byte(covWorkflow), nil)
	if j := needsOf(js, "matrixed"); !j.Unknown {
		t.Fatalf("matrix runs-on is unknown: %+v", j)
	}
	g := newGH()
	g.vars["o/r"] = []github.Variable{{Name: "CRSP_RUNS_ON_WINDOWS", Value: `["self-hosted","windows"]`}}
	g.files["o/r"] = []github.WorkflowFile{{Path: ".github/workflows/ci.yml", Content: []byte(covWorkflow)}}
	env := doctor.Env{GitHub: g, Fleet: &fleet{repos: []doctor.Repository{repo(1, "o/r", nil)}}}
	fs := run(t, env, "repo.coverage")
	byTitle := map[string]doctor.Finding{}
	for _, f := range fs {
		byTitle[f.Subject.Name+"|"+f.Title] = f
	}
	if f := byTitle["o/r (Windows)|covered"]; f.Severity != doctor.OK {
		t.Errorf("windows row: %+v", fs)
	}
	var sawLinux, sawUnchecked bool
	for _, f := range fs {
		if strings.HasPrefix(f.Title, "missing: ") && f.Severity == doctor.Info && strings.Contains(f.Subject.Name, "Linux") {
			sawLinux = true
		}
		if f.Title == "not checked" && f.Severity == doctor.Info && strings.Contains(f.Detail, "matrixed") {
			sawUnchecked = true
		}
	}
	if !sawLinux || !sawUnchecked {
		t.Errorf("rows: %+v", fs)
	}
}
