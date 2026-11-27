package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const problemFile = "crsp-hook-problem.txt"

// CRSP22: a missing or broken crsp never fails the job, and never hides.
func runSh(t *testing.T, name, crsp string) (string, int, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("sh hooks run on Linux and macOS")
	}
	body, err := Unix(name)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, name+".sh")
	if err := os.WriteFile(script, body, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "/bin/sh", script)
	cmd.Env = append(os.Environ(), "CRSP_BIN="+crsp)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	marker, _ := os.ReadFile(filepath.Join(dir, problemFile))
	return string(out), code, string(marker)
}

func TestShHookWarnsWhenCrspIsMissing(t *testing.T) {
	for _, name := range []string{"job-started", "job-completed"} {
		missing := filepath.Join(t.TempDir(), "no-crsp")
		out, code, marker := runSh(t, name, missing)
		want := "::warning title=crsp::crsp is not installed at " + missing + ", so this job ran without the queue and the load gate."
		if code != 0 || !strings.HasPrefix(out, want) || !strings.Contains(out, "crsp doctor --check runner.hooks") {
			t.Fatalf("%s: exit %d output %q", name, code, out)
		}
		if !strings.HasPrefix(marker, name+": crsp is not installed at ") {
			t.Fatalf("%s: problem file %q", name, marker)
		}
	}
}

func TestShHookWarnsWhenCrspCannotRun(t *testing.T) {
	// executable, but not a program the OS can run: exit 126 or 127
	bad := filepath.Join(t.TempDir(), "crsp")
	if err := os.WriteFile(bad, []byte("#!/no/such/interpreter\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, code, marker := runSh(t, "job-started", bad)
	if code != 0 || !strings.Contains(out, "::warning title=crsp::crsp at "+bad+" could not run (exit ") || marker == "" {
		t.Fatalf("exit %d output %q marker %q", code, out, marker)
	}
}

func TestShHookPassesCrspExitCodeAndStaysQuiet(t *testing.T) {
	// the gate's own failure (timeout) is not a broken install: no annotation, same exit code
	fake := filepath.Join(t.TempDir(), "crsp")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho \"hook $2\"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, code, marker := runSh(t, "job-started", fake)
	if code != 1 || out != "hook job-started\n" || marker != "" {
		t.Fatalf("exit %d output %q marker %q", code, out, marker)
	}
}

func TestPs1HookWarnsWhenCrspIsMissing(t *testing.T) {
	for _, name := range []string{"job-started", "job-completed"} {
		body, err := Windows(name)
		if err != nil {
			t.Fatal(err)
		}
		s := string(body)
		for _, want := range []string{"::warning title=crsp::", "crsp is not installed at $crsp", "crsp doctor --check runner.hooks", "crsp-hook-problem.txt", "catch {", "exit 0"} {
			if !strings.Contains(s, want) {
				t.Errorf("%s.ps1 lacks %q", name, want)
			}
		}
		if strings.Contains(s, "the job goes on") {
			t.Errorf("%s.ps1 still has the silent message", name)
		}
	}
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Log("pwsh not found: the .ps1 hooks are checked by text only")
		return
	}
	body, _ := Windows("job-started")
	dir := t.TempDir()
	script := filepath.Join(dir, "job-started.ps1")
	if err := os.WriteFile(script, body, 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "no-crsp.exe")
	cmd := exec.CommandContext(t.Context(), pwsh, "-NoProfile", "-File", script)
	cmd.Env = append(os.Environ(), "CRSP_BIN="+missing)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.HasPrefix(string(out), "::warning title=crsp::crsp is not installed at "+missing) {
		t.Fatalf("err %v output %q", err, out)
	}
}

// CRSP38, scheduling R5.62: both scripts of both families turn prompts off
// before they call crsp, so nothing a hook starts can wait for a login.
func TestScriptsTurnPromptsOffBeforeCallingCrsp(t *testing.T) {
	for _, name := range []string{"job-started", "job-completed"} {
		sh, err := Unix(name)
		if err != nil {
			t.Fatal(err)
		}
		ps, err := Windows(name)
		if err != nil {
			t.Fatal(err)
		}
		for kind, body := range map[string]string{"sh": string(sh), "ps1": string(ps)} {
			call := strings.Index(body, `"$crsp" hook`)
			if kind == "ps1" {
				call = strings.Index(body, "& $crsp hook")
			}
			for _, want := range []string{"GIT_TERMINAL_PROMPT", "GCM_INTERACTIVE"} {
				at := strings.Index(body, want)
				if at < 0 || call < 0 || at > call {
					t.Errorf("%s.%s: %s must be set before crsp is called (set at %d, call at %d)", name, kind, want, at, call)
				}
			}
		}
	}
}

// R5.62: with the real shell script, the child sees prompts turned off.
func TestShHookExportsNonInteractiveEnvToCrsp(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "crsp")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho \"git=$GIT_TERMINAL_PROMPT gcm=$GCM_INTERACTIVE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, code, _ := runSh(t, "job-started", fake)
	if code != 0 || out != "git=0 gcm=never\n" {
		t.Fatalf("exit %d output %q", code, out)
	}
}

// R5.26, R5.41: a forked pull request job fails at job start with the policy
// message, through the real sh hook and a real crsp binary. The PowerShell hook
// runs the same `crsp hook job-started`; it is checked by text, and run when
// pwsh is on the machine.
func buildCrsp(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "crsp")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "../../apps/controller/cmd/crsp")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func forkEventFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "event.json")
	body := `{"repository":{"private":false,"full_name":"o/r"},"pull_request":{"head":{"repo":{"fork":true,"full_name":"x/r"}}}}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestForkGuardFailsForkedPullRequestJob(t *testing.T) {
	crsp := buildCrsp(t)
	event := forkEventFile(t)
	env := append(os.Environ(), "CRSP_BIN="+crsp, "GITHUB_EVENT_NAME=pull_request", "GITHUB_EVENT_PATH="+event,
		"CRSP_ALLOW_FORK_PRS=")
	check := func(kind string, out []byte, err error) {
		t.Helper()
		ee, ok := err.(*exec.ExitError)
		if !ok || ee.ExitCode() != 1 {
			t.Fatalf("%s: err %v, want exit 1; output %q", kind, err, out)
		}
		if !strings.Contains(string(out), "::error title=crsp::") || !strings.Contains(string(out), "does not allow fork pull requests") {
			t.Fatalf("%s: output %q", kind, out)
		}
	}
	if runtime.GOOS != "windows" {
		body, err := Unix("job-started")
		if err != nil {
			t.Fatal(err)
		}
		script := filepath.Join(t.TempDir(), "job-started.sh")
		if err := os.WriteFile(script, body, 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), "/bin/sh", script)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		check("sh", out, err)
		// the same job with the policy on is not refused here
		cmd = exec.CommandContext(t.Context(), "/bin/sh", script)
		cmd.Env = append(env, "CRSP_ALLOW_FORK_PRS=1", "CRSP_HOOK_PROBE=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("policy on: %v %q", err, out)
		}
	}
	ps, err := Windows("job-started")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"& $crsp hook $hook @args", "exit $LASTEXITCODE"} {
		if !strings.Contains(string(ps), want) {
			t.Errorf("job-started.ps1 lacks %q: the guard would not reach the job", want)
		}
	}
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Log("pwsh not found: the .ps1 hook is checked by text only")
		return
	}
	script := filepath.Join(t.TempDir(), "job-started.ps1")
	if err := os.WriteFile(script, ps, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), pwsh, "-NoProfile", "-File", script)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	check("ps1", out, err)
}
