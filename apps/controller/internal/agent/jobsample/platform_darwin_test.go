//go:build darwin

package jobsample

import (
	"os/exec"
	"testing"
)

// OPS66 R5.62: macOS has the tree reader over the real ps, and its output parses.
func TestPlatformDarwin(t *testing.T) {
	p := NewPlatform()
	if p.ProcessTree == nil || p.Cgroup != nil || p.JobObject != nil {
		t.Fatalf("%+v", p)
	}
	out, err := exec.Command("ps", PSArgs...).Output()
	if err != nil {
		t.Skip("no ps:", err)
	}
	if len(parsePSTable(out)) == 0 {
		t.Fatalf("ps output did not parse: %q", out)
	}
}
