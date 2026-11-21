// Command crsp manages shared self-hosted GitHub Actions runners.
//
// Modes: controller (config, GitHub, scheduling, web app and API), agent (runs
// on every machine and dials the controller), tray (comes later). Every other
// command is the command line for people and scripts, in internal/cli. A mode
// is added with one line in the Modes map of run.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api"
	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/container"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backup"
	"github.com/luis-mc/ci-runners-shared-pool/internal/cli"
	"github.com/luis-mc/ci-runners-shared-pool/internal/config"
	"github.com/luis-mc/ci-runners-shared-pool/internal/hooks"
	"github.com/luis-mc/ci-runners-shared-pool/internal/jobcmd"
	"github.com/luis-mc/ci-runners-shared-pool/internal/listenaddr"
	"github.com/luis-mc/ci-runners-shared-pool/internal/logging"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/internal/registry/mirror"
	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler"
	"github.com/luis-mc/ci-runners-shared-pool/internal/secrets"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
	"github.com/luis-mc/ci-runners-shared-pool/internal/tray"
	"github.com/luis-mc/ci-runners-shared-pool/internal/update"
	"github.com/luis-mc/ci-runners-shared-pool/internal/updatewindow"
	"github.com/luis-mc/ci-runners-shared-pool/internal/webui"
)

// version is set at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

// baseContext is the context the controller runs under. A Windows service
// replaces it with one that the service manager's Stop cancels.
var baseContext = context.Background()

func main() {
	container.ProductVersion = version
	if runAsService() {
		return
	}
	hideConsoleForTray(os.Args[1:])
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "hook" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return hooks.Run(ctx, args[1:], hooks.Env{Stdout: stdout, Stderr: stderr})
	}
	if len(args) > 0 && jobcmd.Handles(args[0]) {
		// `crsp vm` and `crsp db-env` inside a container job (runners R5.48).
		return jobcmd.Run(args[0], args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "update-helper" {
		// The detached install helper (internal/update); it outlives the service it restarts.
		return update.RunHelper(context.Background(), args[1:], stderr)
	}
	if len(args) > 0 && args[0] == tray.UpdateWindowCommand {
		// Hidden: the tray starts a copy of this program with it for the native update window (Windows, TRAY5).
		return updatewindow.Run(updatewindow.Options{Args: args[1:], Service: tray.NewClient(), Stdout: stdout, Stderr: stderr})
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return cli.Run(&cli.Env{
		Stdin:       os.Stdin,
		Stdout:      stdout,
		Stderr:      stderr,
		IsTerminal:  stdinIsTerminal,
		OpenBrowser: openBrowser,
		Version:     version,
		GoVersion:   goVersion(),
		Context:     ctx,
		Modes: map[string]cli.ModeFunc{
			"controller": func(context.Context, []string) error { return runController() },
			"agent": func(_ context.Context, a []string) error {
				return runAgentCmd(a, stdout, stderr)
			},
			"tray": func(_ context.Context, a []string) error {
				if code := tray.Run(a, tray.Options{Version: version, Stdout: stdout, Stderr: stderr}); code != 0 {
					return cli.Exit(code, "the tray could not start")
				}
				return nil
			},
		},
	}, args)
}

func goVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		return info.GoVersion
	}
	return runtime.Version()
}

