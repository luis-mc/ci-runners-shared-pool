package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/internal/agentlink"
	"github.com/luis-mc/ci-runners-shared-pool/internal/api"
	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/caches"
	"github.com/luis-mc/ci-runners-shared-pool/internal/config"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/internal/packages"
	"github.com/luis-mc/ci-runners-shared-pool/internal/secrets"
	"github.com/luis-mc/ci-runners-shared-pool/internal/settings"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
	"github.com/luis-mc/ci-runners-shared-pool/internal/tlsca"
	"github.com/luis-mc/ci-runners-shared-pool/internal/update"
	"github.com/luis-mc/ci-runners-shared-pool/internal/webui"
)

// Environment names of the agent link, beside the CRSP_* configuration.
const (
	envAgentAddr  = "CRSP_AGENT_ADDR"  // where the agent link listens, default 0.0.0.0:8461
	envPublicHost = "CRSP_PUBLIC_HOST" // the name agents use for this controller
	defaultAgent  = "0.0.0.0:8461"
)

// compactEvery is how often the Docker disk compact policy is looked at.
const compactEvery = 5 * time.Minute

// machinesRuntime is the machines area of a running controller.
type machinesRuntime struct {
	Service *machines.Service
	Caches  *caches.Service
	// Link carries runner commands to remote agents and holds their machine reports.
	Link *agentlink.Link
	CA   *tlsca.Authority
	// PublicHost is the name agents and runners use for this controller (CRSP_PUBLIC_HOST, else the host name).
	PublicHost string
	// Packages holds the release packages that downloaded installers fetch; nil for a development build.
	Packages *packages.Store
	// Secrets is the secret store of the controller, read by the config export.
	Secrets secrets.Store
	stop    func()
}

// Close stops the link, the controller's own agent and the periodic work.
func (m *machinesRuntime) Close() { m.stop() }

