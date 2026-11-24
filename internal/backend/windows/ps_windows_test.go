//go:build windows

package windows

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func runPS(t *testing.T, script string, env ...map[string]string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "-")
	cmd.Stdin = strings.NewReader(wrapScript(script))
	cmd.Env = cmd.Environ()
	for _, m := range env {
		for k, v := range m {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
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

// OPS93: every script must parse in the real Windows PowerShell. Parsing
// needs no admin rights and has no side effects.
func TestRealPowerShellParsesEveryScript(t *testing.T) {
	for name, script := range allScripts() {
		out, errOut, code := runPS(t, `$errs = $null; $tok = $null
[void][System.Management.Automation.Language.Parser]::ParseInput($env:CRSP_TEST_SCRIPT, [ref]$tok, [ref]$errs)
foreach ($e in $errs) { Write-Output ($e.Extent.StartLineNumber.ToString() + ': ' + $e.Message) }
Write-Output ('errors=' + @($errs).Count)`+"\n", map[string]string{"CRSP_TEST_SCRIPT": wrapScript(script)})
		if code != 0 || !strings.Contains(out, "errors=0") {
			t.Errorf("%s: exit %d, parser said %q %q", name, code, out, errOut)
		}
	}
}

// OPS93: scripts that only read run for real, with no admin right and no side
// effect, against a folder, service and account that do not exist.
func TestRealPowerShellReadOnlyScriptsRun(t *testing.T) {
	env := map[string]string{"CRSP_FOLDER": t.TempDir(), "CRSP_SERVICE": "crsp-test-absent", "CRSP_ACCOUNT": "crsp-test-absent", "CRSP_PIDS": ""}
	want := map[string]string{"detect": "admin=", "find": "[", "usage": "cpu_ms=0;mem=0", "removeAccount": "account-absent", "listeners": "", "workers": "", "tail": "", "endListeners": ""}
	for name, marker := range want {
		out, errOut, code := runPS(t, allScripts()[name], env)
		if code != 0 || !strings.Contains(out, marker) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", name, code, out, errOut)
		}
	}
}
