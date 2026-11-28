package windows

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/persistent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys/sysfake"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

const testSID = "S-1-5-21-1111-2222-3333-1004"

const testPassword = "S3cret-Pass-Word-ZZZ-never-logged!"

type rig struct {
	b    *Backend
	cmd  *sysfake.Commands
	fs   *sysfake.FS
	fe   *sysfake.Fetcher
	logs []string
	body []byte
	// accounts are the local accounts the fake machine has; the account
	// scripts add and remove them and answer as the real scripts do.
	accounts map[string]bool
}

func (r *rig) accountScripts(c sys.Command) (sys.Result, bool) {
	acct := c.Env["CRSP_ACCOUNT"]
	switch {
	case strings.Contains(c.Stdin, "Expand-Archive"):
		_ = r.fs.MkdirAll(c.Env["CRSP_FOLDER"], 0o755)
	case strings.Contains(c.Stdin, "account-ready"):
		r.accounts[acct] = true
		return sys.Result{Stdout: "account-ready " + acct + "\r\n"}, true
	case strings.Contains(c.Stdin, "account-absent"):
		if !r.accounts[acct] {
			return sys.Result{Stdout: "account-absent\r\n"}, true
		}
		delete(r.accounts, acct)
		return sys.Result{Stdout: "account-removed\r\n"}, true
	}
	return sys.Result{}, false
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{cmd: &sysfake.Commands{Rules: []sysfake.Rule{{Match: "SID.Value", Result: sys.Result{Stdout: testSID + "\r\n"}}}}, fs: sysfake.NewFS(), body: []byte("zip bytes")}
	r.accounts = map[string]bool{}
	r.cmd.Fallback = r.accountScripts
	r.fe = &sysfake.Fetcher{FS: r.fs, Body: r.body}
	verify := func(path, want string) error {
		d, err := r.fs.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(d)
		if hex.EncodeToString(sum[:]) != want {
			return runners.ErrHashMismatch
		}
		return nil
	}
	r.b = New(sys.Deps{
		Cmd: r.cmd, FS: r.fs, Fetch: r.fe, Verify: verify,
		LogLine: func(step, msg string) { r.logs = append(r.logs, step+": "+msg) },
	}, Config{Root: `C:\ProgramData\crsp\runners`, NewPassword: func() string { return testPassword }})
	return r
}

func (r *rig) spec() backend.RunnerSpec {
	sum := sha256.Sum256(r.body)
	return backend.RunnerSpec{
		ID: "r1", Name: "box-1", OS: "windows", Arch: "x64",
		Labels: []string{"self-hosted", "windows", "x64"}, GitHubURL: "https://github.com/luis-mc/demo",
		RunnerVersion: "2.330.0", RunnerSHA256: hex.EncodeToString(sum[:]),
	}
}

func scripts(r *rig) []string {
	var out []string
	for _, c := range r.cmd.Calls {
		out = append(out, c.Stdin)
	}
	return out
}

// index of the first call whose script contains marker, or -1.
// withSID keeps the SID answer beside a test's own rules.
func withSID(rules []sysfake.Rule) []sysfake.Rule {
	return append(rules, sysfake.Rule{Match: "SID.Value", Result: sys.Result{Stdout: testSID + "\r\n"}})
}

func idx(r *rig, marker string) int {
	for i, c := range r.cmd.Calls {
		if strings.Contains(c.Stdin, marker) {
			return i
		}
	}
	return -1
}

// isListener is a direct call of a runner folder's Runner.Listener.exe.
func isListener(c sys.Command) bool {
	return strings.HasSuffix(c.Name, `\bin\Runner.Listener.exe`)
}

// listenerIdx is the index of the first Runner.Listener call, or -1.
func listenerIdx(r *rig) int {
	for i, c := range r.cmd.Calls {
		if isListener(c) {
			return i
		}
	}
	return -1
}

func start(t *testing.T, r *rig, spec backend.RunnerSpec, cfg backend.StartConfig) error {
	t.Helper()
	_, err := r.b.Start(context.Background(), spec, cfg)
	return err
}

