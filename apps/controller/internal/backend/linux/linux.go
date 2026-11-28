// Package linux is the persistent runner backend for Linux: a systemd system
// unit that runs as the runner's own system user `crsp-<slug>` in the group
// `crsp-runners`, with the runner folder at mode 0700 (runners R5.21, R5.22,
// R5.5, R5.37; decision 0003).
//
// Every system call goes through sys.Commands and sys.FS, so tests inject
// fakes and assert the exact useradd and systemctl calls (runners R6.9).
package linux

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
	// Root holds one folder per runner.
	Root string
	// UnitDir is the systemd system unit directory.
	UnitDir string
	// IDs gives each runner account its persistent uid from the reserved
	// range (runners R5.72). Nil lets useradd pick one.
	IDs backend.IDSource
}

// DefaultConfig is the production layout.
func DefaultConfig() Config {
	return Config{Root: "/var/lib/crsp/runners", UnitDir: "/etc/systemd/system"}
}

// Backend is the Linux persistent backend.
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
	if cfg.UnitDir == "" {
		cfg.UnitDir = def.UnitDir
	}
	return &Backend{d: d, cfg: cfg}
}

// Name implements backend.Backend.
func (b *Backend) Name() backend.Name { return backend.Persistent }

// Folder is the runner folder for a runner name.
func (b *Backend) Folder(name string) string { return b.cfg.Root + "/" + name }

func (b *Backend) unitPath(service string) string { return b.cfg.UnitDir + "/" + service + ".service" }

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

// probe runs a command whose exit code is an answer, not a failure.
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

