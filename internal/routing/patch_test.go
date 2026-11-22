package routing_test

import (
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/routing"
)

const ciYML = `name: ci
on: push
jobs:
  unit:
    runs-on: ${{ fromJSON(vars.CRSP_RUNS_ON_LINUX) }}
    steps:
      - run: go test ./...
  e2e:
    runs-on: ${{ fromJSON(vars.CRSP_RUNS_ON_LINUX_LARGE || '["self-hosted","Linux"]') }}  # big one
    steps:
      - run: pnpm e2e
  win:
    runs-on: "${{ fromJSON(vars.CRSP_RUNS_ON_WINDOWS) }}"
    steps: []
  hosted:
    runs-on: ubuntu-latest
    steps: []
  literal:
    runs-on: [self-hosted, Linux]
    steps: []
  done:
    runs-on: ${{ fromJSON(vars.CRSP_DONE || vars.CRSP_RUNS_ON_LINUX || '"ubuntu-latest"') }}
    steps: []
  multi:
    runs-on:
      ${{ fromJSON(vars.CRSP_RUNS_ON_MACOS) }}
    steps: []
`

// R5.107: every routed runs-on becomes the per-job form; the rest stays.
func TestMakePatchRewritesRoutedRunsOn(t *testing.T) {
	files := map[string][]byte{".github/workflows/ci.yml": []byte(ciYML)}
	p := routing.MakePatch(files)
	if len(p.Files) != 1 || len(p.Files[0].Changes) != 3 {
		t.Fatalf("patch %+v", p)
	}
	got := string(p.Files[0].New)
	for _, want := range []string{
		"runs-on: ${{ fromJSON(vars.CRSP_UNIT || vars.CRSP_RUNS_ON_LINUX || '\"ubuntu-latest\"') }}",
		"runs-on: ${{ fromJSON(vars.CRSP_E2E || vars.CRSP_RUNS_ON_LINUX_LARGE || '[\"self-hosted\",\"Linux\"]') }}  # big one",
		"runs-on: ${{ fromJSON(vars.CRSP_WIN || vars.CRSP_RUNS_ON_WINDOWS || '\"windows-latest\"') }}",
		"runs-on: ubuntu-latest", "runs-on: [self-hosted, Linux]", "vars.CRSP_DONE || vars.CRSP_RUNS_ON_LINUX ||",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if len(p.Skipped) != 1 || !strings.HasSuffix(p.Skipped[0], ": multi") {
		t.Fatalf("skipped %v", p.Skipped)
	}
	// The result is stable: patching the patched file changes nothing.
	again := routing.MakePatch(map[string][]byte{".github/workflows/ci.yml": p.Files[0].New})
	if !again.Empty() {
		t.Fatalf("second patch not empty: %+v", again)
	}
}

// R5.108: the Doctor shows the patch as a unified diff to copy or download.
func TestPatchDiff(t *testing.T) {
	p := routing.MakePatch(map[string][]byte{"w.yml": []byte(ciYML)})
	d := p.Diff()
	for _, want := range []string{"diff --git a/w.yml b/w.yml", "--- a/w.yml", "+++ b/w.yml", "@@ -", "-    runs-on: ${{ fromJSON(vars.CRSP_RUNS_ON_LINUX) }}", "+    runs-on: ${{ fromJSON(vars.CRSP_UNIT"} {
		if !strings.Contains(d, want) {
			t.Errorf("diff misses %q:\n%s", want, d)
		}
	}
	if (routing.Patch{}).Diff() != "" || !(routing.Patch{}).Empty() {
		t.Fatal("empty patch")
	}
}

// R5.107: a file that does not parse, or has no routed job, gives no patch.
func TestPatchIgnoresBrokenAndUnroutedFiles(t *testing.T) {
	p := routing.MakePatch(map[string][]byte{"a.yml": []byte("jobs: [unterminated"), "b.yml": []byte("jobs:\n  x:\n    runs-on: ubuntu-latest\n")})
	if !p.Empty() {
		t.Fatalf("patch %+v", p)
	}
}

// CRSP229: a workflow that still reads RUNS_ON_<OS> or RUNS_ON_<OS>_<SIZE>,
// the names older versions wrote, is routed too, and the patch writes the new
// CRSP_RUNS_ON_* name, also where the per-job form already falls back to the
// old one.
func TestMakePatchWritesTheNewPoolNames(t *testing.T) {
	const old = `jobs:
  unit:
    runs-on: ${{ fromJSON(vars.RUNS_ON_LINUX) }}
  big:
    runs-on: ${{ fromJSON(vars.RUNS_ON_WINDOWS_LARGE) }}
  half:
    runs-on: ${{ fromJSON(vars.CRSP_HALF || vars.RUNS_ON_MACOS || '"macos-latest"') }}
  ci:
    runs-on: ${{ fromJSON(vars.CI_RUNS_ON) }}
`
	files := map[string][]byte{"ci.yml": []byte(old)}
	routed := routing.Routed(files)
	if len(routed) != 3 {
		t.Fatalf("routed %+v", routed)
	}
	for _, r := range routed {
		if !r.OldBase || !routing.IsPoolVar(r.Base) {
			t.Fatalf("job %+v", r)
		}
	}
	p := routing.MakePatch(files)
	if len(p.Files) != 1 || len(p.Files[0].Changes) != 3 {
		t.Fatalf("patch %+v", p)
	}
	got := string(p.Files[0].New)
	for _, want := range []string{
		"runs-on: ${{ fromJSON(vars.CRSP_UNIT || vars.CRSP_RUNS_ON_LINUX || '\"ubuntu-latest\"') }}",
		"runs-on: ${{ fromJSON(vars.CRSP_BIG || vars.CRSP_RUNS_ON_WINDOWS_LARGE || '\"windows-latest\"') }}",
		"runs-on: ${{ fromJSON(vars.CRSP_HALF || vars.CRSP_RUNS_ON_MACOS || '[\"macos-latest\"]') }}",
		"runs-on: ${{ fromJSON(vars.CI_RUNS_ON) }}",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "vars.RUNS_ON_") {
		t.Errorf("an old name is left:\n%s", got)
	}
	if again := routing.MakePatch(map[string][]byte{"ci.yml": p.Files[0].New}); !again.Empty() {
		t.Fatalf("second patch not empty: %+v", again)
	}
}