// startMachines makes the controller's CA, serves the agent link (TLS 1.3, port
// 8461), and starts the controller's own agent, which joins without a code over
// loopback like any other agent (machines R5.1, R5.2, decision 0004).
func startMachines(ctx context.Context, cfg config.Config, logger *slog.Logger, idle *idleHook, rec audit.Recorder, db *store.Store, pool func(context.Context) (machines.PoolDefaults, error)) (*machinesRuntime, error) {
	secretsDir := filepath.Join(cfg.DataDir, "secrets", "controller")
	store, err := agent.NewFileStore(secretsDir)
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	public := os.Getenv(envPublicHost)
	listenHost := concreteListenHost(cfg.ListenAddr)
	if public == "" {
		public = host
		// The address chosen in the console is the one runners reach.
		if cfg.ListenFromSetting && listenHost != "" {
			public = listenHost
		}
	}
	// The certificate covers every name and address the controller is reached at,
	// the chosen listen address too; a certificate that lacks one is made again.
	hosts := []string{public, host}
	if listenHost != "" {
		hosts = append(hosts, listenHost)
	}
	ca, err := tlsca.LoadOrCreate(ctx, tlsca.Options{Dir: filepath.Join(cfg.DataDir, "tls"), Secrets: store, Hosts: hosts})
	if err != nil {
		return nil, err
	}
	addr := os.Getenv(envAgentAddr)
	if addr == "" {
		addr = defaultAgent
	}
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, err
	}

	// The release packages a downloaded installer fetches (machines R5.65): only
	// for a stable release build, and only when the console is reachable from
	// another machine.
	var pkgs *packages.Store
	var pkgSource machines.PackageSource
	consoleURL := ""
	if _, ok := update.ParseVersion(version); ok {
		pkgs = &packages.Store{Source: update.GitHubSource{Base: cfg.Addresses.GitHubAPIURL, Version: version}, Dir: filepath.Join(cfg.DataDir, "packages")}
		pkgSource = pkgs
		if lh, lp, err := net.SplitHostPort(cfg.ListenAddr); err == nil && !webui.IsLoopback(lh) {
			consoleURL = "https://" + net.JoinHostPort(public, lp)
		}
	}

	// An SSH join to Windows downloads from the controller's own package
	// endpoint when there is one, else from the GitHub release (CRSP123).
	var winPackages machines.WindowsPackageSource = releasePackages{version: version, apiBase: cfg.Addresses.GitHubAPIURL}
	if pkgSource != nil && consoleURL != "" {
		winPackages = controllerPackages{sums: pkgSource, console: consoleURL}
	}

	// The pool lives in the database, so an update or a restart keeps every machine, setting and key (OPS59).
	repo, err := openMachineRepo(ctx, logger, cfg.DataDir, db, rec)
	if err != nil {
		return nil, err
	}
	svc := machines.New(machines.Options{
		Repo: repo, ControllerAddress: public, AgentPort: port, Fingerprint: ca.Fingerprint(),
		OnIdle: idle.call, PoolDefaults: pool, SSH: machines.NewSSHRunner(), MinProtocol: minAgentProtocol,
		Packages: pkgSource, ConsoleURL: consoleURL, WindowsPackages: winPackages,
	})
	cs := caches.New(caches.Options{Machines: svc, Audit: rec})
	lk := agentlink.New(svc)
	svc.SetHostRunner(lk)
	svc.SetHooks(machines.Hooks{ControllerVersion: version,
		// Protocol version 3, the registry path, is offered only when a registry core can open (REG11).
		Registry: registryAvailable(),
		// Protocol version 4: the agents send the CPU and memory of each job (OPS66, ci-run-insights R5.19).
		OnJobSamples: agentlink.ResourceSink{Batches: db.Resources()}.JobSamples,
		OnHello:      func(ctx context.Context, id string, h *agentv1.Hello) { cs.OnHello(ctx, id, h); lk.OnHello(id) },
		OnMessage: func(ctx context.Context, id string, m *agentv1.SessionRequest, reply func(*agentv1.SessionResponse)) bool {
			return lk.OnMessage(id, m) || cs.OnMessage(ctx, id, m, reply)
		}})

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	mountRecovery(svc, ca, cfg.Addresses.GitHubAPIURL, version)
	link := machines.NewLinkServer(addr, ca, svc)
	go func() {
		logger.Info("agent link listening", "addr", ln.Addr().String())
		if err := link.Serve(tls.NewListener(ln, ca.ServerTLS())); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("agent link stopped", "err", err)
		}
	}()

	runCtx, cancel := context.WithCancel(ctx)
	go svc.Run(runCtx, 5*time.Second, func(err error) { logger.Warn("machines tick", "err", err) })

	// The controller's own machine: the same Join and Session, over loopback.
	agentDir := filepath.Join(cfg.DataDir, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		cancel()
		_ = link.Close()
		return nil, err
	}
	credFile := filepath.Join(agentDir, "local-join")
	if err := writeLocalCredential(ctx, svc, credFile); err != nil {
		cancel()
		_ = link.Close()
		return nil, err
	}
	agentStore, err := agent.NewFileStore(filepath.Join(agentDir, "secrets"))
	if err != nil {
		cancel()
		_ = link.Close()
		return nil, err
	}
	loop := net.JoinHostPort("127.0.0.1", portStr)
	done := make(chan struct{})
	// A separately installed crsp-agent on this host that already joined wins:
	// the built-in agent is skipped, so the PC is one machine (R5.1).
	if name, dup, derr := svc.SeparateAgentOnHost(ctx, host); derr == nil && dup {
		logger.Info("the controller's own agent is skipped: this computer is already in the pool through a separately installed agent", "machine", name)
		close(done)
	} else {
		go refreshLocalCredential(runCtx, logger, svc, credFile)
		go func() {
			defer close(done)
			acfg := agent.Config{
				Controller: loop, Pin: ca.Fingerprint(), LocalCredentialFile: credFile, Secrets: agentStore, DataDir: agentDir,
				Version: version, Logger: logger.With("component", "local-agent"), Caches: agentCacheSource(), DiskFloorGiB: diskFloorFor(cfg),
				Images: agentImageSource(), Redactor: secretRedactor,
			}
			if err := agent.Run(runCtx, acfg); err != nil {
				if strings.Contains(err.Error(), machines.LocalDuplicateMarker) {
					logger.Info("the controller's own agent is skipped: this computer is already in the pool through a separately installed agent", "reason", err.Error())
					return
				}
				logger.Error("the controller's own agent stopped", "err", err)
			}
		}()
	}
	return &machinesRuntime{Service: svc, Caches: cs, Link: lk, CA: ca, PublicHost: public, Packages: pkgs, Secrets: store, stop: func() {
		cancel()
		_ = link.Close()
		<-done
	}}, nil
}

