//go:build windows

package windows

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
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
// needs no admin rights and has no side effects. One process for all scripts
// keeps the test inside its time limit.
func TestRealPowerShellParsesEveryScript(t *testing.T) {
	dir := t.TempDir()
	for name, script := range allScripts() {
		if err := os.WriteFile(filepath.Join(dir, name+".ps1"), []byte(wrapScript(script)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out, errOut, code := runPS(t, `$bad = 0
foreach ($f in Get-ChildItem -LiteralPath $env:CRSP_TEST_DIR -Filter *.ps1) {
    $errs = $null; $tok = $null
    [void][System.Management.Automation.Language.Parser]::ParseFile($f.FullName, [ref]$tok, [ref]$errs)
    foreach ($e in $errs) { $bad++; Write-Output ($f.Name + ' line ' + $e.Extent.StartLineNumber + ': ' + $e.Message) }
}
Write-Output ('errors=' + $bad)`+"\n", map[string]string{"CRSP_TEST_DIR": dir})
	if code != 0 || !strings.Contains(out, "errors=0") {
		t.Errorf("exit %d, parser said %q %q", code, out, errOut)
	}
}

// OPS93: scripts that only read run for real, with no admin right and no side
// effect, against a folder, service and account that do not exist.
func TestRealPowerShellReadOnlyScriptsRun(t *testing.T) {
	env := map[string]string{"CRSP_FOLDER": t.TempDir(), "CRSP_SERVICE": "crsp-test-absent", "CRSP_ACCOUNT": "crsp-test-absent"}
	want := map[string]string{"detect": "admin=", "find": "[", "usage": "cpu_ms=0;mem=0", "removeAccount": "account-absent", "listeners": "", "workers": "", "tail": ""}
	dir := t.TempDir()
	env["CRSP_TEST_DIR"] = dir
	for name := range want {
		if err := os.WriteFile(filepath.Join(dir, name+".ps1"), []byte(allScripts()[name]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out, errOut, code := runPS(t, `foreach ($f in Get-ChildItem -LiteralPath $env:CRSP_TEST_DIR -Filter *.ps1) {
    $r = & ([scriptblock]::Create((Get-Content -Raw -LiteralPath $f.FullName))) | Out-String
    Write-Output ('== ' + $f.BaseName + ' ' + $r)
}`+"\n", env)
	if code != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	for name, marker := range want {
		if !strings.Contains(out, "== "+name+" ") || !strings.Contains(out, marker) {
			t.Errorf("%s: missing %q in %q", name, marker, out)
		}
	}
}
