package load

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// R5.21 (DL-77): the package only measures. It has no way to kill, signal,
// suspend or lower the priority of a process.
func TestMeasureOnly(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	banned := []string{"syscall.Kill", ".Signal(", ".Kill()", "Setpriority", "SetPriorityClass", "SIGSTOP", "SIGKILL", "taskkill", "renice", "os.FindProcess", "Process.Kill"}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, bad := range banned {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s uses %q: this package must only measure", f, bad)
			}
		}
	}
	// The probe can run commands, but only the fixed measurement ones.
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), `"kill"`) || strings.Contains(string(b), `"pkill"`) {
			t.Errorf("%s runs a kill command", f)
		}
	}
}