// poolDefaults gives machines the Budgets settings for a machine that joins.
func poolDefaults(set *settings.Service) func(context.Context) (machines.PoolDefaults, error) {
	return func(ctx context.Context) (machines.PoolDefaults, error) {
		p, err := set.GetPool(ctx)
		if err != nil {
			return machines.PoolDefaults{}, err
		}
		return machines.PoolDefaults{
			BudgetMemoryPct: p.BudgetMemoryPct, BudgetCPUPct: p.BudgetCPUPct,
			SizeSlotsFromMachine: p.SizeSlotsFromMachine, JobSlots: p.DefaultJobSlots,
			DrainTimeoutMinutes: p.DrainTimeoutMinutes,
		}, nil
	}
}

// diskFloorFor turns the controller's setting into the agent's: the config
// holds 0 for "no floor", the agent takes a negative value for that.
func diskFloorFor(cfg config.Config) int {
	if cfg.DiskFloorGiB <= 0 {
		return -1
	}
	return cfg.DiskFloorGiB
}

// localCredentialEvery is how often the one-time credential file is renewed. The
// credential lives ten minutes; the agent reads the file again whenever the
// controller refuses its key, so the file never holds an expired one (R5.1).
const localCredentialEvery = 5 * time.Minute

// writeLocalCredential makes a one-time local credential and writes it, mode
// 0600, where the controller's own agent reads it.
func writeLocalCredential(ctx context.Context, svc *machines.Service, path string) error {
	cred, err := svc.LocalCredential(ctx)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(cred), 0o600)
}

func refreshLocalCredential(ctx context.Context, logger *slog.Logger, svc *machines.Service, path string) {
	t := time.NewTicker(localCredentialEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := writeLocalCredential(ctx, svc, path); err != nil && ctx.Err() == nil {
				logger.Warn("could not renew the local credential", "err", err)
			}
		}
	}
}

// packageServer is the API's view of the package store; a nil store stays a nil interface.
func packageServer(mr *machinesRuntime) api.PackageServer {
	if mr == nil || mr.Packages == nil {
		return nil
	}
	return mr.Packages
}

// concreteListenHost is the host of a listen address that names one address of
// this machine: not loopback, not a wildcard, not empty.
func concreteListenHost(listen string) string {
	h, _, err := net.SplitHostPort(listen)
	if err != nil {
		return ""
	}
	h = strings.Trim(h, "[]")
	if ip := net.ParseIP(h); ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return ""
	}
	return h
}

// openMachineRepo opens the database-backed pool and, once, imports the
// machines.json an older controller wrote: the file is renamed to a backup and
// never deleted (OPS59). Every machine action also goes to the console audit log.
func openMachineRepo(ctx context.Context, logger *slog.Logger, dataDir string, db *store.Store, rec audit.Recorder) (machines.Repository, error) {
	d, err := machines.OpenDurableRepo(ctx, db.MachinePersister())
	if err != nil {
		return nil, err
	}
	file := filepath.Join(dataDir, machines.MachinesFile)
	snap, ok, err := machines.ReadSnapshotFile(file)
	if err != nil {
		return nil, err
	}
	if ok {
		imported, err := d.Import(ctx, snap)
		if err != nil {
			return nil, err
		}
		if imported {
			backup := file + ".imported-" + time.Now().UTC().Format("20060102T150405Z")
			if err := os.Rename(file, backup); err != nil {
				logger.Warn("machines.json was imported but could not be renamed", "err", err)
			}
			_ = d.Audit(ctx, "system", "machine.import", "machines.json", fmt.Sprintf("%d machines imported, file kept as a backup", len(snap.Machines)))
			logger.Info("machines imported from machines.json into the database", "machines", len(snap.Machines))
		}
	}
	return auditedRepo{DurableRepo: d, rec: rec}, nil
}

// auditedRepo writes the audit entries of the machines service to the console
// audit log as well as to the repository's own list.
type auditedRepo struct {
	*machines.DurableRepo
	rec audit.Recorder
}

func (a auditedRepo) Audit(ctx context.Context, actor, action, subject, detail string) error {
	_ = a.DurableRepo.Audit(ctx, actor, action, subject, detail)
	src := audit.SourceSystem
	if actor == "agent" {
		src = audit.SourceAgent
	}
	return a.rec.Record(ctx, audit.Actor{Login: actor, Source: src}, action, audit.Target{Type: "machine", ID: subject}, map[string]any{"summary": detail})
}
