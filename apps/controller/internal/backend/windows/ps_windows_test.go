//go:build windows

package windows

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

// listenerScriptRun runs a script of the Runner.Listener / Runner.Worker list
// with a real process named like it, started from the folder's bin.
func startNamedProcess(t *testing.T, name string) (folder string, pid int) {
	t.Helper()
	folder = t.TempDir()
	bin := filepath.Join(folder, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(os.Getenv("SystemRoot"), "System32", "ping.exe")
	b, err := os.ReadFile(src)
	if err != nil {
		t.Skip("no ping.exe to copy")
	}
	exe := filepath.Join(bin, name+".exe")
	if err := os.WriteFile(exe, b, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), exe, "-n", "30", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return folder, cmd.Process.Pid
}

// CRSP382: the listener list gives the id of a listener of the folder, nothing
// for another folder or for no listener, and always exits 0.
func TestRealListenersScriptFindsOnlyTheFoldersListener(t *testing.T) {
	folder, pid := startNamedProcess(t, "Runner.Listener")
	out, errOut, code := runPS(t, scriptListeners, map[string]string{"CRSP_FOLDER": folder})
	if code != 0 || strings.TrimSpace(out) != strconv.Itoa(pid) {
		t.Fatalf("exit %d, stdout %q, stderr %q, want pid %d", code, out, errOut, pid)
	}
	out, errOut, code = runPS(t, scriptListeners, map[string]string{"CRSP_FOLDER": t.TempDir()})
	if code != 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("another folder: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

// CRSP382: a process whose Path cannot be read (it throws, as it does for a
// process of another account or of another bitness) is not a failure of the
// list; Windows is asked for its path another way.
func TestRealListenersScriptSurvivesAProcessPathThatThrows(t *testing.T) {
	folder, pid := startNamedProcess(t, "Runner.Listener")
	prefix := "function Get-Process {\n" +
		"  $o = New-Object psobject -Property @{ Id = " + strconv.Itoa(pid) + " }\n" +
		"  $o | Add-Member -MemberType ScriptProperty -Name Path -Value { throw 'Access is denied' }\n" +
		"  $o\n}\n"
	for name, script := range map[string]string{"listeners": scriptListeners, "workers": scriptWorkers} {
		out, errOut, code := runPS(t, prefix+script, map[string]string{"CRSP_FOLDER": folder})
		if code != 0 {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", name, code, out, errOut)
		}
		if name == "listeners" && strings.TrimSpace(out) != strconv.Itoa(pid) {
			t.Errorf("listeners: stdout %q, want pid %d", out, pid)
		}
	}
}

// CRSP382: a folder on a drive that is not there, or no folder at all, has no
// listener; it is not an error (Join-Path used to throw for both).
func TestRealListenersScriptNoListenerIsNotAnError(t *testing.T) {
	for _, folder := range []string{`Q:\crsp\runners\absent`, "", `C:\crsp-382-absent\`} {
		for name, script := range map[string]string{"listeners": scriptListeners, "workers": scriptWorkers} {
			out, errOut, code := runPS(t, script, map[string]string{"CRSP_FOLDER": folder})
			if code != 0 || strings.TrimSpace(out) != "" {
				t.Errorf("%s of %q: exit %d, stdout %q, stderr %q", name, folder, code, out, errOut)
			}
		}
	}
}

// CRSP385: the cause of win-1's "list listeners: powershell exited 1" on the
// owner's PC (crsp 0.1.28). With no process of the name at all, the old script
// ended on a Get-Process that left $? false, and `powershell -Command -` exits
// 1 then, with nothing on stderr. The script of this release exits 0 for the
// same machine. The process name is one no machine runs, so the test does not
// depend on a runner running here.
func TestRealListenersScriptExitsZeroWhenNoProcessHasTheName(t *testing.T) {
	const none = "Runner.Listener-crsp385-none"
	old := scriptHeader + "$want = (Join-Path (Join-Path $env:CRSP_FOLDER 'bin') 'Runner.Listener') + '.exe'\n" +
		"Get-Process -Name '" + none + "' -ErrorAction SilentlyContinue |\n" +
		"    Where-Object { $_.Path -and ($_.Path -ieq $want) } |\n" +
		"    ForEach-Object { $_.Id }\n"
	folder := t.TempDir()
	if _, _, code := runPS(t, old, map[string]string{"CRSP_FOLDER": folder}); code != 1 {
		t.Fatalf("the 0.1.28 script should reproduce exit 1, got %d", code)
	}
	for name, script := range map[string]string{"listeners": scriptListeners, "workers": scriptWorkers} {
		s := strings.NewReplacer("'Runner.Listener'", "'"+none+"'", "'Runner.Worker'", "'"+none+"'").Replace(script)
		out, errOut, code := runPS(t, s, map[string]string{"CRSP_FOLDER": folder})
		if code != 0 || strings.TrimSpace(out) != "" {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", name, code, out, errOut)
		}
	}
}
