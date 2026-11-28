// Package macos is the persistent runner backend for macOS: a launchd daemon
// in /Library/LaunchDaemons whose UserName is the runner's own hidden user
// `crsp-<slug>` (no login shell, not an admin, not shown at login), with the
// runner folder at mode 0700 (runners R5.23, R5.5, R5.37; decision 0003).
//
// Every system call goes through sys.Commands and sys.FS, so tests inject
// fakes and assert the exact dscl and launchctl calls (runners R6.9).
package macos

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/persistent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/hygiene"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/poolsize"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/packaging/hooks"
)

// Config sets where things live.
type Config struct {
	Root       string // one folder per runner
	DaemonsDir string // /Library/LaunchDaemons
	// UIDMin and UIDMax bound the hidden user ids (below 500 stays out of the
	// login window).
	UIDMin, UIDMax int
}

// DefaultConfig is the production layout.
func DefaultConfig() Config {
	return Config{Root: "/usr/local/var/crsp/runners", DaemonsDir: "/Library/LaunchDaemons", UIDMin: 300, UIDMax: 399}
}

// Backend is the macOS persistent backend.
type Backend struct {
	d   sys.Deps
	cfg Config
}

var (
	_ backend.Backend      = (*Backend)(nil)
	_ backend.Finder       = (*Backend)(nil)
	_ backend.Deregisterer = (*Backend)(nil)
)

// New builds the backend over its seams.
func New(d sys.Deps, cfg Config) *Backend {
	def := DefaultConfig()
	if cfg.Root == "" {
		cfg.Root = def.Root
	}
	if cfg.DaemonsDir == "" {
		cfg.DaemonsDir = def.DaemonsDir
	}
	if cfg.UIDMin == 0 {
		cfg.UIDMin, cfg.UIDMax = def.UIDMin, def.UIDMax
	}
	return &Backend{d: d, cfg: cfg}
}

// Name implements backend.Backend.
func (b *Backend) Name() backend.Name { return backend.Persistent }

// Folder is the runner folder for a runner name.
func (b *Backend) Folder(name string) string { return b.cfg.Root + "/" + name }

func (b *Backend) plistPath(label string) string { return b.cfg.DaemonsDir + "/" + label + ".plist" }

func (b *Backend) run(ctx context.Context, step string, c sys.Command) (sys.Result, error) {
	res, err := b.d.Cmd.Run(ctx, c)
	if err != nil {
		return res, fmt.Errorf("%s: %w", step, err)
	}
	if res.ExitCode != 0 {
		return res, fmt.Errorf("%s: %s exited %d: %s", step, c.Name, res.ExitCode, b.redact(firstLine(res.Stderr+res.Stdout)))
	}
	return res, nil
}