// Detect reports whether systemd is there and the agent is root (runners R5.9).
func (b *Backend) Detect(ctx context.Context) (backend.Support, error) {
	s := backend.Support{Name: backend.Persistent, Isolation: backend.IsolationPersistent, OS: []string{"linux"}}
	if !b.probe(ctx, sys.Command{Name: "systemctl", Args: []string{"--version"}}) {
		s.Reason = "systemd is not available"
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

// Unit renders the systemd system unit (runners R5.22).
func Unit(name, user, folder string, extraWritable ...string) string {
	return strings.Join([]string{
		"[Unit]",
		"Description=CRSP runner " + name,
		"After=network-online.target",
		"Wants=network-online.target",
		"",
		"[Service]",
		"User=" + user,
		"Group=" + runners.RunnersGroup,
		"WorkingDirectory=" + folder,
		"ExecStart=" + folder + "/bin/runsvc.sh",
		"KillMode=mixed",
		"KillSignal=SIGTERM",
		"TimeoutStopSec=5min",
		"Restart=on-failure",
		"RestartSec=5",
		"NoNewPrivileges=yes",
		"PrivateTmp=yes",
		"ProtectSystem=strict",
		"ReadWritePaths=" + strings.Join(append([]string{folder}, extraWritable...), " "),
		"",
		"[Install]",
		"WantedBy=multi-user.target",
		"",
	}, "\n")
}

func (b *Backend) want(r backend.RunnerSpec, user string) (persistent.Meta, string) {
	folder := b.Folder(r.Name)
	kv := runnercache.EnvJoin(folder, r.CacheQuota, path.Join)
	// Each persistent runner keeps its dependencies in its own folder, and its
	// hooks clean after every job (runners R5.90, R5.91).
	maps.Copy(kv, runnercache.DependencyEnvJoin(folder, path.Join))
	kv[hygiene.EnvRunnerDir] = folder
	if r.SharedTools.On() {
		maps.Copy(kv, r.SharedTools.Env(path.Join, r.Name))
	}
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

// Start registers and starts a persistent runner.
//
// Order: decide from what is there; when registering again in place, remove the
// old registration first and check it; make the group and user; fetch the
// pinned archive and check its SHA-256; unpack; own the folder, mode 0700;
// register as the user with self-update off; write hooks and .env; write the
// unit; enable and start it.
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
	h := backend.Handle{RunnerID: r.ID, Ref: wantMeta.Service}

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
	unit := wantMeta.Service + ".service"

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
		// Stop, wait for the listener itself to end, then start: never a
		// second listener beside one that is still alive (runners R5.46).
		return h, b.restartUnit(ctx, unit, folder)
	case persistent.RegisterLost:
		// GitHub deleted the registration: nothing is left to remove. Stop the
		// unit, drop the stale files, register again in place, start (R5.140).
		if err := b.stopUnit(ctx, unit, folder); err != nil {
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
		_, err := b.run(ctx, "start", sys.Command{Name: "systemctl", Args: []string{"start", unit}})
		return h, err
	case persistent.Reregister:
		if cfg.Removal == "" {
			return h, errors.New("registering again in place needs a remove token for the old runner")
		}
		oldUnit := have.Service + ".service"
		if err := b.stopUnit(ctx, oldUnit, have.Folder); err != nil {
			return h, err
		}
		if err := b.configure(ctx, "remove old registration", have.Folder, have.Account, []string{"remove"}, cfg.Removal); err != nil {
			_, _ = b.d.Cmd.Run(ctx, sys.Command{Name: "systemctl", Args: []string{"start", oldUnit}})
			return h, fmt.Errorf("%w: %v", ErrRemovalFailed, err)
		}
		if _, err := b.run(ctx, "disable old service", sys.Command{Name: "systemctl", Args: []string{"disable", oldUnit}}); err != nil {
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
	if err := b.shareTools(ctx, r, user); err != nil {
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
	if err := b.d.FS.WriteFile(b.unitPath(wantMeta.Service), []byte(Unit(r.Name, user, folder, sharedWritable(r)...)), 0o644); err != nil {
		return h, err
	}
	if err := b.guardStart(ctx, folder); err != nil {
		return h, err
	}
	for _, c := range [][]string{{"daemon-reload"}, {"enable", "--now", unit}} {
		if _, err := b.run(ctx, "systemctl "+c[0], sys.Command{Name: "systemctl", Args: c}); err != nil {
			return h, err
		}
	}
	b.log("start", "unit "+unit+" started as "+user)
	return h, nil
}

// sharedWritable is the shared tools folder the unit may write, when it is used.
func sharedWritable(r backend.RunnerSpec) []string {
	if r.SharedTools.On() {
		return []string{r.SharedTools.Root}
	}
	return nil
}

// shareTools makes the repository's shared tool folders and gives the runner's
// user the group that may read and write them (caches R5.57). Without
// SharedTools it does nothing.
func (b *Backend) shareTools(ctx context.Context, r backend.RunnerSpec, user string) error {
	st := r.SharedTools
	if !st.On() {
		return nil
	}
	if !b.probe(ctx, sys.Command{Name: "getent", Args: []string{"group", st.Group}}) {
		if _, err := b.run(ctx, "tools group", sys.Command{Name: "groupadd", Args: []string{"--system", st.Group}}); err != nil {
			return err
		}
	}
	if _, err := b.run(ctx, "tools group member", sys.Command{Name: "usermod", Args: []string{"-aG", st.Group, user}}); err != nil {
		return err
	}
	plan, err := runnercache.LinuxSharePlan(st, r.Name)
	if err != nil {
		return err
	}
	for _, c := range plan {
		if _, err := b.run(ctx, "share tools", c); err != nil {
			return err
		}
	}
	return nil
}

func (b *Backend) fresh(ctx context.Context, r backend.RunnerSpec, folder string) error {
	archive := b.cfg.Root + "/_download-" + r.Name + ".tar.gz"
	if err := b.d.FS.MkdirAll(folder, 0o700); err != nil {
		return err
	}
	pin := runners.Pin{OS: "linux", Arch: r.Arch, Version: r.RunnerVersion, SHA256: r.RunnerSHA256}
	if err := persistent.Download(ctx, b.d, pin.DownloadURL(b.d.Addresses), archive, r.RunnerSHA256); err != nil {
		return err
	}
	if _, err := b.run(ctx, "unpack", sys.Command{Name: "tar", Args: []string{"-xzf", archive, "-C", folder}}); err != nil {
		return err
	}
	return b.d.FS.RemoveAll(archive)
}

// ensureUser makes the group and the system user: no login shell, no password,
// no sudo (runners R5.21).
func (b *Backend) ensureUser(ctx context.Context, user, folder string) error {
	if !b.probe(ctx, sys.Command{Name: "getent", Args: []string{"group", runners.RunnersGroup}}) {
		if _, err := b.run(ctx, "group", sys.Command{Name: "groupadd", Args: []string{"--system", runners.RunnersGroup}}); err != nil {
			return err
		}
	}
	if b.probe(ctx, sys.Command{Name: "getent", Args: []string{"passwd", user}}) {
		return nil
	}
	args := []string{"--system", "--gid", runners.RunnersGroup, "--no-create-home", "--home-dir", folder,
		"--shell", "/usr/sbin/nologin"}
	// A runner keeps one uid for life, from the reserved range, so a user made
	// again owns its old folder and caches (runners R5.72).
	if b.cfg.IDs != nil {
		id, err := b.cfg.IDs.ID(ctx, user)
		if err != nil {
			return fmt.Errorf("user id: %w", err)
		}
		args = append(args, "--uid", strconv.Itoa(id))
	}
	_, err := b.run(ctx, "user", sys.Command{Name: "useradd", Args: append(args, user)})
	return err
}

// configure runs the runner's config.sh as the user. The token goes in the
// environment of that one process, not in an argument.
func (b *Backend) configure(ctx context.Context, step, folder, user string, args []string, token string) error {
	argv := append([]string{"-u", user, "--", folder + "/config.sh"}, args...)
	_, err := b.run(ctx, step, sys.Command{
		Name: "runuser", Args: argv, Dir: folder,
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

// Stop stops the unit. Callers drain first and check for an idle runner right
// before (runners R6.4).
func (b *Backend) Stop(ctx context.Context, h backend.Handle) error {
	f, ok, err := b.lookup(ctx, h.Ref)
	if err != nil {
		return err
	}
	folder := ""
	if ok {
		folder = f.Folder
	}
	return b.stopUnit(ctx, h.Ref+".service", folder)
}

// stopUnit stops the unit and waits until the listener of the runner folder is
// gone. A listener that outlives its unit is reported, not ignored (R5.46).
func (b *Backend) stopUnit(ctx context.Context, unit, folder string) error {
	if _, err := b.run(ctx, "stop", sys.Command{Name: "systemctl", Args: []string{"stop", unit}}); err != nil {
		return err
	}
	return b.noListener(ctx, folder)
}

// restartUnit is stop, wait for the listener to end, start.
func (b *Backend) restartUnit(ctx context.Context, unit, folder string) error {
	if err := b.stopUnit(ctx, unit, folder); err != nil {
		return err
	}
	_, err := b.run(ctx, "start", sys.Command{Name: "systemctl", Args: []string{"start", unit}})
	return err
}

func (b *Backend) noListener(ctx context.Context, folder string) error {
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

// guardStart refuses to start the service while a listener of the runner
// folder runs (R5.59).
func (b *Backend) guardStart(ctx context.Context, folder string) error {
	return persistent.CheckNoListener(ctx, func(ctx context.Context) ([]int, error) {
		return persistent.UnixListeners(ctx, b.d.Cmd, folder)
	})
}

// Deregister removes the runner from GitHub with a remove token and checks the
// result; on failure the unit is started again and nothing else is touched.
func (b *Backend) Deregister(ctx context.Context, h backend.Handle, removeToken string) error {
	f, ok, err := b.lookup(ctx, h.Ref)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	unit := h.Ref + ".service"
	if err := b.stopUnit(ctx, unit, f.Folder); err != nil {
		return err
	}
	if err := b.configure(ctx, "deregister", f.Folder, f.Account, []string{"remove"}, removeToken); err != nil {
		_, _ = b.d.Cmd.Run(ctx, sys.Command{Name: "systemctl", Args: []string{"start", unit}})
		return fmt.Errorf("%w: %v", ErrRemovalFailed, err)
	}
	return nil
}

// CleanUp removes the unit, the user and the folder; safe to call twice
// (runners R5.10).
func (b *Backend) CleanUp(ctx context.Context, h backend.Handle) error {
	f, ok, err := b.lookup(ctx, h.Ref)
	if err != nil {
		return err
	}
	unit := h.Ref + ".service"
	var errs []error
	_, _ = b.d.Cmd.Run(ctx, sys.Command{Name: "systemctl", Args: []string{"disable", "--now", unit}})
	if err := b.d.FS.RemoveAll(b.unitPath(h.Ref)); err != nil {
		errs = append(errs, err)
	}
	if _, err := b.run(ctx, "daemon-reload", sys.Command{Name: "systemctl", Args: []string{"daemon-reload"}}); err != nil {
		errs = append(errs, err)
	}
	if ok {
		if strings.HasPrefix(f.Account, runners.AccountPrefix) && b.probe(ctx, sys.Command{Name: "getent", Args: []string{"passwd", f.Account}}) {
			if _, err := b.run(ctx, "remove user", sys.Command{Name: "userdel", Args: []string{f.Account}}); err != nil {
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
	res, err := b.d.Cmd.Run(ctx, sys.Command{Name: "journalctl", Args: []string{"-u", h.Ref + ".service", "-n", "200", "--no-pager", "-o", "cat"}})
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

// ReportUsage reads the unit's CPU time and memory from systemd.
func (b *Backend) ReportUsage(ctx context.Context, h backend.Handle) (backend.Usage, error) {
	res, err := b.d.Cmd.Run(ctx, sys.Command{Name: "systemctl", Args: []string{"show", h.Ref + ".service", "-p", "CPUUsageNSec", "-p", "MemoryCurrent"}})
	if err != nil {
		return backend.Usage{}, err
	}
	var u backend.Usage
	for _, l := range strings.Split(res.Stdout, "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(l), "=")
		var n int64
		if _, err := fmt.Sscan(v, &n); err != nil {
			continue
		}
		switch k {
		case "CPUUsageNSec":
			u.CPU = time.Duration(n)
		case "MemoryCurrent":
			u.MemoryPeak = n
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
