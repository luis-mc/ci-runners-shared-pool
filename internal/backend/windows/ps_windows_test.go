//go:build windows

package windows

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func runPS(t *testing.T, script string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "-")
	cmd.Stdin = strings.NewReader(wrapScript(script))
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	return o.String(), e.String(), code
}

// OPS92: the real powershell.exe must run the wrapped script from stdin.
func TestRealPowerShellRunsWrappedScript(t *testing.T) {
	out, _, code := runPS(t, "Write-Output 'ready x'")
	if code != 0 || !strings.Contains(out, "ready x") {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
	// a script with a blank line inside still runs whole
	out, _, code = runPS(t, "$ErrorActionPreference = 'Stop'\n\nif ($true) {\n\n    Write-Output 'inner ok'\n}\n")
	if code != 0 || !strings.Contains(out, "inner ok") {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
}

func TestRealPowerShellFailureGivesMarker(t *testing.T) {
	_, errOut, code := runPS(t, "throw 'boom'")
	if code != 1 || !strings.Contains(errOut, errMarker+"boom") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}