func (b *Backend) probe(ctx context.Context, c sys.Command) bool {
	res, err := b.d.Cmd.Run(ctx, c)
	return err == nil && res.ExitCode == 0
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

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// Detect reports whether launchctl is there and the agent is root (runners R5.9).
func (b *Backend) Detect(ctx context.Context) (backend.Support, error) {
	s := backend.Support{Name: backend.Persistent, Isolation: backend.IsolationPersistent, OS: []string{"macos"}}
	if !b.probe(ctx, sys.Command{Name: "launchctl", Args: []string{"version"}}) {
		s.Reason = "launchctl is not available"
		return s, nil
	}
	s.Present = true
	res, err := b.d.Cmd.Run(ctx, sys.Command{Name: "id", Args: []string{"-u"}})
	if err != nil || strings.TrimSpace(res.Stdout) != "0" {
		s.Reason = "the agent is not running as root"
		return s, nil
	}
	s.Usable = true
	return s, nil
}

// PrepareImage has no image for a persistent runner.
func (b *Backend) PrepareImage(context.Context, backend.ImageSpec) (backend.ImageRef, error) {
	return backend.ImageRef{}, nil
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// Plist renders the launchd daemon (runners R5.23): its own user and group,
// the runner folder as working directory, restarted only when it fails: a clean
// exit stays stopped, which the listener gate needs (runners R5.23, R5.134).
func Plist(label, user, folder string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + xmlEscape(label) + `</string>
  <key>UserName</key>
  <string>` + xmlEscape(user) + `</string>
  <key>GroupName</key>
  <string>` + runners.RunnersGroup + `</string>
  <key>WorkingDirectory</key>
  <string>` + xmlEscape(folder) + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>` + xmlEscape(folder) + `/bin/runsvc.sh</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key>
    <false/>
  </dict>
  <key>ProcessType</key>
  <string>Interactive</string>
  <key>StandardOutPath</key>
  <string>` + xmlEscape(folder) + `/_diag/launchd.out.log</string>
  <key>StandardErrorPath</key>
  <string>` + xmlEscape(folder) + `/_diag/launchd.err.log</string>
</dict>
</plist>
`
}

func (b *Backend) want(r backend.RunnerSpec, user string) (persistent.Meta, string) {
	folder := b.Folder(r.Name)
	kv := runnercache.EnvJoin(folder, r.CacheQuota, path.Join)
	// Each persistent runner keeps its dependencies in its own folder, and its
	// hooks clean after every job (runners R5.90, R5.91).
	maps.Copy(kv, runnercache.DependencyEnvJoin(folder, path.Join))
	kv[hygiene.EnvRunnerDir] = folder
	if r.MaxParallel > 0 {
		maps.Copy(kv, poolsize.Env(r.MaxParallel))
	}
	if m := b.d.Addresses.VMImageMirror; m != "" {
		kv[addresses.EnvVMImageMirror] = m // `crsp vm` downloads from the mirror (operations R5.96)
	}
	// The package proxy of the runner's repository, when the hub answered at start (caches R5.76).
	maps.Copy(kv, r.PackageEnv)
	kv["ACTIONS_RUNNER_HOOK_JOB_STARTED"] = folder + "/hooks/job-started.sh"
	kv["ACTIONS_RUNNER_HOOK_JOB_COMPLETED"] = folder + "/hooks/job-completed.sh"
	env := persistent.Env(kv)
	return persistent.Meta{
		Name: r.Name, Account: user, Folder: folder, Service: persistent.ServiceName(r.GitHubURL, r.Name),
		GitHubURL: r.GitHubURL, Labels: r.Labels, RunnerVersion: r.RunnerVersion,
	}, env
}

// ErrRemovalFailed means the old registration could not be removed; the old
// runner is left as it was (runners R5.19).
var ErrRemovalFailed = errors.New("removing the old runner registration failed; the old runner is left as it was")

// Start registers and starts a persistent runner. Same order as Linux: decide;
// remove an old registration first when registering again; make the group and
// the hidden user; fetch the pinned archive and check its SHA-256; unpack; own
// the folder, mode 0700; register as the user with self-update off; hooks and
// .env; the daemon plist; bootstrap it.
func (b *Backend) Start(ctx context.Context, r backend.RunnerSpec, cfg backend.StartConfig) (backend.Handle, error) {
	if cfg.Registration == "" {
		return backend.Handle{}, errors.New("a persistent runner needs a registration token")
	}
	b.learn(cfg.Registration, cfg.Removal, cfg.JITConfig)
	if !runners.ValidSHA256(r.RunnerSHA256) {
		return backend.Handle{}, runners.ErrBadHash
	}
	folder := b.Folder(r.Name)
	user := runners.AccountName(r.Name, nil)
	wantMeta, wantEnv := b.want(r, user)
	label := wantMeta.Service
	h := backend.Handle{RunnerID: r.ID, Ref: label}

	have, err := persistent.ReadMeta(b.d.FS, folder+"/"+persistent.MetaFile)
	if err != nil {
		return h, fmt.Errorf("read runner folder: %w", err)
	}
	var haveEnv string
	if e, err := b.d.FS.ReadFile(folder + "/.env"); err == nil {
		haveEnv = string(e)
	}
	action := persistent.WithLost(persistent.Decide(have, haveEnv, wantMeta, wantEnv), have != nil && persistent.RegistrationLost(folder))
	b.log("register", fmt.Sprintf("runner %s: %s", r.Name, action))

	switch action {
	case persistent.Already:
		return h, nil
	case persistent.UpdateRights, persistent.RewriteEnv:
		if err := b.writeFiles(ctx, folder, user, wantEnv, r.KeepDependencies); err != nil {
			return h, err
		}
		if err := persistent.WriteMeta(b.d.FS, folder+"/"+persistent.MetaFile, wantMeta); err != nil {
			return h, err
		}
		return h, b.restart(ctx, label, folder)
	case persistent.RegisterLost:
		// GitHub deleted the registration: nothing is left to remove (R5.152).
		if err := b.stopDaemon(ctx, label, folder); err != nil {
			return h, err
		}
		if err := persistent.CleanStale(b.d.FS, folder, path.Join); err != nil {
			return h, err
		}
		if err := b.configure(ctx, "register again", folder, user,
			persistent.ConfigArgs(r.GitHubURL, r.Name, r.Group, r.Labels), cfg.Registration); err != nil {
			return h, err
		}
		if err := b.writeFiles(ctx, folder, user, wantEnv, r.KeepDependencies); err != nil {
			return h, err
		}
		if err := persistent.WriteMeta(b.d.FS, folder+"/"+persistent.MetaFile, wantMeta); err != nil {
			return h, err
		}
		_, err := b.run(ctx, "bootstrap", sys.Command{Name: "launchctl", Args: []string{"bootstrap", "system", b.plistPath(label)}})
		return h, err
	case persistent.Reregister:
		if cfg.Removal == "" {
			return h, errors.New("registering again in place needs a remove token for the old runner")
		}
		if err := b.stopDaemon(ctx, have.Service, have.Folder); err != nil {
			return h, err
		}
		if err := b.configure(ctx, "remove old registration", have.Folder, have.Account, []string{"remove"}, cfg.Removal); err != nil {
			_, _ = b.d.Cmd.Run(ctx, sys.Command{Name: "launchctl", Args: []string{"bootstrap", "system", b.plistPath(have.Service)}})
			return h, fmt.Errorf("%w: %v", ErrRemovalFailed, err)
		}
		if err := b.d.FS.RemoveAll(b.plistPath(have.Service)); err != nil {
			return h, err
		}
	case persistent.Fresh:
		if err := b.fresh(ctx, r, folder); err != nil {
			return h, err
		}
	}

	if err := b.ensureUser(ctx, user, folder); err != nil {
		return h, err
	}
	if _, err := b.run(ctx, "own folder", sys.Command{Name: "chown", Args: []string{"-R", user + ":" + runners.RunnersGroup, folder}}); err != nil {
		return h, err
	}
	if _, err := b.run(ctx, "folder mode", sys.Command{Name: "chmod", Args: []string{"0700", folder}}); err != nil {
		return h, err
	}
	if err := b.configure(ctx, "register", folder, user,
		persistent.ConfigArgs(r.GitHubURL, r.Name, r.Group, r.Labels), cfg.Registration); err != nil {
		return h, err
	}
	if err := b.writeFiles(ctx, folder, user, wantEnv, r.KeepDependencies); err != nil {
		return h, err
	}
	if err := persistent.WriteMeta(b.d.FS, folder+"/"+persistent.MetaFile, wantMeta); err != nil {
		return h, err
	}
	if err := b.d.FS.WriteFile(b.plistPath(label), []byte(Plist(label, user, folder)), 0o644); err != nil {
		return h, err
	}
	if _, err := b.run(ctx, "plist owner", sys.Command{Name: "chown", Args: []string{"root:wheel", b.plistPath(label)}}); err != nil {
		return h, err
	}
	if err := b.guardStart(ctx, folder); err != nil {
		return h, err
	}
	if _, err := b.run(ctx, "bootstrap", sys.Command{Name: "launchctl", Args: []string{"bootstrap", "system", b.plistPath(label)}}); err != nil {
		return h, err
	}
	b.log("start", "daemon "+label+" started as "+user)
	return h, nil
}

// restart unloads the daemon, waits for the listener itself to end, and loads
// it again: never a second listener beside one that is still alive (R5.46).
func (b *Backend) restart(ctx context.Context, label, folder string) error {
	if err := b.stopDaemon(ctx, label, folder); err != nil {
		return err
	}
	_, err := b.run(ctx, "bootstrap", sys.Command{Name: "launchctl", Args: []string{"bootstrap", "system", b.plistPath(label)}})
	return err
}

// stopDaemon unloads the daemon and waits until the listener of the runner
// folder is gone.
func (b *Backend) stopDaemon(ctx context.Context, label, folder string) error {
	if err := b.bootout(ctx, label); err != nil {
		return err
	}
	if folder == "" {
		return nil
	}
	return persistent.EndListeners(ctx, persistent.Ender{
		List:   func(ctx context.Context) ([]int, error) { return persistent.UnixListeners(ctx, b.d.Cmd, folder) },
		Signal: persistent.UnixSignal(b.d.Cmd),
		Sleep:  b.d.Sleep,
		Log:    func(m string) { b.log("stop", m) },
	})
}

// guardStart refuses to start the daemon while a listener of the runner folder
// runs (R5.59).
func (b *Backend) guardStart(ctx context.Context, folder string) error {
	return persistent.CheckNoListener(ctx, func(ctx context.Context) ([]int, error) {
		return persistent.UnixListeners(ctx, b.d.Cmd, folder)
	})
}

// bootout unloads the daemon; a daemon that is not loaded is fine.
func (b *Backend) bootout(ctx context.Context, label string) error {
	res, err := b.d.Cmd.Run(ctx, sys.Command{Name: "launchctl", Args: []string{"bootout", "system/" + label}})
	if err != nil {
		return fmt.Errorf("stop: %w", err)
	}
	// 113 and 3: not loaded.
	if res.ExitCode != 0 && res.ExitCode != 113 && res.ExitCode != 3 {
		return fmt.Errorf("stop: launchctl exited %d: %s", res.ExitCode, b.redact(firstLine(res.Stderr)))
	}
	return nil
}

func (b *Backend) fresh(ctx context.Context, r backend.RunnerSpec, folder string) error {
	archive := b.cfg.Root + "/_download-" + r.Name + ".tar.gz"
	if err := b.d.FS.MkdirAll(folder, 0o700); err != nil {
		return err
	}
	pin := runners.Pin{OS: "macos", Arch: r.Arch, Version: r.RunnerVersion, SHA256: r.RunnerSHA256}
	if err := persistent.Download(ctx, b.d, pin.DownloadURL(b.d.Addresses), archive, r.RunnerSHA256); err != nil {
		return err
	}
	if _, err := b.run(ctx, "unpack", sys.Command{Name: "tar", Args: []string{"-xzf", archive, "-C", folder}}); err != nil {
		return err
	}
	return b.d.FS.RemoveAll(archive)
}

// ensureUser makes the group and the hidden user: shell /usr/bin/false, no
// password, not an admin, hidden from the login window (runners R5.23).
func (b *Backend) ensureUser(ctx context.Context, user, folder string) error {
	if !b.probe(ctx, sys.Command{Name: "dscl", Args: []string{".", "-read", "/Groups/" + runners.RunnersGroup}}) {
		if _, err := b.run(ctx, "group", sys.Command{Name: "dseditgroup", Args: []string{"-o", "create", "-q", runners.RunnersGroup}}); err != nil {
			return err
		}
	}
	if b.probe(ctx, sys.Command{Name: "dscl", Args: []string{".", "-read", "/Users/" + user}}) {
		return nil
	}
	gidRes, err := b.run(ctx, "group id", sys.Command{Name: "dscl", Args: []string{".", "-read", "/Groups/" + runners.RunnersGroup, "PrimaryGroupID"}})
	if err != nil {
		return err
	}
	gid := strings.TrimSpace(gidRes.Stdout[strings.LastIndex(gidRes.Stdout, ":")+1:])
	uidRes, err := b.run(ctx, "list ids", sys.Command{Name: "dscl", Args: []string{".", "-list", "/Users", "UniqueID"}})
	if err != nil {
		return err
	}
	uid, err := b.freeUID(uidRes.Stdout)
	if err != nil {
		return err
	}
	path := "/Users/" + user
	for _, kv := range [][]string{
		{"-create", path},
		{"-create", path, "UserShell", "/usr/bin/false"},
		{"-create", path, "RealName", "CRSP runner " + user},
		{"-create", path, "UniqueID", uid},
		{"-create", path, "PrimaryGroupID", gid},
		{"-create", path, "NFSHomeDirectory", folder},
		{"-create", path, "IsHidden", "1"},
		{"-create", path, "Password", "*"},
	} {
		if _, err := b.run(ctx, "user", sys.Command{Name: "dscl", Args: append([]string{"."}, kv...)}); err != nil {
			return err
		}
	}
	return nil
}

func (b *Backend) freeUID(list string) (string, error) {
	used := map[int]bool{}
	for _, l := range strings.Split(list, "\n") {
		f := strings.Fields(l)
		if len(f) == 2 {
			if n, err := strconv.Atoi(f[1]); err == nil {
				used[n] = true
			}
		}
	}
	for n := b.cfg.UIDMin; n <= b.cfg.UIDMax; n++ {
		if !used[n] {
			return strconv.Itoa(n), nil
		}
	}
	return "", errors.New("no free hidden user id")
}

// configure runs config.sh as the user. The token goes in the environment of
// that one process, passed through sudo with --preserve-env, not in an argument.
func (b *Backend) configure(ctx context.Context, step, folder, user string, args []string, token string) error {
	argv := append([]string{"--preserve-env=ACTIONS_RUNNER_INPUT_TOKEN", "-n", "-H", "-u", user, folder + "/config.sh"}, args...)
	_, err := b.run(ctx, step, sys.Command{
		Name: "sudo", Args: argv, Dir: folder,
		SecretEnv: map[string]string{"ACTIONS_RUNNER_INPUT_TOKEN": token},
	})
	return err
}

func (b *Backend) writeFiles(ctx context.Context, folder, user, env string, keep bool) error {
	hookDir := folder + "/hooks"
	if err := b.d.FS.MkdirAll(hookDir, 0o700); err != nil {
		return err
	}
	for _, n := range []string{"job-started", "job-completed"} {
		body, err := hooks.Unix(n)
		if err != nil {
			return err
		}
		if err := b.d.FS.WriteFile(hookDir+"/"+n+".sh", body, 0o700); err != nil {
			return err
		}
	}
	if err := b.d.FS.WriteFile(folder+"/.env", []byte(env), 0o600); err != nil {
		return err
	}
	if err := b.seedKeep(folder, keep); err != nil {
		return err
	}
	_, err := b.run(ctx, "own files", sys.Command{Name: "chown", Args: []string{"-R", user + ":" + runners.RunnersGroup, folder}})
	return err
}

// Stop unloads the daemon. Callers drain first and check for an idle runner
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
	return b.stopDaemon(ctx, h.Ref, folder)
}

// Deregister removes the runner from GitHub with a remove token and checks the
// result; on failure the daemon is loaded again and nothing else is touched.
func (b *Backend) Deregister(ctx context.Context, h backend.Handle, removeToken string) error {
	f, ok, err := b.lookup(ctx, h.Ref)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := b.stopDaemon(ctx, h.Ref, f.Folder); err != nil {
		return err
	}
	if err := b.configure(ctx, "deregister", f.Folder, f.Account, []string{"remove"}, removeToken); err != nil {
		_, _ = b.d.Cmd.Run(ctx, sys.Command{Name: "launchctl", Args: []string{"bootstrap", "system", b.plistPath(h.Ref)}})
		return fmt.Errorf("%w: %v", ErrRemovalFailed, err)
	}
	return nil
}

// CleanUp removes the daemon, the user and the folder; safe to call twice
// (runners R5.10).
func (b *Backend) CleanUp(ctx context.Context, h backend.Handle) error {
	f, ok, err := b.lookup(ctx, h.Ref)
	if err != nil {
		return err
	}
	var errs []error
	if err := b.bootout(ctx, h.Ref); err != nil {
		errs = append(errs, err)
	}
	if err := b.d.FS.RemoveAll(b.plistPath(h.Ref)); err != nil {
		errs = append(errs, err)
	}
	if ok {
		if strings.HasPrefix(f.Account, runners.AccountPrefix) && b.probe(ctx, sys.Command{Name: "dscl", Args: []string{".", "-read", "/Users/" + f.Account}}) {
			if _, err := b.run(ctx, "remove user", sys.Command{Name: "dscl", Args: []string{".", "-delete", "/Users/" + f.Account}}); err != nil {
				errs = append(errs, err)
			}
		}
		if f.Folder != "" && strings.HasPrefix(f.Folder, b.cfg.Root+"/") {
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
	res, err := b.d.Cmd.Run(ctx, sys.Command{Name: "tail", Args: []string{"-n", "200", f.Folder + "/_diag/launchd.out.log"}})
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

// ReportUsage reads the runner user's CPU time and resident memory with ps.
func (b *Backend) ReportUsage(ctx context.Context, h backend.Handle) (backend.Usage, error) {
	f, ok, err := b.lookup(ctx, h.Ref)
	if err != nil || !ok {
		return backend.Usage{}, err
	}
	res, err := b.d.Cmd.Run(ctx, sys.Command{Name: "ps", Args: []string{"-u", f.Account, "-o", "rss=,time="}})
	if err != nil {
		return backend.Usage{}, err
	}
	var u backend.Usage
	for _, l := range strings.Split(res.Stdout, "\n") {
		fl := strings.Fields(l)
		if len(fl) != 2 {
			continue
		}
		if kb, err := strconv.ParseInt(fl[0], 10, 64); err == nil && kb*1024 > u.MemoryPeak {
			u.MemoryPeak = kb * 1024
		}
	}
	return u, nil
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

// seedKeep writes the setting Keep downloaded dependencies once. A file that is
// there stays: the agent changes it later when the owner does (runners R5.90).
func (b *Backend) seedKeep(folder string, keep bool) error {
	if b.d.FS.Exists(folder + "/" + hygiene.SettingsFile) {
		return nil
	}
	return hygiene.WriteKeep(b.d.FS, folder, keep)
}