// R5.14: the account is in the group, may log on as a service, is denied
// interactive and remote desktop logon, and is never an administrator (R6.3).
func TestAccountRights(t *testing.T) {
	r := newRig(t)
	if err := start(t, r, r.spec(), backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	i := idx(r, "New-LocalUser")
	if i < 0 {
		t.Fatal("no account step")
	}
	s := r.cmd.Calls[i].Stdin
	for _, want := range []string{"New-LocalGroup", "Add-LocalGroupMember"} {
		if !strings.Contains(s, want) {
			t.Errorf("account script lacks %s", want)
		}
	}
	if strings.Contains(s, "Administrators") {
		t.Error("account script must never touch the Administrators group")
	}
	if got := r.cmd.Calls[i].Env["CRSP_ACCOUNT"]; got != "crsp-box-1" {
		t.Errorf("account = %q", got)
	}
	if got := r.cmd.Calls[i].Env["CRSP_GROUP"]; got != "crsp-runners" {
		t.Errorf("group = %q", got)
	}
	j := idx(r, "SeServiceLogonRight")
	if j < i {
		t.Fatal("the rights step must follow the account step")
	}
	rs := r.cmd.Calls[j].Stdin
	for _, want := range []string{"SeServiceLogonRight", "SeDenyInteractiveLogonRight", "SeDenyRemoteInteractiveLogonRight"} {
		if !strings.Contains(rs, want) {
			t.Errorf("rights script lacks %s", want)
		}
	}
	if strings.Contains(rs, "Administrators") {
		t.Error("rights script must never touch the Administrators group")
	}
}

// R5.14, owner decision: the symlink right only when the settings ask.
func TestSymlinkRightOnlyWhenAsked(t *testing.T) {
	for _, ask := range []bool{false, true} {
		r := newRig(t)
		spec := r.spec()
		spec.WindowsSymlinkRight = ask
		if err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
			t.Fatal(err)
		}
		c := r.cmd.Calls[idx(r, "SeServiceLogonRight")]
		want := "0"
		if ask {
			want = "1"
		}
		if c.Env["CRSP_SYMLINK"] != want || c.Env["CRSP_ACCOUNT"] != "crsp-box-1" {
			t.Errorf("ask=%v: CRSP_SYMLINK=%q account=%q", ask, c.Env["CRSP_SYMLINK"], c.Env["CRSP_ACCOUNT"])
		}
		if !strings.Contains(c.Stdin, "($env:CRSP_SYMLINK -eq '1')") {
			t.Error("the right must follow the setting, so a rerun can remove it")
		}
	}
}

// A rerun that only changes the symlink setting changes the right in place.
func TestSymlinkChangeUpdatesRightOnly(t *testing.T) {
	r := newRig(t)
	spec := r.spec()
	if err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	r.cmd.Calls = nil
	spec.WindowsSymlinkRight = true
	if err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	if len(r.cmd.Calls) != 1 || !strings.Contains(r.cmd.Calls[0].Stdin, "SeCreateSymbolicLinkPrivilege") {
		t.Fatalf("calls = %v", r.cmd.Texts())
	}
	if _, set := r.cmd.Calls[0].SecretEnv["CRSP_PASSWORD"]; set {
		t.Error("no new password for a rights change")
	}
}