func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// runController runs the controller until a signal or a staged restore ends it.
// After a restore it returns an error, so the exit code is non-zero and the
// service manager starts the controller again.
func runController() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	logger := logging.New(os.Stderr, cfg.LogLevel, secretRedactor)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(baseContext, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// A restore that was staged before the last exit goes in place now, before
	// the database opens (operations R5.25).
	if applied, err := backup.ApplyPending(cfg.DataDir, cfg.File); err != nil {
		return fmt.Errorf("apply the staged restore: %w", err)
	} else if applied {
		logger.Info("restore applied")
	}

	db, err := store.Open(ctx, cfg.DataDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	var restarting atomic.Bool
	ops := backup.Start(ctx, backup.StartOptions{
		DB: db, DataDir: cfg.DataDir, ConfigFile: cfg.File, Version: version, Logger: logger,
		Restart: func() {
			// Exit non-zero after the answer is sent; the service manager restarts us.
			restarting.Store(true)
			time.AfterFunc(time.Second, stop)
		},
		DrillHandler: func(drillDB *store.Store) http.Handler {
			m := http.NewServeMux()
			api.Register(m, api.Options{Version: version, DB: drillDB, Logger: logger})
			return m
		},
	})

	// The address chosen in the console, unless the environment sets one
	// (operations R5.90).
	if stored, err := ops.Settings.GetListenAddress(ctx); err != nil {
		logger.Warn("read the stored listen address", "err", err)
	} else {
		cfg.ListenAddr, cfg.ListenFromSetting = listenaddr.Effective(os.Getenv(config.EnvListenAddr), stored.Address, cfg.ListenAddr)
	}
	listen := &listenaddr.Service{Settings: ops.Settings, Source: listenaddr.SystemSource{}, Getenv: os.Getenv, Agent: cfg.AgentAddr}

	idle := &idleHook{}
	mr, err := startMachines(ctx, cfg, logger, idle, audit.New(db), db, poolDefaults(ops.Settings))
	if err != nil {
		return err
	}
	defer mr.Close()
	// A drain names the job that holds it and flags one far past its usual
	// duration, from job history (machines R5.55, R5.56).
	mr.Service.SetTypical(usualDuration(db))
	// The Docker disk compact looks at the queue before it holds a machine, and
	// checks its policy every 5 minutes (caches R5.51 to R5.54).
	mr.Service.SetQueueSource(db)
	go mr.Service.RunCompactLoop(ctx, compactEvery, func(err error) { logger.Warn("docker disk compact pass", "err", err) })

	um := startUpdates(ctx, cfg, logger, mr, ops.Settings, idle)
	// A machine that is paused or on battery holds new jobs with
	// that reason (scheduling R5.68, machines R5.62).
	queue := scheduler.NewEngine(db, scheduler.WithLogger(logger), scheduler.WithAccepting(func(id string) (bool, string) {
		p := mr.Service.Policy(id)
		return p.Accepting, p.PauseReason
	}))
	go scheduler.RunSweeper(ctx, queue, logger, 30*time.Second)
	// The agent's holder report is the heartbeat of the slot leases of its running jobs (scheduling R5.70).
	mr.Service.SetRunningObserver(func(id string, jobs []machines.JobRef) {
		refs := make([]scheduler.JobRef, 0, len(jobs))
		for _, j := range jobs {
			refs = append(refs, scheduler.JobRef{Repository: j.Repository, RunID: j.RunID, RunAttempt: j.RunAttempt,
				Job: j.Job, GitHubJobID: j.GitHubJobID, RunnerName: j.RunnerName})
		}
		if err := queue.RenewJobs(ctx, id, refs); err != nil && ctx.Err() == nil {
			logger.Warn("slot lease renewal failed", "machine", id, "err", err)
		}
	})

	sec, err := secrets.Open(ctx, cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open the secret store: %w", err)
	}
	sec = secrets.Learn(sec, secretRedactor.Add)
	ctl, err := buildController(ctx, controllerDeps{
		Cfg: cfg, Logger: logger, DB: db, Secrets: sec, Ops: ops,
		Machines: mr.Service, CA: mr.CA, PublicHost: mr.PublicHost, Packages: packageServer(mr), Updates: um, Listen: listen, Queue: queue, Caches: mr.Caches, Link: mr.Link, MachineSecrets: mr.Secrets,
	})
	if err != nil {
		return err
	}
	defer ctl.Close()

	// The console and its API are plain HTTP on 127.0.0.1 and ::1 only, and
	// HTTPS only on every other address (web-console R5.1), with the
	// certificate of the controller's own CA, the same one the agent link uses.
	// A chosen address that cannot be bound falls back to loopback, and the
	// console says why. A non-loopback address keeps 127.0.0.1 too, so the local
	// console and the tray keep working.
	listenAt, fallback := cfg.ListenAddr, ""
	ln, err := webui.Listen(ctx, listenAt)
	if err != nil && cfg.ListenFromSetting {
		_, port, _ := net.SplitHostPort(listenAt)
		fallback = "the controller could not listen on " + listenAt + " (" + err.Error() + "), so it listens on 127.0.0.1 only; choose another address"
		logger.Warn("listen address not bound", "addr", listenAt, "err", err)
		listenAt = net.JoinHostPort("127.0.0.1", port)
		ln, err = webui.Listen(ctx, listenAt)
	}
	if err != nil {
		return err
	}
	listen.Running, listen.Fallback = listenAt, fallback
	var localLn net.Listener
	if lh, lp, _ := net.SplitHostPort(listenAt); !webui.IsLoopback(lh) && !isWildcard(lh) {
		if localLn, err = webui.Listen(ctx, net.JoinHostPort("127.0.0.1", lp)); err != nil {
			logger.Warn("the local console address is not open", "err", err)
			localLn = nil
		}
	}
	localAddr := listenAt
	if localLn != nil {
		localAddr = localLn.Addr().String()
	}

	// The one-time link the tray's Open asks for over the local channel (tray R5.6 to R5.9, R6.1).
	console := consoleURL(localAddr)
	startTrayChannel(ctx, logger, trayBackend{
		Updates: um, Drain: machineDrainer{svc: mr.Service}, Console: console,
		Links:  setupOrConsole{auth: ctl.Auth, console: console, next: authLinks{svc: ctl.Auth, console: console}},
		Digest: func() string { return latestDigest(cfg.DataDir) },
	})

	// The pull-through mirror of upstream bases, under its own route prefix (registry R5.63).
	// The registry core guards the mirror with its token check; without a core
	// the mirror has no guard and answers 401 to everyone.
	var mirrorGuard mirror.Guard
	if ctl.Registry.Core != nil {
		mirrorGuard = ctl.Registry.Core.MirrorGuard
	}
	mirrorSvc, mirrorProxy := startRegistryMirror(ctx, logger, cfg.DataDir, db, audit.New(db), mirrorGuard)
	defer func() { _ = mirrorProxy.Close() }()
	ctl.Options.RegistryMirror = mirrorSvc
	// CI run insights: routes and the analyser engine (ci-run-insights R5.42).
	startInsights(ctx, logger, &ctl.Options, insightsDeps{Area: &api.InsightsArea{Resources: db.Resources(), Peaks: db.Resources()}})

	mux := http.NewServeMux()
	api.Register(mux, ctl.Options)
	mux.Handle(mirror.PathPrefix, mirrorProxy.Handler())
	// The image registry itself, on the same listener (registry R5.1): only
	// when a registry core is present; people never talk to /v2/.
	if ctl.Registry.Core != nil && ctl.Registry.Core.Registry != nil {
		mux.Handle("/v2/", ctl.Registry.Core.Registry.Handler())
	}
	mux.Handle("/", webui.Handler())

	handler := api.Recover(logger, mux)
	srv := webui.NewServer(listenAt, mr.CA.ServerTLS(), handler)
	srv.ErrorLog = webui.ServerErrorLog(logger)
	servers := []*http.Server{srv}
	errc := make(chan error, 2)
	go func() {
		logger.Info("controller listening", "addr", listenAt, "version", version, "tls", !webui.IsLoopback(listenAt))
		errc <- webui.Serve(srv, ln)
	}()
	if localLn != nil {
		local := webui.NewServer(localAddr, nil, handler)
		local.ErrorLog = webui.ServerErrorLog(logger)
		servers = append(servers, local)
		go func() {
			logger.Info("controller listening locally", "addr", localAddr)
			errc <- webui.Serve(local, localLn)
		}()
	}

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, sv := range servers {
			if err := sv.Shutdown(shutdownCtx); err != nil {
				return err
			}
		}
		if restarting.Load() {
			return errors.New("restarting to apply a restore")
		}
		return nil
	}
}

// isWildcard reports an unspecified listen host, which already covers 127.0.0.1.
func isWildcard(h string) bool {
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return h == "" || ip != nil && ip.IsUnspecified()
}
