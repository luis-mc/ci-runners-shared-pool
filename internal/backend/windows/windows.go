// Package windows is the persistent runner backend for Windows: a service
// that runs as the runner's own local non-admin account (runners R5.13 to
// R5.20, R5.5, R5.37; decision 0003).
//
// The package holds no Windows-only import. Every system call goes through
// sys.Commands, sys.FS and sys.Fetcher, so tests on any OS inject fakes and
// assert the exact PowerShell calls; the real wiring sits behind build tags in
// internal/backend/registry (runners R5.24, R6.9).
//
// Port of dev-standards scripts/delivery_runner_pool.py and the Windows
// service parts of scripts/delivery_ci_setup.py, with the tests of
// test_dl310_windows_runner.py and test_dl315_runner_remove_token.py (see the
// tests in this package).
package windows

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/addresses"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/persistent"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/internal/hygiene"
	"github.com/luis-mc/ci-runners-shared-pool/internal/poolsize"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runnercache"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/packaging/hooks"
)

// Config sets where things live.
type Config struct {
	// Root holds one folder per runner, for example C:\ProgramData\crsp\runners.
	Root string
	// CrspBinary is the all-users binary the hooks call.
	CrspBinary string
	// NewPassword makes a random password. Replaceable for tests.
	NewPassword func() string
	// Rights sets and reads the user rights of the runner account. Nil means
	// the PowerShell implementation; tests pass a fake (runners R5.14).
	Rights RightsGranter
	// LookupSID reads the SID of a local account in this process, without
	// PowerShell. Nil means the PowerShell script (tests, non-Windows).
	LookupSID func(account string) (string, error)
}

// DefaultConfig is the production layout.
func DefaultConfig() Config {
	return Config{
		Root:        `C:\ProgramData\crsp\runners`,
		CrspBinary:  `C:\Program Files\crsp\crsp.exe`,
		NewPassword: RandomPassword,
		LookupSID:   nativeLookupSID,
	}
}

// Backend is the Windows persistent backend.
type Backend struct {
	d      sys.Deps
	cfg    Config
	rights RightsGranter
}

var (
	_ backend.Backend       = (*Backend)(nil)
	_ backend.Finder        = (*Backend)(nil)
	_ backend.Deregisterer  = (*Backend)(nil)
	_ backend.ServiceHealer = (*Backend)(nil)
)

// New builds the backend over its seams.
func New(d sys.Deps, cfg Config) *Backend {
	if cfg.NewPassword == nil {
		cfg.NewPassword = RandomPassword
	}
	if cfg.Root == "" {
		cfg.Root = DefaultConfig().Root
	}
	b := &Backend{d: d, cfg: cfg, rights: cfg.Rights}
	if b.rights == nil {
		b.rights = psRights{b: b}
	}
	return b
}

// RandomPassword makes a 32-character random password of letters and digits
// only. A symbol can break `sc.exe`, a quoting layer or the service logon
// ("Credential ... is invalid", runners R5.96), and 32 characters of 57 give
// about 187 bits, so nothing is lost. It is made inside the process that
// registers the service and is never kept (runners R5.15).
func RandomPassword() string {
	const set = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	for {
		b := make([]byte, 32)
		for i := range b {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(set))))
			if err != nil {
				panic("crypto/rand failed: " + err.Error())
			}
			b[i] = set[n.Int64()]
		}
		// Windows password rules want three of five classes; upper, lower and
		// digit are three, so a local policy never refuses the account.
		if s := string(b); strings.ContainsAny(s, "ABCDEFGHJKLMNPQRSTUVWXYZ") && strings.ContainsAny(s, "abcdefghijkmnopqrstuvwxyz") &&
			strings.ContainsAny(s, "23456789") {
			return s
		}
	}
}

// Name implements backend.Backend.
func (b *Backend) Name() backend.Name { return backend.Persistent }

