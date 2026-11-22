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
    runs-on: ${{ fromJSON(vars.RUNS_ON_LINUX) }}
    steps:
      - run: go test ./...
  e2e:
    runs-on: ${{ fromJSON(vars.RUNS_ON_LINUX_LARGE || '["self-hosted","Linux"]') }}  # big one
    steps:
      - run: pnpm e2e
  win:
    runs-on: "${{ fromJSON(vars.RUNS_ON_WINDOWS) }}"
    steps: []
  hosted:
    runs-on: ubuntu-latest
    steps: []
  literal:
    runs-on: [self-hosted, Linux]
    steps: []
  done:
    runs-on: ${{ fromJSON(vars.CRSP_DONE || vars.RUNS_ON_LINUX || '"ubuntu-latest"') }}
    steps: []
  multi:
    runs-on:
      ${{ fromJSON(vars.RUNS_ON_MACOS) }}
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
		"runs-on: ${{ fromJSON(vars.CRSP_UNIT || vars.RUNS_ON_LINUX || '\"ubuntu-latest\"') }}",
		"runs-on: ${{ fromJSON(vars.CRSP_E2E || vars.RUNS_ON_LINUX_LARGE || '[\"self-hosted\",\"Linux\"]') }}  # big one",
		"runs-on: ${{ fromJSON(vars.CRSP_WIN || vars.RUNS_ON_WINDOWS || '\"windows-latest\"') }}",
		"runs-on: ubuntu-latest", "runs-on: [self-hosted, Linux]", "vars.CRSP_DONE || vars.RUNS_ON_LINUX ||",
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
	for _, want := range []string{"diff --git a/w.yml b/w.yml", "--- a/w.yml", "+++ b/w.yml", "@@ -", "-    runs-on: ${{ fromJSON(vars.RUNS_ON_LINUX) }}", "+    runs-on: ${{ fromJSON(vars.CRSP_UNIT"} {
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