// R5.15 (port of test_plan_holds_no_password_and_no_token): the password is in
// the environment of the two processes that need it and nowhere else.
func TestNoPasswordInPlanOrLog(t *testing.T) {
	r := newRig(t)
	if err := start(t, r, r.spec(), backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	withPassword := 0
	for _, c := range r.cmd.Calls {
		if _, ok := c.SecretEnv["CRSP_PASSWORD"]; ok {
			withPassword++
		}
		if strings.Contains(c.String(), testPassword) || strings.Contains(c.Stdin, testPassword) {
			t.Errorf("password in the command text: %s", c.Name)
		}
		for _, v := range c.Env {
			if strings.Contains(v, testPassword) || strings.Contains(v, "REGTOKEN") {
				t.Error("secret in a plain environment value")
			}
		}
		if strings.Contains(c.String(), "REGTOKEN") || strings.Contains(c.Stdin, "REGTOKEN") {
			t.Error("token in the command text")
		}
	}
	if withPassword != 3 { // account, logon check, service registration
		t.Errorf("password reached %d processes, want 3", withPassword)
	}
	for name, d := range r.fs.Files {
		if strings.Contains(string(d), testPassword) || strings.Contains(string(d), "REGTOKEN") {
			t.Errorf("secret written to %s", name)
		}
	}
	for _, l := range r.logs {
		if strings.Contains(l, testPassword) || strings.Contains(l, "REGTOKEN") {
			t.Errorf("secret in log %q", l)
		}
	}
}

// R5.16 (port of test_folder_rights): Full control for the account,
// Administrators and SYSTEM only; inheritance off.
func TestFolderAcl(t *testing.T) {
	r := newRig(t)
	if err := start(t, r, r.spec(), backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	c := r.cmd.Calls[idx(r, "Set-Acl")]
	for _, want := range []string{"SetAccessRuleProtection($true, $false)", "$env:CRSP_ACCOUNT", "BUILTIN\\Administrators", "NT AUTHORITY\\SYSTEM", "FullControl"} {
		if !strings.Contains(c.Stdin, want) {
			t.Errorf("lock script lacks %s", want)
		}
	}
	if strings.Contains(c.Stdin, "Users") || strings.Contains(c.Stdin, "Everyone") {
		t.Error("the lock grants a group it must not")
	}
	if c.Env["CRSP_FOLDER"] != `C:\ProgramData\crsp\runners\box-1` {
		t.Errorf("folder = %q", c.Env["CRSP_FOLDER"])
	}
}

// R5.17 (port of test_no_cmd_hook_and_no_second_machine_folder).
func TestPs1HooksCallAllUsersBinary(t *testing.T) {
	r := newRig(t)
	if err := start(t, r, r.spec(), backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	folder := `C:\ProgramData\crsp\runners\box-1`
	env := string(r.fs.Files[folder+`\.env`])
	for _, want := range []string{
		`ACTIONS_RUNNER_HOOK_JOB_STARTED=` + folder + `\hooks\job-started.ps1`,
		`ACTIONS_RUNNER_HOOK_JOB_COMPLETED=` + folder + `\hooks\job-completed.ps1`,
		"PSExecutionPolicyPreference=RemoteSigned",
		`CI_TOOLS_DIR=` + folder + `\cache\tools`,
	} {
		if !strings.Contains(env, want) {
			t.Errorf(".env lacks %q:\n%s", want, env)
		}
	}
	for name, d := range r.fs.Files {
		if strings.HasSuffix(strings.ToLower(name), ".cmd") || strings.HasSuffix(strings.ToLower(name), ".bat") {
			t.Errorf("a cmd hook was written: %s", name)
		}
		if strings.Contains(name, "hooks") {
			if !strings.HasSuffix(name, ".ps1") {
				t.Errorf("hook %s is not .ps1", name)
			}
			s := string(d)
			if !strings.Contains(s, "ProgramFiles") || !strings.Contains(s, `crsp\crsp.exe`) {
				t.Errorf("hook %s does not call the all-users binary", name)
			}
			if strings.Contains(strings.ToLower(s), "appdata") || strings.Contains(strings.ToLower(s), "userprofile") {
				t.Errorf("hook %s points into a user profile", name)
			}
		}
	}
	// The machine policy is not changed.
	for _, s := range scripts(r) {
		if strings.Contains(s, "Set-ExecutionPolicy") {
			t.Error("the machine execution policy must not change")
		}
	}
}

// R5.18: no cmd quoting. Every call is powershell.exe with fixed arguments; the
// script is on stdin and each value is its own environment entry.
func TestNoCmdQuotedCommandLine(t *testing.T) {
	r := newRig(t)
	spec := r.spec()
	spec.Labels = []string{"self-hosted", "windows", "x64"}
	if err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range r.cmd.Calls {
		if c.Name == "takeown" || c.Name == "icacls" {
			continue // the fixed ACL reset, tested in TestAccountSwitchResetsFolderACL
		}
		if c.Name != "powershell.exe" && !isListener(c) {
			t.Errorf("unexpected program %s", c.Name)
		}
		for _, a := range c.Args {
			if strings.ContainsAny(a, `"'`) || strings.Contains(strings.ToLower(a), "cmd") {
				t.Errorf("quoted or cmd argument %q", a)
			}
		}
		for _, bad := range []string{"cmd.exe", "cmd /c", "sc.exe", "binPath=", "icacls", "net user", "schtasks"} {
			if strings.Contains(c.Stdin, bad) {
				t.Errorf("script uses %s", bad)
			}
		}
	}
	if idx(r, "New-Service") < 0 {
		t.Error("the service is registered with New-Service")
	}
	// Configure gets each argument on its own (OPS96: no PowerShell layer).
	c := r.cmd.Calls[listenerIdx(r)]
	got := strings.Join(c.Args, " ")
	if !strings.HasPrefix(got, "configure --unattended") {
		t.Errorf("the listener gets the configure verb first (OPS93), got %s", got)
	}
	if !strings.Contains(got, "--labels self-hosted,windows,x64") || !strings.Contains(got, "--disableupdate") ||
		!strings.Contains(got, "--replace") {
		t.Errorf("configure args = %s", got)
	}
	if c.SecretEnv["ACTIONS_RUNNER_INPUT_TOKEN"] != "REGTOKEN" {
		t.Error("the registration token goes in the environment of the one process")
	}
}

// R5.19 (port of test_dl315_runner_remove_token.py): the old registration is
// removed with a remove token before the folder is locked.
func TestRemoveBeforeLock(t *testing.T) {
	r := newRig(t)
	spec := r.spec()
	if err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	// Another account in the meta: the runner is registered again in place.
	folder := `C:\ProgramData\crsp\runners\box-1`
	meta, _ := persistent.ReadMeta(r.fs, folder+`\`+persistent.MetaFile)
	meta.Account = "old-account"
	if err := persistent.WriteMeta(r.fs, folder+`\`+persistent.MetaFile, *meta); err != nil {
		t.Fatal(err)
	}
	r.cmd.Calls = nil
	if err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN2", Removal: "REMOVETOKEN"}); err != nil {
		t.Fatal(err)
	}
	rm, lock := -1, idx(r, "Set-Acl")
	for i, c := range r.cmd.Calls {
		if c.SecretEnv["ACTIONS_RUNNER_INPUT_TOKEN"] == "REMOVETOKEN" {
			rm = i
			if strings.Join(c.Args, " ") != "remove" {
				t.Errorf("the old registration is removed with `remove`, got %q", c.Args)
			}
		}
	}
	if rm < 0 || lock < 0 || rm > lock {
		t.Fatalf("remove at %d, lock at %d; remove must come first", rm, lock)
	}
	if idx(r, "Invoke-CimMethod") > lock {
		t.Error("the old service is deleted before the lock")
	}
}

// R5.19: a failed removal stops, leaves the old runner and folder, and starts
// the old service again.
func TestRemoveFailureStops(t *testing.T) {
	r := newRig(t)
	spec := r.spec()
	if err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	folder := `C:\ProgramData\crsp\runners\box-1`
	meta, _ := persistent.ReadMeta(r.fs, folder+`\`+persistent.MetaFile)
	meta.Account = "old-account"
	_ = persistent.WriteMeta(r.fs, folder+`\`+persistent.MetaFile, *meta)
	r.cmd.Calls = nil
	r.cmd.Rules = withSID([]sysfake.Rule{{Match: "Runner.Listener.exe", Result: sys.Result{ExitCode: 1, Stderr: "remove failed"}}})
	err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN2", Removal: "REMOVETOKEN"})
	if !errors.Is(err, ErrRemovalFailed) {
		t.Fatalf("err = %v", err)
	}
	if idx(r, "Set-Acl") >= 0 || idx(r, "New-LocalUser") >= 0 || idx(r, "New-Service") >= 0 {
		t.Error("nothing may be changed after a failed removal")
	}
	last := r.cmd.Calls[len(r.cmd.Calls)-1].Stdin
	if !strings.Contains(last, "Start-Service") {
		t.Error("the old service is started again")
	}
	if _, ok := r.fs.Files[folder+`\.env`]; !ok {
		t.Error("the old folder must be left as it was")
	}
}

// Without a remove token a re-register cannot start.
func TestReregisterNeedsRemoveToken(t *testing.T) {
	r := newRig(t)
	spec := r.spec()
	_ = start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"})
	folder := `C:\ProgramData\crsp\runners\box-1`
	meta, _ := persistent.ReadMeta(r.fs, folder+`\`+persistent.MetaFile)
	meta.Labels = []string{"self-hosted", "windows", "x64", "gpu"}
	_ = persistent.WriteMeta(r.fs, folder+`\`+persistent.MetaFile, *meta)
	r.cmd.Calls = nil
	if err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN2"}); err == nil {
		t.Fatal("want an error")
	}
	if len(r.cmd.Calls) != 0 {
		t.Errorf("nothing runs: %v", r.cmd.Texts())
	}
}

// R5.20: everything matches, so `already` and nothing is touched.
func TestRerunAlready(t *testing.T) {
	r := newRig(t)
	spec := r.spec()
	if err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	r.cmd.Calls, r.fs.Order = nil, nil
	if err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	if len(r.cmd.Calls) != 0 || len(r.fs.Order) != 0 {
		t.Errorf("a matching runner is not touched: %v %v", r.cmd.Texts(), r.fs.Order)
	}
	if got := r.logs[len(r.logs)-1]; !strings.Contains(got, "already") {
		t.Errorf("last log = %q", got)
	}
}

// R5.20: only a stale .env: rewrite it and restart, with no new password.
func TestStaleEnvOnlyRewrites(t *testing.T) {
	r := newRig(t)
	spec := r.spec()
	if err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	folder := `C:\ProgramData\crsp\runners\box-1`
	r.fs.Files[folder+`\.env`] = []byte("ACTIONS_RUNNER_HOOK_JOB_STARTED=C:\\old.cmd\n")
	r.cmd.Calls = nil
	if err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	// Stop, wait for the listener, give the logon right again (R5.89), start.
	// The two list rights (R5.156) come before the logon right and the start.
	if len(r.cmd.Calls) != 6 || !strings.Contains(r.cmd.Calls[0].Stdin, "Stop-Service") ||
		!strings.Contains(r.cmd.Calls[1].Stdin, "Get-Process") || !isListRight(r.cmd.Calls[2]) || !isListRight(r.cmd.Calls[3]) ||
		!strings.Contains(r.cmd.Calls[4].Stdin, "LsaAddAccountRights") || !strings.Contains(r.cmd.Calls[5].Stdin, "Start-Service") {
		t.Fatalf("calls = %v", r.cmd.Texts())
	}
	if len(r.cmd.Calls[0].SecretEnv) != 0 {
		t.Error("no password for an env rewrite")
	}
	if !strings.Contains(string(r.fs.Files[folder+`\.env`]), "PSExecutionPolicyPreference=RemoteSigned") {
		t.Error(".env not rewritten")
	}
}

// R5.37: a wrong hash is refused and the file deleted; nothing is registered.
func TestWrongHashRefused(t *testing.T) {
	r := newRig(t)
	spec := r.spec()
	spec.RunnerSHA256 = strings.Repeat("ab", 32) // valid shape, wrong value
	err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"})
	if !errors.Is(err, runners.ErrHashMismatch) {
		t.Fatalf("err = %v", err)
	}
	if len(r.fs.Names()) != 0 {
		t.Errorf("the download must be deleted: %v", r.fs.Names())
	}
	if listenerIdx(r) >= 0 {
		t.Error("nothing is registered after a bad hash")
	}
	spec.RunnerSHA256 = strings.Repeat("a", 64)
	if err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"}); !errors.Is(err, runners.ErrBadHash) {
		t.Errorf("a repeated-character hash is wrong: %v", err)
	}
}

// R5.10: CleanUp twice is safe; it removes service, account and folder.
func TestCleanUpTwice(t *testing.T) {
	r := newRig(t)
	spec := r.spec()
	if err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	svc := persistent.ServiceName(spec.GitHubURL, spec.Name)
	r.cmd.Rules = withSID([]sysfake.Rule{{Match: "Win32_Service -Filter \"Name LIKE", Result: sys.Result{Stdout: `{"Name":"` + svc + `","State":"Running","PathName":"\"C:\\ProgramData\\crsp\\runners\\box-1\\bin\\RunnerService.exe\"","StartName":".\\crsp-box-1"}`}}})
	h := backend.Handle{Ref: svc}
	if err := r.b.CleanUp(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	if idx(r, "Remove-LocalUser") < 0 {
		t.Error("account not removed")
	}
	for _, n := range r.fs.Names() {
		if strings.HasPrefix(n, `C:\ProgramData\crsp\runners\box-1\`) {
			t.Errorf("folder not removed: %s", n)
		}
	}
	r.cmd.Rules = withSID([]sysfake.Rule{{Match: "Name LIKE", Result: sys.Result{Stdout: "[]"}}})
	if err := r.b.CleanUp(context.Background(), h); err != nil {
		t.Fatalf("second CleanUp: %v", err)
	}
}

// Deregister: a failed remove leaves the runner running and says so.
func TestDeregisterFailureKeepsRunner(t *testing.T) {
	r := newRig(t)
	spec := r.spec()
	svc := persistent.ServiceName(spec.GitHubURL, spec.Name)
	row := `{"Name":"` + svc + `","State":"Running","PathName":"\"C:\\ProgramData\\crsp\\runners\\box-1\\bin\\RunnerService.exe\"","StartName":".\\crsp-box-1"}`
	r.cmd.Rules = withSID([]sysfake.Rule{
		{Match: "Runner.Listener.exe", Result: sys.Result{ExitCode: 1}},
		{Match: "Name LIKE", Result: sys.Result{Stdout: row}},
	})
	err := r.b.Deregister(context.Background(), backend.Handle{Ref: svc}, "REMOVETOKEN")
	if !errors.Is(err, ErrRemovalFailed) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(r.cmd.Calls[len(r.cmd.Calls)-1].Stdin, "Start-Service") {
		t.Error("the runner is started again after a failed removal")
	}
}

// R5.5 (port of test_dl318_doctor_locked_runner.py): runners come from the
// service list; a runner not made by CRSP is not managed; an unreadable folder
// is still reported.
func TestFindFromServiceList(t *testing.T) {
	r := newRig(t)
	spec := r.spec()
	if err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	mine := persistent.ServiceName(spec.GitHubURL, spec.Name)
	rows := `[
 {"Name":"` + mine + `","State":"Running","PathName":"\"C:\\ProgramData\\crsp\\runners\\box-1\\bin\\RunnerService.exe\"","StartName":".\\crsp-box-1"},
 {"Name":"actions.runner.luis-mc-old.hand-made","State":"Stopped","PathName":"\"D:\\actions-runner\\bin\\RunnerService.exe\"","StartName":"NT AUTHORITY\\NETWORK SERVICE"},
 {"Name":"Spooler","State":"Running","PathName":"x","StartName":"LocalSystem"}
]`
	r.cmd.Rules = withSID([]sysfake.Rule{{Match: "Name LIKE", Result: sys.Result{Stdout: rows}}})
	found, err := r.b.Find(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("found %d: %+v", len(found), found)
	}
	a, o := found[0], found[1]
	if !a.Managed || a.Folder != `C:\ProgramData\crsp\runners\box-1` || a.Account != `.\crsp-box-1` || !a.Running {
		t.Errorf("managed runner = %+v", a)
	}
	if o.Managed || o.Folder != `D:\actions-runner` || !strings.Contains(o.Account, "NETWORK SERVICE") || o.Running {
		t.Errorf("hand-made runner = %+v", o)
	}
	// Only the powershell list is asked; no folder walk.
	if !strings.Contains(r.cmd.Calls[len(r.cmd.Calls)-1].Stdin, "Win32_Service") {
		t.Error("runners are found from Win32_Service")
	}
}

type deniedFS struct{ *sysfake.FS }

func (deniedFS) ReadFile(string) ([]byte, error) { return nil, errors.New("access denied") }

func TestFindReportsUnreadableFolder(t *testing.T) {
	r := newRig(t)
	r.b.d.FS = deniedFS{r.fs}
	r.cmd.Rules = withSID([]sysfake.Rule{{Match: "Name LIKE", Result: sys.Result{Stdout: `{"Name":"actions.runner.a-b.x","State":"Running","PathName":"\"C:\\locked\\bin\\RunnerService.exe\"","StartName":".\\crsp-x"}`}}})
	found, err := r.b.Find(context.Background())
	if err != nil || len(found) != 1 {
		t.Fatalf("%v %v", found, err)
	}
	if !found[0].FolderUnreadable || found[0].Managed {
		t.Errorf("%+v", found[0])
	}
}

func TestDetect(t *testing.T) {
	r := newRig(t)
	r.cmd.Rules = withSID([]sysfake.Rule{{Match: "IsInRole", Result: sys.Result{Stdout: "admin=False;ps=5.1"}}})
	s, _ := r.b.Detect(context.Background())
	if s.Usable || s.Reason == "" || !s.Present {
		t.Errorf("%+v", s)
	}
	r.cmd.Rules = withSID([]sysfake.Rule{{Match: "IsInRole", Result: sys.Result{Stdout: "admin=True;ps=5.1"}}})
	s, _ = r.b.Detect(context.Background())
	if !s.Usable {
		t.Errorf("%+v", s)
	}
}

func TestNoTokenInStartIfMissing(t *testing.T) {
	r := newRig(t)
	if err := start(t, r, r.spec(), backend.StartConfig{}); err == nil {
		t.Fatal("want an error without a registration token")
	}
}

// runners R5.46: the service wrapper being stopped is not enough; a listener
// that is still alive blocks the start, so no second listener appears.
func TestRestartNeverStartsASecondListener(t *testing.T) {
	r := newRig(t)
	r.b.d.Sleep = func(context.Context, time.Duration) {}
	spec := r.spec()
	if err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	folder := `C:\ProgramData\crsp\runners\box-1`
	r.fs.Files[folder+`\.env`] = []byte("stale\n")
	r.cmd.Calls = nil
	r.cmd.Rules = withSID([]sysfake.Rule{{Match: "Get-Process", Result: sys.Result{Stdout: "5120\r\n"}}})
	err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"})
	if !errors.Is(err, persistent.ErrListenerAlive) {
		t.Fatalf("err = %v", err)
	}
	if idx(r, "Start-Service") >= 0 {
		t.Error("Start-Service ran beside a live listener")
	}
}

// R5.50: after the account exists and before the lock, the folder goes to the
// account and every file is reset to inherited access.
func TestAccountSwitchResetsFolderACL(t *testing.T) {
	r := newRig(t)
	if err := start(t, r, r.spec(), backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for i, c := range r.cmd.Calls {
		if (c.Name == "takeown" || c.Name == "icacls") && !isListRight(c) {
			got = append(got, c.String())
			if i < idx(r, "New-LocalUser") || i > idx(r, "SetAccessRuleProtection") {
				t.Errorf("%s runs outside account..lock", c.Name)
			}
		}
	}
	folder := r.b.Folder("box-1")
	want := []string{
		"takeown /F " + folder + " /R /D Y",
		"icacls " + folder + " /setowner *" + testSID + " /T /C",
		"icacls " + folder + " /reset /T /C",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls = %q, want %q", got, want)
	}
}

func TestACLResetFailureStopsBeforeLock(t *testing.T) {
	r := newRig(t)
	r.cmd.Rules = append(r.cmd.Rules, sysfake.Rule{Match: "/setowner", Result: sys.Result{ExitCode: 5, Stderr: "Access is denied."}})
	err := start(t, r, r.spec(), backend.StartConfig{Registration: "REGTOKEN"})
	if err == nil || !strings.HasPrefix(err.Error(), ErrPrepareFolder) || !strings.Contains(err.Error(), "set owner") || !strings.Contains(err.Error(), "exited 5") || !strings.Contains(err.Error(), "Access is denied") {
		t.Fatalf("err = %v, want the short line with the step, the exit code and the message", err)
	}
	var fe *FolderResetError
	if !errors.As(err, &fe) || !strings.Contains(fe.Detail, "Access is denied") || !strings.Contains(fe.Detail, "reset folder owner") {
		t.Fatalf("detail = %+v", fe)
	}
	if !strings.Contains(strings.Join(r.logs, "\n"), "Access is denied") {
		t.Error("the full reason is in the log")
	}
	if idx(r, "SetAccessRuleProtection") >= 0 || idx(r, "New-Service") >= 0 {
		t.Error("the folder was locked or the service made after a failed reset")
	}
}

// R5.56: RUSTUP_HOME and CARGO_HOME sit under the runner's own folder, so two
// runner accounts never share a toolchain.
func TestToolHomesPerRunner(t *testing.T) {
	r := newRig(t)
	if err := start(t, r, r.spec(), backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	folder := `C:\ProgramData\crsp\runners\box-1`
	env := string(r.fs.Files[folder+`\.env`])
	for _, want := range []string{
		`RUSTUP_HOME=` + folder + `\cache\tools\rustup` + "\n",
		`CARGO_HOME=` + folder + `\cache\tools\cargo` + "\n",
	} {
		if !strings.Contains(env, want) {
			t.Errorf(".env lacks %q:\n%s", want, env)
		}
	}
}

// runners R5.53 / machines R5.54: the runner's CPU count reaches its jobs.
func TestEnvCarriesCIMaxParallel(t *testing.T) {
	r := newRig(t)
	spec := r.spec()
	spec.MaxParallel = 3
	if err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	if env := string(r.fs.Files[`C:\ProgramData\crsp\runners\box-1\.env`]); !strings.Contains(env, "CI_MAX_PARALLEL=3") {
		t.Errorf(".env:\n%s", env)
	}
}

// The owner is the SID read after the account exists: the SID is read after
// the account is made, before the first icacls, and the name never reaches
// /setowner (error 1332 on a name Windows cannot map).
func TestSIDReadAfterAccountBeforeICACLS(t *testing.T) {
	r := newRig(t)
	if err := start(t, r, r.spec(), backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	acct, sid := idx(r, "New-LocalUser"), idx(r, "SID.Value")
	first := -1
	for i, c := range r.cmd.Calls {
		if (c.Name == "takeown" || c.Name == "icacls") && !isListRight(c) {
			first = i
			break
		}
	}
	if acct < 0 || sid <= acct || first <= sid {
		t.Fatalf("order: account %d, sid %d, first folder tool %d", acct, sid, first)
	}
	for _, c := range r.cmd.Calls {
		if c.Name == "icacls" && strings.Contains(c.String(), "crsp-box-1") {
			t.Errorf("icacls got the account name: %s", c)
		}
	}
}

func TestNoSIDStopsBeforeICACLS(t *testing.T) {
	r := newRig(t)
	r.cmd.Rules = withSID([]sysfake.Rule{{Match: "SID.Value", Result: sys.Result{ExitCode: 1, Stderr: errMarker + "no such user"}}})
	err := start(t, r, r.spec(), backend.StartConfig{Registration: "REGTOKEN"})
	if err == nil || !strings.HasPrefix(err.Error(), ErrPrepareFolder) {
		t.Fatalf("err = %v", err)
	}
	for _, c := range r.cmd.Calls {
		if (c.Name == "icacls" || c.Name == "takeown") && !isListRight(c) {
			t.Errorf("%s ran without a SID", c.Name)
		}
	}
}

func TestAccountSIDEmptyOutputNamesCause(t *testing.T) {
	r := newRig(t)
	r.cmd.Rules = []sysfake.Rule{{Match: "SID.Value", Result: sys.Result{}}}
	_, err := r.b.accountSID(context.Background(), "crsp-box-1", map[string]string{"CRSP_ACCOUNT": "crsp-box-1"})
	if err == nil || !strings.Contains(err.Error(), "account not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestAccountSIDGoodAndNative(t *testing.T) {
	r := newRig(t)
	got, err := r.b.accountSID(context.Background(), "crsp-box-1", nil)
	if err != nil || got != testSID {
		t.Fatalf("ps path: %q %v", got, err)
	}
	if !strings.Contains(scriptAccountSID, "Write-Output $sid") || strings.Contains(scriptAccountSID, "[Console]::Out") {
		t.Error("script must use Write-Output")
	}
	r.b.cfg.LookupSID = func(string) (string, error) { return testSID, nil }
	n := len(r.cmd.Calls)
	if got, err = r.b.accountSID(context.Background(), "crsp-box-1", nil); err != nil || got != testSID || len(r.cmd.Calls) != n {
		t.Fatalf("native path: %q %v calls %d->%d", got, err, n, len(r.cmd.Calls))
	}
	r.b.cfg.LookupSID = func(string) (string, error) { return "", errors.New("none") }
	n = len(r.cmd.Calls)
	if got, err = r.b.accountSID(context.Background(), "crsp-box-1", nil); err != nil || got != testSID || len(r.cmd.Calls) != n+1 {
		t.Errorf("native miss must fall back to PowerShell: %q %v", got, err)
	}
}

func TestQualifyAccount(t *testing.T) {
	for _, c := range []struct{ m, a, want string }{
		{"GAMEROOMPC", "crsp-gameroompc-33e8", `GAMEROOMPC\crsp-gameroompc-33e8`},
		{"BOX", `.\crsp-box-1`, `BOX\crsp-box-1`},
		{"BOX", `OTHER\crsp-box-1`, `BOX\crsp-box-1`},
		{"", "crsp-box-1", "crsp-box-1"},
	} {
		if got := QualifyAccount(c.m, c.a); got != c.want {
			t.Errorf("QualifyAccount(%q,%q) = %q, want %q", c.m, c.a, got, c.want)
		}
	}
}

// Caches R5.57: the shared tool folders give the runner accounts' group Modify,
// inherited, and each runner has its own build folder.
func TestSharedToolFoldersGetGroupModify(t *testing.T) {
	r := newRig(t)
	spec := r.spec()
	spec.SharedTools = runnercache.SharedTools{Root: `C:\ProgramData\crsp\tools\r1`, Group: runners.RunnersGroup}
	if err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	texts := strings.Join(r.cmd.Texts(), "\n")
	if !strings.Contains(texts, `icacls C:\ProgramData\crsp\tools\r1 /grant crsp-runners:(OI)(CI)(M) /T /C`) {
		t.Errorf("no grant:\n%s", texts)
	}
	_, env := r.b.want(spec, "crsp-box-1")
	if !strings.Contains(env, `CARGO_TARGET_DIR=C:\ProgramData\crsp\tools\r1\cargo-target\box-1`) {
		t.Errorf("env:\n%s", env)
	}
}

// CRSP382, runners R5.152: a runner whose registration GitHub deleted, with an
// unchanged config, registers again in place: the service stops, the stale
// files go, Runner.Listener configure runs with --replace and the fresh token
// (no remove step), and the service starts again. The account and the service
// stay.
func TestLostRegistrationRegistersAgainInPlace(t *testing.T) {
	r := newRig(t)
	root := t.TempDir()
	cfg := r.b.cfg
	cfg.Root = root
	r.b = New(r.b.d, cfg)
	if err := start(t, r, r.spec(), backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	folder := Join(root, "box-1")
	_ = r.fs.WriteFile(Join(folder, ".runner"), []byte("old"), 0o600)
	_ = r.fs.WriteFile(Join(folder, ".credentials_rsaparams"), []byte("old"), 0o600)

	r.cmd.Calls = nil
	if err := start(t, r, r.spec(), backend.StartConfig{Registration: "TOKEN2"}); err != nil {
		t.Fatal(err)
	}
	if len(r.cmd.Calls) != 0 {
		t.Fatalf("an unchanged registered runner is left alone: %d calls", len(r.cmd.Calls))
	}

	diag := Join(folder, "_diag")
	if err := r.fs.MkdirAll(diag, 0o755); err != nil {
		t.Fatal(err)
	}
	log := "Failed to create a session. The runner registration has been deleted from the server, please re-configure.\n"
	if err := r.fs.WriteFile(Join(diag, "Runner_20261128-100000-utc.log"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	r.cmd.Calls = nil
	if err := start(t, r, r.spec(), backend.StartConfig{Registration: "TOKEN2"}); err != nil {
		t.Fatal(err)
	}
	i := listenerIdx(r)
	if i < 0 {
		t.Fatalf("no Runner.Listener call: %d calls", len(r.cmd.Calls))
	}
	c := r.cmd.Calls[i]
	if c.Args[0] != "configure" && !strings.Contains(strings.Join(c.Args, " "), "--unattended") {
		t.Errorf("args %v", c.Args)
	}
	if !strings.Contains(strings.Join(c.Args, " "), "--replace") || c.SecretEnv["ACTIONS_RUNNER_INPUT_TOKEN"] != "TOKEN2" {
		t.Errorf("registers with --replace and the fresh token: %v", c.Args)
	}
	for _, x := range r.cmd.Calls {
		if isListener(x) && len(x.Args) > 0 && x.Args[0] == "remove" {
			t.Error("the registration is gone at GitHub: there is nothing to remove")
		}
		if strings.Contains(x.Stdin, "New-LocalUser") || strings.Contains(x.Stdin, "New-Service") {
			t.Error("the account and the service stay")
		}
	}
	var sawStop, sawStart bool
	for j, x := range r.cmd.Calls {
		sawStop = sawStop || (j < i && strings.Contains(x.Stdin, "Stop-Service"))
		sawStart = sawStart || (j > i && strings.Contains(x.Stdin, "Start-Service"))
	}
	if !sawStop || !sawStart {
		t.Errorf("stop before %v, start after %v", sawStop, sawStart)
	}
	if r.fs.Exists(Join(folder, ".runner")) || r.fs.Exists(Join(folder, ".credentials_rsaparams")) {
		t.Error("the stale registration files stay")
	}
}

// isListRight is one of the icacls calls of ListRightPlan (runners R5.156).
func isListRight(c sys.Command) bool {
	return c.Name == "icacls" && strings.Contains(c.String(), "/grant "+runners.RunnersGroup+":(RX)")
}

// R5.156: the runner accounts may list the runners folder and the folder that
// holds it, on those folders only, before the service starts. The listener
// stops at once when one folder above its own cannot be read.
func TestRunnerGroupListsTheFoldersAboveTheRunner(t *testing.T) {
	r := newRig(t)
	if err := start(t, r, r.spec(), backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range r.cmd.Calls {
		if isListRight(c) {
			got = append(got, c.String())
		}
	}
	want := []string{
		`icacls C:\ProgramData\crsp\runners /grant crsp-runners:(RX) /C`,
		`icacls C:\ProgramData\crsp /grant crsp-runners:(RX) /C`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls = %q, want %q", got, want)
	}
	first := -1
	for i, c := range r.cmd.Calls {
		if isListRight(c) {
			first = i
			break
		}
	}
	if first < idx(r, "New-LocalUser") || first > idx(r, "New-Service") {
		t.Errorf("the right is given at %d, outside account..service", first)
	}
	for _, c := range r.cmd.Calls {
		if isListRight(c) && strings.Contains(c.String(), "(OI)") {
			t.Errorf("the right must not be inherited: %s", c)
		}
	}
}

// R5.156: a service that starts and stops at once is a failed start that says
// why, not a start that looks good.
func TestStartScriptsCheckTheServiceStaysUp(t *testing.T) {
	for name, s := range map[string]string{"startService": scriptStartService, "registerService": scriptRegisterService} {
		if !strings.Contains(s, "stopped again at once") || !strings.Contains(s, "ActionsRunnerService") {
			t.Errorf("%s does not check that the service stays up", name)
		}
	}
}
