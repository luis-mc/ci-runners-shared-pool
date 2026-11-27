package runnercache

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJobsReadOnlyTheTwoVariables(t *testing.T) {
	got := map[string]bool{}
	for k := range LaneVars(LaneDirs{Tools: "/a", Rust: "/b", ToolCache: "/c"}) {
		got[k] = true
	}
	for _, k := range JobCacheVariables {
		if !got[k] {
			t.Errorf("%s missing", k)
		}
		delete(got, k)
	}
	if len(got) != 0 {
		t.Errorf("a lane exports more than the named variables: %v", got)
	}
	// The variables a job reads for tools and the Rust cache are the two of runners R5.53.
	if EnvToolsDir != "CI_TOOLS_DIR" || EnvRustDir != "CARGO_TARGET_DIR" {
		t.Error("variable names changed")
	}
}

// crsp never reads tools.json (D308): no Go source outside tests names it in code.
func TestNoGoCodeReadsToolsJSON(t *testing.T) {
	root := filepath.Join("..", "..") // apps/controller
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		for i, line := range strings.Split(string(b), "\n") {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "//") {
				continue
			}
			if strings.Contains(line, `"tools.json"`) || strings.Contains(line, "tools.json\"") {
				return &scanErr{p, i + 1}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

type scanErr struct {
	path string
	line int
}

func (e *scanErr) Error() string { return e.path + ": reads tools.json (D308)" }