// Join joins Windows path parts.
func Join(parts ...string) string {
	return strings.Join(parts, `\`)
}

// Folder is the runner folder for a runner name.
func (b *Backend) Folder(name string) string { return Join(b.cfg.Root, name) }

// ps runs a script on powershell.exe. The script goes on stdin; values go in
// the environment; secrets go in SecretEnv. No argument is a quoted command
// line (runners R5.18).
func (b *Backend) ps(ctx context.Context, script string, env, secret map[string]string) (sys.Result, error) {
	return b.d.Cmd.Run(ctx, sys.Command{
		Name:      "powershell.exe",
		Args:      []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "-"},
		Stdin:     wrapScript(script),
		Env:       env,
		SecretEnv: secret,
	})
}

func (b *Backend) psOK(ctx context.Context, step, script string, env, secret map[string]string) error {
	res, err := b.ps(ctx, script, env, secret)
	if err != nil {
		return fmt.Errorf("%s: %w", step, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s: powershell exited %d: %s", step, res.ExitCode, b.redact(plainError(res.Stderr, res.Stdout)))
	}
	return nil
}

// accountSID reads the SID of the account that was made, so icacls never has
// to map a name. It asks Windows directly (LookupAccountName) when it can;
// the PowerShell script is the fallback.
func (b *Backend) accountSID(ctx context.Context, account string, env map[string]string) (string, error) {
	if b.cfg.LookupSID != nil {
		sid, err := b.cfg.LookupSID(account)
		if err == nil && strings.HasPrefix(sid, "S-1-") {
			return sid, nil
		}
		// CRSP234: the in-process lookup can miss an account that exists
		// (ERROR_NONE_MAPPED); Get-LocalUser asks the SAM by exact name, so it
		// is the second try. Only when both fail is the folder reset refused.
		if err == nil {
			err = fmt.Errorf("answer %q is not a SID", sid)
		}
		b.log("acl", fmt.Sprintf("native SID lookup of %s failed (%v); trying PowerShell", account, err))
	}
	res, err := b.ps(ctx, scriptAccountSID, env, nil)
	if err != nil {
		return "", &FolderResetError{Detail: b.redact(fmt.Sprintf("read SID of %s: %v", account, err)), Err: err}
	}
	sid := strings.TrimSpace(res.Stdout)
	if res.ExitCode == 0 && sid == "" {
		// Never an empty success: Get-LocalUser found nothing or its output
		// was lost, and either way there is no SID (OPS88).
		e := &FolderResetError{Detail: fmt.Sprintf("read SID of %s: account not found (powershell exited 0 but printed nothing on stdout; stderr: %q)", account, b.redact(strings.TrimSpace(res.Stderr)))}
		b.log("acl", e.Detail)
		return "", e
	}
	if res.ExitCode != 0 || !strings.HasPrefix(sid, "S-1-") {
		e := &FolderResetError{Detail: fmt.Sprintf("read SID of %s: powershell exited %d: %s", account, res.ExitCode, b.redact(plainError(res.Stderr, res.Stdout)))}
		b.log("acl", e.Detail)
		return "", e
	}
	return sid, nil
}

func (b *Backend) redact(s string) string {
	if b.d.Redact != nil {
		return b.d.Redact(s)
	}
	return s
}

func (b *Backend) log(step, msg string) {
	if b.d.LogLine != nil {
		b.d.LogLine(step, b.redact(msg))
	}
}

// Detect reports whether PowerShell is there and whether the agent has the
// admin rights account and service work needs (runners R5.9).
func (b *Backend) Detect(ctx context.Context) (backend.Support, error) {
	s := backend.Support{Name: backend.Persistent, Isolation: backend.IsolationPersistent, OS: []string{"windows"}}
	res, err := b.ps(ctx, scriptDetect, nil, nil)
	if err != nil || res.ExitCode != 0 {
		s.Reason = "powershell.exe cannot be run"
		return s, nil
	}
	s.Present = true
	out := strings.TrimSpace(res.Stdout)
	if !strings.Contains(out, "admin=True") {
		s.Reason = "the agent is not running with administrator rights"
		return s, nil
	}
	s.Usable = true
	return s, nil
}

// PrepareImage has no image for a persistent runner.
func (b *Backend) PrepareImage(context.Context, backend.ImageSpec) (backend.ImageRef, error) {
	return backend.ImageRef{}, nil
}

// wantMeta and wantEnv are what the runner should look like.
func (b *Backend) want(r backend.RunnerSpec, account string) (persistent.Meta, string) {
	folder := b.Folder(r.Name)
	hookDir := Join(folder, "hooks")
	kv := runnercache.EnvJoin(folder, r.CacheQuota, Join)
	// Each persistent runner keeps its dependencies in its own folder, and its
	// hooks clean after every job (runners R5.90, R5.91).
	maps.Copy(kv, runnercache.DependencyEnvJoin(folder, Join))
	kv[hygiene.EnvRunnerDir] = folder
	if r.SharedTools.On() {
		maps.Copy(kv, r.SharedTools.Env(Join, r.Name))
	}
	if r.MaxParallel > 0 {
		maps.Copy(kv, poolsize.Env(r.MaxParallel))
	}
	if m := b.d.Addresses.VMImageMirror; m != "" {
		kv[addresses.EnvVMImageMirror] = m // `crsp vm` downloads from the mirror (operations R5.96)
	}
	kv["ACTIONS_RUNNER_HOOK_JOB_STARTED"] = Join(hookDir, "job-started.ps1")
	kv["ACTIONS_RUNNER_HOOK_JOB_COMPLETED"] = Join(hookDir, "job-completed.ps1")
	kv["PSExecutionPolicyPreference"] = "RemoteSigned"
	env := persistent.Env(kv)
	return persistent.Meta{
		Name: r.Name, Account: account, Folder: folder,
		Service:   persistent.ServiceName(r.GitHubURL, r.Name),
		GitHubURL: r.GitHubURL, Labels: r.Labels, SymlinkRight: r.WindowsSymlinkRight,
		RunnerVersion: r.RunnerVersion,
	}, env
}

// ErrRemovalFailed means the old registration could not be removed. The old
// runner and its folder are left as they were (runners R5.19).
var ErrRemovalFailed = errors.New("removing the old runner registration failed; the old runner is left as it was")

// Start registers and starts a persistent runner. Steps, in order:
//
//  1. decide from what is on the machine (already, rewrite .env, change the
//     right, register again, or fresh);
//  2. when registering again in place: remove the old registration with a
//     remove token and check the result, before anything is locked;
//  3. fetch the pinned archive, check its SHA-256, unpack;
//  4. register the runner with GitHub, its self-update off;
//  5. make the account (random password, never kept) and its rights;
//  6. write the .ps1 hooks and the .env;
//  7. take ownership of the folder for the account, reset every file to
//     inherited access, then lock the folder to the account, Administrators and SYSTEM;
//  8. register the service as the account and start it.
func (b *Backend) Start(ctx context.Context, r backend.RunnerSpec, cfg backend.StartConfig) (backend.Handle, error) {
	st := &startState{}
	h, err := b.start(ctx, r, cfg, st)
	if err != nil && st.made {
		err = b.cleanFailedStart(ctx, r, cfg, st, h.Ref, err)
	}
	return h, err
}

func (b *Backend) start(ctx context.Context, r backend.RunnerSpec, cfg backend.StartConfig, st *startState) (backend.Handle, error) {
	if cfg.Registration == "" {
		return backend.Handle{}, errors.New("a persistent runner needs a registration token")
	}
	b.learn(cfg.Registration, cfg.Removal, cfg.JITConfig)
	if !runners.ValidSHA256(r.RunnerSHA256) {
		return backend.Handle{}, runners.ErrBadHash
	}
	folder := b.Folder(r.Name)
	account := runners.AccountName(r.Name, nil)
	wantMeta, wantEnv := b.want(r, account)
	h := backend.Handle{RunnerID: r.ID, Ref: wantMeta.Service}

	have, err := persistent.ReadMeta(b.d.FS, Join(folder, persistent.MetaFile))
	if err != nil {
		return h, fmt.Errorf("read runner folder: %w", err)
	}
	var haveEnv string
	if e, err := b.d.FS.ReadFile(Join(folder, ".env")); err == nil {
		haveEnv = string(e)
	}
	action := persistent.Decide(have, haveEnv, wantMeta, wantEnv)
	b.log("register", fmt.Sprintf("runner %s: %s", r.Name, action))

	env := map[string]string{"CRSP_FOLDER": folder, "CRSP_ACCOUNT": account, "CRSP_SERVICE": wantMeta.Service, "CRSP_RUNNER": r.Name}
	switch action {
	case persistent.Already:
		return h, nil
	case persistent.RewriteEnv:
		if err := b.writeHooksAndEnv(folder, wantEnv, r.KeepDependencies); err != nil {
			return h, err
		}
		if err := persistent.WriteMeta(b.d.FS, Join(folder, persistent.MetaFile), wantMeta); err != nil {
			return h, err
		}
		// Stop, wait for the listener itself to end, start: never a second
		// listener beside one that is still alive (runners R5.46).
		if err := b.stopService(ctx, env, folder); err != nil {
			return h, err
		}
		return h, b.startService(ctx, env, account)
	case persistent.UpdateRights:
		if err := b.rights.Grant(ctx, account, r.WindowsSymlinkRight); err != nil {
			return h, err
		}
		return h, persistent.WriteMeta(b.d.FS, Join(folder, persistent.MetaFile), wantMeta)
	case persistent.Reregister:
		if cfg.Removal == "" {
			return h, errors.New("registering again in place needs a remove token for the old runner")
		}
		oldService := have.Service
		oldEnv := map[string]string{"CRSP_FOLDER": have.Folder, "CRSP_SERVICE": oldService}
		if err := b.stopService(ctx, oldEnv, have.Folder); err != nil {
			return h, err
		}
		// The old registration goes first, and its result is checked, before
		// the folder is locked to the new account (R5.19, DL315).
		if err := b.runner(ctx, "remove old registration", have.Folder, []string{"remove"}, cfg.Removal); err != nil {
			// Leave the old runner as it was: start its service again.
			_ = b.startService(ctx, oldEnv, have.Account)
			return h, fmt.Errorf("%w: %v", ErrRemovalFailed, err)
		}
		if err := b.psOK(ctx, "delete old service", scriptDeleteService, oldEnv, nil); err != nil {
			return h, err
		}
		st.made = true
	case persistent.Fresh:
		st.made = true
		archive := Join(b.cfg.Root, "_download-"+r.Name+".zip")
		if err := b.d.FS.MkdirAll(b.cfg.Root, 0o755); err != nil {
			return h, err
		}
		pin := runners.Pin{OS: "windows", Arch: r.Arch, Version: r.RunnerVersion, SHA256: r.RunnerSHA256}
		if err := persistent.Download(ctx, b.d, pin.DownloadURL(b.d.Addresses), archive, r.RunnerSHA256); err != nil {
			return h, err
		}
		env["CRSP_ARCHIVE"] = archive
		if err := b.psOK(ctx, "unpack", scriptExtract, env, nil); err != nil {
			return h, err
		}
	}

	// Register with GitHub. The runner's own update is off (R5.38). The token
	// goes in the environment of this one process.
	st.registered = true
	if err := b.runner(ctx, "register", folder,
		ConfigureArgs(r.GitHubURL, r.Name, r.Group, r.Labels), cfg.Registration); err != nil {
		return h, err
	}

	// The password is made here, set on the account and passed only to the
	// service registration (R5.15).
	password := b.cfg.NewPassword()
	secret := map[string]string{"CRSP_PASSWORD": password}
	env["CRSP_GROUP"] = runners.RunnersGroup
	st.account = true
	if err := b.makeAccount(ctx, account, env, secret); err != nil {
		return h, err
	}
	if err := b.rights.Grant(ctx, account, r.WindowsSymlinkRight); err != nil {
		return h, err
	}
	// The shared tool folders: every runner account is in crsp-runners, which
	// gets Modify, inherited (caches R5.57).
	for _, c := range runnercache.WindowsSharePlan(r.SharedTools) {
		res, err := b.d.Cmd.Run(ctx, c)
		if err == nil && res.ExitCode != 0 {
			err = fmt.Errorf("%s exited %d: %s", c.Name, res.ExitCode, strings.TrimSpace(res.Stderr))
		}
		if err != nil {
			return h, fmt.Errorf("share tools: %w", err)
		}
	}
	if err := b.writeHooksAndEnv(folder, wantEnv, r.KeepDependencies); err != nil {
		return h, err
	}
	if err := persistent.WriteMeta(b.d.FS, Join(folder, persistent.MetaFile), wantMeta); err != nil {
		return h, err
	}
	// An account switch leaves old owners and explicit entries on the files
	// (R5.50): hand the folder to the account and reset it before the lock.
	sid, err := b.accountSID(ctx, account, env)
	if err != nil {
		return h, err
	}
	if err := ResetFolderACL(ctx, b.d, folder, account, sid); err != nil {
		return h, err
	}
	if err := b.psOK(ctx, "lock folder", scriptLock, env, nil); err != nil {
		return h, err
	}
	if err := b.guardStart(ctx, folder); err != nil {
		return h, err
	}
	// Log the account on as a service with the password the service will get,
	// so a missing right or a wrong password is named in plain words here and
	// not as an obscure "cannot be started" from Start-Service.
	if err := b.psOK(ctx, "check logon", scriptCheckLogon, env, secret); err != nil {
		return h, err
	}
	st.service = true
	if err := b.psOK(ctx, "register service", scriptRegisterService, env, secret); err != nil {
		return h, err
	}
	b.log("start", "service "+wantMeta.Service+" started as "+account)
	return h, nil
}

// makeAccount makes the account and checks it exists before anything reads its
// SID: the script confirms with one line, and a run that exits 0 without it
// did not make the account (OPS88). Every add, also a retry after a clean-up
// that deleted the account, goes through here first.
func (b *Backend) makeAccount(ctx context.Context, account string, env, secret map[string]string) error {
	res, err := b.ps(ctx, scriptAccount, env, secret)
	if err != nil {
		return fmt.Errorf("account: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("account: powershell exited %d: %s", res.ExitCode, b.redact(plainError(res.Stderr, res.Stdout)))
	}
	if !strings.Contains(res.Stdout, "account-ready "+account) {
		return fmt.Errorf("account: %s was not made: powershell exited 0 without confirming it (stderr: %q)", account, b.redact(strings.TrimSpace(res.Stderr)))
	}
	return nil
}

func flag(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

// ConfigureArgs are the Runner.Listener arguments to register a runner. Windows
// calls Runner.Listener.exe directly, and config.cmd is only
// `Runner.Listener.exe configure %*`, so the verb goes first; without it the
// listener starts in run mode and says "Not configured" (OPS93). Linux and
// macOS call config.sh, which adds the verb itself.
func ConfigureArgs(githubURL, name, group string, labels []string) []string {
	return append([]string{"configure"}, persistent.ConfigArgs(githubURL, name, group, labels)...)
}

// runner runs Runner.Listener (configure or remove). The arguments are one
// JSON array, each argument on its own. The exit code is checked.
func (b *Backend) runner(ctx context.Context, step, folder string, args []string, token string) error {
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	res, err := b.ps(ctx, scriptRunner,
		map[string]string{"CRSP_FOLDER": folder, "CRSP_ARGS": string(raw)},
		map[string]string{"ACTIONS_RUNNER_INPUT_TOKEN": token})
	if err != nil {
		return fmt.Errorf("%s: %w", step, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s: runner exited %d: %s", step, res.ExitCode, b.redact(plainError(res.Stderr, res.Stdout)))
	}
	return nil
}

func (b *Backend) writeHooksAndEnv(folder, env string, keep bool) error {
	hookDir := Join(folder, "hooks")
	if err := b.d.FS.MkdirAll(hookDir, 0o755); err != nil {
		return err
	}
	for _, n := range []string{"job-started", "job-completed"} {
		body, err := hooks.Windows(n)
		if err != nil {
			return err
		}
		if err := b.d.FS.WriteFile(Join(hookDir, n+".ps1"), body, 0o644); err != nil {
			return err
		}
	}
	if err := b.d.FS.WriteFile(Join(folder, ".env"), []byte(env), 0o644); err != nil {
		return err
	}
	// The setting Keep downloaded dependencies is written once; the agent changes it later (runners R5.90).
	if b.d.FS.Exists(Join(folder, hygiene.SettingsFile)) {
		return nil
	}
	return hygiene.WriteKeep(b.d.FS, folder, keep)
}

// Stop stops the service. Callers drain first and check for an idle runner
// right before (runners R6.4).
func (b *Backend) Stop(ctx context.Context, h backend.Handle) error {
	f, ok, err := b.lookup(ctx, h.Ref)
	if err != nil {
		return err
	}
	folder := ""
	if ok {
		folder = f.Folder
	}
	return b.stopService(ctx, map[string]string{"CRSP_SERVICE": h.Ref}, folder)
}

// stopService stops the service and waits until the Runner.Listener of the
// runner folder is gone. The service wrapper being stopped is not enough: a
// listener that outlives it keeps the session, and a second one is refused
// (runners R5.46).
func (b *Backend) stopService(ctx context.Context, env map[string]string, folder string) error {
	if err := b.psOK(ctx, "stop", scriptStopService, env, nil); err != nil {
		return err
	}
	if folder == "" {
		return nil
	}
	list := func(ctx context.Context) ([]int, error) {
		res, err := b.ps(ctx, scriptListeners, map[string]string{"CRSP_FOLDER": folder}, nil)
		if err != nil {
			return nil, fmt.Errorf("list listeners: %w", err)
		}
		if res.ExitCode != 0 {
			return nil, fmt.Errorf("list listeners: powershell exited %d", res.ExitCode)
		}
		return persistent.ParsePIDs(res.Stdout), nil
	}
	return persistent.EndListeners(ctx, persistent.Ender{
		List: list,
		// taskkill /T ends the listener and every child with it.
		Signal: func(ctx context.Context, pids []int, force bool) error {
			ids := make([]string, len(pids))
			for i, p := range pids {
				ids[i] = strconv.Itoa(p)
			}
			return b.psOK(ctx, "end listener", scriptEndListeners, map[string]string{"CRSP_PIDS": strings.Join(ids, ",")}, nil)
		},
		Sleep: b.d.Sleep,
		Log:   func(m string) { b.log("stop", m) },
	})
}

// guardStart refuses to start the service while a listener of the runner
// folder runs (R5.59).
func (b *Backend) guardStart(ctx context.Context, folder string) error {
	return persistent.CheckNoListener(ctx, func(ctx context.Context) ([]int, error) {
		res, err := b.ps(ctx, scriptListeners, map[string]string{"CRSP_FOLDER": folder}, nil)
		if err != nil {
			return nil, fmt.Errorf("list listeners: %w", err)
		}
		if res.ExitCode != 0 {
			return nil, fmt.Errorf("list listeners: powershell exited %d", res.ExitCode)
		}
		return persistent.ParsePIDs(res.Stdout), nil
	})
}

// Deregister removes the runner from GitHub with a remove token and checks the
// result. On failure nothing else is touched (runners R5.19, R5.4).
func (b *Backend) Deregister(ctx context.Context, h backend.Handle, removeToken string) error {
	f, ok, err := b.lookup(ctx, h.Ref)
	if err != nil {
		return err
	}
	if !ok {
		return nil // already gone
	}
	if err := b.stopService(ctx, map[string]string{"CRSP_SERVICE": h.Ref}, f.Folder); err != nil {
		return err
	}
	if err := b.runner(ctx, "deregister", f.Folder, []string{"remove"}, removeToken); err != nil {
		_ = b.startService(ctx, map[string]string{"CRSP_SERVICE": h.Ref}, f.Account)
		return fmt.Errorf("%w: %v", ErrRemovalFailed, err)
	}
	return nil
}

// CleanUp removes the service, the account and the folder. It is safe to call
// twice (runners R5.10). Deregister runs first.
func (b *Backend) CleanUp(ctx context.Context, h backend.Handle) error {
	f, ok, err := b.lookup(ctx, h.Ref)
	if err != nil {
		return err
	}
	env := map[string]string{"CRSP_SERVICE": h.Ref}
	var errs []error
	if err := b.psOK(ctx, "delete service", scriptDeleteService, env, nil); err != nil {
		errs = append(errs, err)
	}
	if ok {
		account := strings.TrimPrefix(f.Account, `.\`)
		if strings.HasPrefix(account, runners.AccountPrefix) {
			if err := b.psOK(ctx, "remove account", scriptRemoveAccount, map[string]string{"CRSP_ACCOUNT": account}, nil); err != nil {
				errs = append(errs, err)
			}
		}
		if f.Folder != "" && strings.HasPrefix(strings.ToLower(f.Folder), strings.ToLower(b.cfg.Root)+`\`) {
			if err := b.d.FS.RemoveAll(f.Folder); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// StreamLogs sends the newest runner log lines, redacted.
func (b *Backend) StreamLogs(ctx context.Context, h backend.Handle, out chan<- backend.LogLine) error {
	f, ok, err := b.lookup(ctx, h.Ref)
	if err != nil || !ok {
		return err
	}
	res, err := b.ps(ctx, scriptTail, map[string]string{"CRSP_FOLDER": f.Folder}, nil)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		select {
		case out <- backend.LogLine{At: time.Now(), Level: "info", Message: b.redact(strings.TrimSpace(line)), Step: "runner"}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// ReportUsage reads the service process' CPU time and peak memory.
func (b *Backend) ReportUsage(ctx context.Context, h backend.Handle) (backend.Usage, error) {
	res, err := b.ps(ctx, scriptUsage, map[string]string{"CRSP_SERVICE": h.Ref}, nil)
	if err != nil {
		return backend.Usage{}, err
	}
	var cpu, mem int64
	for _, kv := range strings.Split(strings.TrimSpace(res.Stdout), ";") {
		k, v, _ := strings.Cut(kv, "=")
		var n int64
		_, _ = fmt.Sscan(v, &n)
		switch k {
		case "cpu_ms":
			cpu = n
		case "mem":
			mem = n
		}
	}
	return backend.Usage{CPU: time.Duration(cpu) * time.Millisecond, MemoryPeak: mem}, nil
}

// learn gives one-time values to the redactor before any log line is written (runners R5.11).
func (b *Backend) learn(values ...string) {
	if b.d.Secret == nil {
		return
	}
	for _, v := range values {
		if v != "" {
			b.d.Secret(v)
		}
	}
}
