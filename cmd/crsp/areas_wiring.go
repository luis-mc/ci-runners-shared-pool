package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/agentlink"
	"github.com/luis-mc/ci-runners-shared-pool/internal/api"
	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/registry"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backup"
	"github.com/luis-mc/ci-runners-shared-pool/internal/caches"
	"github.com/luis-mc/ci-runners-shared-pool/internal/config"
	"github.com/luis-mc/ci-runners-shared-pool/internal/configexport"
	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor/checks"
	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor/wire"
	"github.com/luis-mc/ci-runners-shared-pool/internal/events"
	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/internal/listenaddr"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/internal/notify"
	"github.com/luis-mc/ci-runners-shared-pool/internal/overview"
	"github.com/luis-mc/ci-runners-shared-pool/internal/repos"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler"
	"github.com/luis-mc/ci-runners-shared-pool/internal/secrets"
	"github.com/luis-mc/ci-runners-shared-pool/internal/settings"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
	"github.com/luis-mc/ci-runners-shared-pool/internal/tlsca"
	"github.com/luis-mc/ci-runners-shared-pool/internal/webhookhealth"
)

const (
	envPoolName     = "CRSP_POOL_NAME"
	defaultPoolName = "crsp-home"
	// runsOnRestoreEvery is how often the RUNS_ON variables are put back to
	// what the policy wants (github-integration R5.28).
	runsOnRestoreEvery = 10 * time.Minute
	// runsOnCheckEvery is how often the runs-on workflow check runs for every
	// enabled repository (github-integration R5.30).
	runsOnCheckEvery = time.Hour
)

// controllerDeps is everything buildController needs that runController (or a
// test) makes first. runController and the wiring guard test both call
// buildController, so the test checks the wiring the binary runs.
type controllerDeps struct {
	Cfg     config.Config
	Logger  *slog.Logger
	DB      *store.Store
	Secrets secrets.Store
	Ops     *backup.Ops
	// Machines is the machines service; CA the controller's certificate authority.
	Machines *machines.Service
	CA       api.CAProvider
	// PublicHost is the name runners reach the controller at; the parity setup writes it (CRSP141).
	PublicHost string
	// Packages serves the release packages to new machines (machines R5.65); nil for a development build.
	Packages api.PackageServer
	Updates  api.UpdateService
	// Listen is the listen address service of the console (operations R5.90).
	Listen *listenaddr.Service
	Queue  scheduler.SlotQueue
	// Caches is the caches service of the machines runtime (web-console R5.25).
	Caches *caches.Service
	// Link carries runner commands to remote machines and reports (CRSP63); nil keeps remote machines unreachable.
	Link *agentlink.Link
	// MachineSecrets is the secret source the config export reads (mr.Secrets).
	MachineSecrets secrets.Store
	// Backends are the runner backends of the controller's own OS; nil means none.
	Backends backend.Registry
	Now      func() time.Time
}

// controller is the built controller: the API options, the setup link printed
// at start, and the pieces runController also needs.
type controller struct {
	Options api.Options
	// SetupLink is the one-time first-run link; empty when an owner exists.
	SetupLink string
	Auth      *auth.Service
	Repos     *repos.Service
	Hub       *events.MemHub
	// Registry is the registry join (REG11); never nil in a built controller.
	Registry *registryWiring
	closers  []func()
}

// Close stops what buildController started.
func (c *controller) Close() {
	for i := len(c.closers) - 1; i >= 0; i-- {
		c.closers[i]()
	}
}

// buildController wires the GitHub area, the runners service and the doctor
// beside what runController made, and starts their background work under ctx
// (the RUNS_ON restore loop, the first installation sync).
func buildController(ctx context.Context, d controllerDeps) (*controller, error) {
	if d.Now == nil {
		d.Now = time.Now
	}
	logger := d.Logger
	console := consoleURL(d.Cfg.ListenAddr)
	pool := os.Getenv(envPoolName)
	if pool == "" {
		pool = defaultPoolName
	}
	c := &controller{}

	// GitHub: client, sign-in, repositories, webhook.
	gh := github.New(github.Config{APIBase: d.Cfg.Addresses.GitHubAPIURL, WebBase: d.Cfg.Addresses.GitHubURL, Secrets: d.Secrets, Logger: logger})
	api.SetGitHubAddresses(d.Cfg.Addresses)
	// The repository state is kept in the database: the pool switch of a
	// repository must survive a restart, such as an upgrade (CRSP148).
	repoStore, err := repos.NewPersistentStore(ctx, d.DB.ReposState())
	if err != nil {
		return nil, err
	}
	c.closers = append(c.closers, func() {
		if err := repoStore.Flush(); err != nil {
			logger.Warn("save the repositories", "err", err)
		}
	})
	authStore := d.DB.Auth()
	cbSettings := settings.New(d.DB)
	health := webhookhealth.New()
	asvc := auth.New(authStore, auth.Config{
		LoadWebhookAddress: cbSettings.GetWebhookAddress,
		SaveWebhookAddress: cbSettings.SetWebhookAddress,
		HookHealth:         health,
		GitHub:             gh, Secrets: d.Secrets, Now: d.Now, WebBase: gh.WebBase(),
		// An App made before the list was kept has the loopback callback
		// of this port only (OPS16): never the address chosen later.
		ConsoleURLs:    func() []string { return []string{loopbackConsoleURL(console)} },
		LoadRegistered: cbSettings.GetCallbackOrigins,
		SaveRegistered: cbSettings.SetCallbackOrigins,
		OnApp:          func(a auth.AppRecord) { gh.UseApp(a.ID, a.ClientID) },
	})
	if st, err := asvc.State(ctx); err == nil && st.HasApp {
		gh.UseApp(st.App.ID, st.App.ClientID)
	}
	rsvc := repos.New(repos.Config{Addresses: d.Cfg.Addresses, Store: repoStore, GitHub: gh, Tokens: asvc, Audit: asvc, PoolName: pool,
		Capacity: machineCapacity{svc: d.Machines, pool: pool}, Sizes: machineCapacity{svc: d.Machines, pool: pool}, Up: machineUp{svc: d.Machines},
		FallbackGrace: fallbackGrace(d), FactsReported: factsReported(d, d.Now()), PublicRepositories: publicRepositories(d), Now: d.Now})
	// An App made without a webhook has no webhook secret in the store (R5.3):
	// no delivery can arrive, so polling finding a job is not a missed delivery.
	health.HooksOn = func() bool {
		app := gh.AppID()
		if app == 0 {
			return false
		}
		b, err := d.Secrets.Get(context.Background(), github.SecretKey(app, github.SecretWebhookSecret))
		return err == nil && len(b) > 0
	}
	appHealth := &github.AppHealth{Client: gh, AppID: gh.AppID, Now: d.Now}
	// The Jobs section: every job event ends in one recorder (observability R5.7 to R5.12).
	jobsIngest := newJobsWiring(ctx, logger, d.DB.Jobs(), gh, repoStore, d.Now)
	c.closers = append(c.closers, jobsIngest.Close)
	hook := &github.WebhookHandler{
		Verifier: func() (github.WebhookVerifier, error) {
			app := gh.AppID()
			if app == 0 {
				return nil, errors.New("no GitHub App yet")
			}
			b, err := d.Secrets.Get(context.Background(), github.SecretKey(app, github.SecretWebhookSecret))
			if err != nil {
				return nil, err
			}
			return github.NewWebhookVerifier(b), nil
		},
		FirstDelivery: newDeliveryLog(4096).first,
		Events:        webhookEvents{ctx: ctx, logger: logger, repos: rsvc, appGone: appHealth.Gone, jobs: jobsIngest},
		OnDelivery: func(repo string, jobID int64, used bool) {
			if used {
				health.Delivered(repo, jobID)
			} else {
				health.Record(repo, webhookhealth.ResultFailed)
			}
		},
	}
	c.Options.GitHub = &api.GitHubArea{Auth: asvc, Repos: rsvc, Webhook: hook, WebBase: gh.WebBase(), Logger: logger, Health: health, AppHealth: appHealth,
		HasMachine: func(ctx context.Context) bool { vs, err := d.Machines.List(ctx); return err == nil && len(vs) > 0 }}
	c.Auth, c.Repos = asvc, rsvc
	if cmp, err := newCompareService(gh, repoStore); err == nil {
		c.Options.Compare = cmp
	} else {
		logger.Warn("speed comparison is off", "err", err)
	}

	// App health: GET /app at start and every hour; while the App or its key
	// is gone, GitHub work stops and the console says so (R5.6).
	healthCtx, stopHealth := context.WithCancel(ctx)
	healthDone := make(chan struct{})
	go func() {
		defer close(healthDone)
		appHealth.Run(healthCtx, nil,
			func(st github.AppState) {
				if st.Gone {
					logger.Error("the GitHub App or its key is gone; GitHub work is stopped", "reason", st.Reason)
				} else {
					logger.Info("the GitHub App answers again; GitHub work resumes")
				}
			},
			func(err error) { logger.Warn("check the GitHub App", "err", err) })
	}()
	c.closers = append(c.closers, func() { stopHealth(); <-healthDone })

	// Live view over an empty source until the store adapter is wired (CRSP23).
	hub, live := wire.NewLive(overview.EmptySource{}, events.Options{})
	c.Hub = hub
	c.closers = append(c.closers, hub.Stop)

	// Runners: the controller's own OS through the backend registry; other
	// machines wait for the runner messages on the agent link.
	reg := d.Backends
	if reg == nil {
		reg = registry.Platform(realDeps(nil, secretRedactor.Redact, secretRedactor.Add), runnerIDSource(logger, d.DB))
	}
	// Every change to a runner record is told to the live view, so the console list moves without a reload.
	durableRunners, err := runners.OpenDurableStore(ctx, d.DB.RunnerPersister())
	if err != nil {
		return nil, err
	}
	rstore := runners.NotifyStore{Store: durableRunners, OnChange: func(r runners.Record, gone bool) {
		if b := api.RunnerEventJSON(r, gone); b != nil {
			hub.Publish(events.RunnerState, b)
		}
	}}
	agent := linkAgent{local: runners.NewLocalAgent(reg), svc: d.Machines, remote: remoteAgent(d.Link)}
	runnerSvc := &runners.Service{
		Store: rstore, Machines: machineLookup{svc: d.Machines}, Resolver: scopeResolver{repos: repoStore, auth: asvc, addr: d.Cfg.Addresses},
		GitHub: gh, Agent: agent, Audit: runnerAudit{auth: asvc},
		PoolName: pool, CacheQuota: d.Cfg.Cache, Now: d.Now, Services: agent.Services,
		// Shared tool folders with group access, one build folder per runner (caches R5.57).
		SharedToolsRoot: os.Getenv("CRSP_SHARED_TOOLS_ROOT"), NoSharedTools: os.Getenv("CRSP_NO_SHARED_TOOLS") != "",
		// A public repository gets no new runner while the setting is off (github-integration R5.55).
		// A plan with no scope takes the first repository with the pool on (D31, until OPS23).
		DefaultScope: func(ctx context.Context) (runners.Scope, error) {
			on := true
			rs, err := repoStore.ListRepositories(ctx, repos.ListFilter{Pool: &on})
			if err != nil {
				return runners.Scope{}, err
			}
			if len(rs) == 0 {
				return runners.Scope{}, runners.ErrNoPoolRepository
			}
			return runners.Scope{Kind: "repository", RepositoryID: rs[0].ID}, nil
		},
		Gate: func(ctx context.Context, sc runners.Scope) error {
			if sc.RepositoryID == 0 {
				return nil
			}
			return rsvc.RequirePublicAllowedByID(ctx, sc.RepositoryID)
		},
	}

	// Automatic runner updates follow the product's automatic-update setting
	// (runners R5.38, operations R5.15). The Updater sends the bundle over the
	// agent link; an agent that does not report `runner_swap` answers with a
	// reason and is sent nothing. No command or flag reads this setting.
	if d.Link != nil {
		runnerSvc.Updater = &agentlink.Updater{L: d.Link, Bundles: pinBundles{svc: d.Machines, addr: d.Cfg.Addresses}}
		// The clean-up reports of persistent runners reach the runner service (runners R5.90, R5.91).
		d.Link.Hygiene = runnerSvc
	}
	if d.Ops != nil && d.Ops.Settings != nil {
		set := d.Ops.Settings
		runnerSvc.AutoUpdate = func(ctx context.Context) bool {
			s, err := set.Get(ctx)
			return err != nil || s.AutoUpdate
		}
	}

	// Light runners are gone (decision D110); the reconcile removes the ones an
	// older version made.
	runnerSvc.LogInfo = func(line string) { logger.Info(line) }
	// An unclean runner takes no new job (runners R5.91).
	if q, ok := d.Queue.(interface {
		SetUncleanLookup(func(machineID, runnerName string) string)
	}); ok {
		q.SetUncleanLookup(func(machineID, name string) string { return runnerSvc.Unclean(ctx, machineID, name) })
	}
	// Long-lived runners serve every repository that uses the pool: one hidden
	// registration for each pool repository, made and removed with the pool
	// switch and by the reconcile loop (runners R5.88).
	runnerSvc.PoolRepos = poolRepos(repoStore)
	rsvc.SetEphemeral(runnerSvc)
	rsvc.SetRegistrar(runnerSvc)
	rsvc.SetFleet(runnerSvc)

	// RUNS_ON variables: put back to the policy every 10 minutes, and once now.
	loopCtx, stopLoop := context.WithCancel(ctx)
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		runsOnLoop(loopCtx, logger, realClock{}, githubLoopTasks(gh, rsvc, runnerSvc, appHealth.Gone))
	}()
	c.closers = append(c.closers, func() { stopLoop(); <-loopDone })

	// Demand: poll queued jobs of the repositories with the pool on and start one
	// ephemeral container runner per job on a machine whose allow list names
	// the container backend (github-integration R5.37, runners R5.27, R5.31).
	stopDemand, quietDemand := startDemand(ctx, logger, gh, demandEnv{repos: repoStore, machines: d.Machines, appGone: appHealth.Gone}, runnerSvc, pool, d.Cfg.DemandQuiet, d.Cfg.ScaleSetDemand, health.PollSawQueued, jobsIngest.Job)
	// crsp's own facts about each job: wait reason, slot, machine, runner (observability R5.7).
	jobsIngest.attach(d.Queue, runnerSvc, d.Machines)
	startSweep(ctx, logger, runnerSvc, d.Cfg.RunnerSweepDays)
	c.closers = append(c.closers, stopDemand)

	// Runner leftovers (doctor R5.48): the machines service asks the agents, and
	// builds what this controller owns from the runner records.
	d.Machines.SetOwnedRunners(ownedRunners(rstore))
	d.Machines.SetLeftoverSources(localLeftoverSource(logger), remoteLeftoverSource(d.Link))

	// Doctor: the M1 check registry over the controller's own data.
	_, docSvc := wire.NewDoctor(wire.DoctorDeps{
		Fleet: doctorFleet{machines: d.Machines, runners: rstore, repos: repoStore, rsvc: rsvc},
		GitHub: func(context.Context) (doctor.GitHub, error) {
			if gh.AppID() == 0 {
				return nil, nil
			}
			return gh, nil
		},
		Store: doctor.NewMemStore(), Events: hub, Logger: logger, PoolName: pool,
		KVM:       reportSources{fleet: doctorFleet{machines: d.Machines}, link: d.Link},
		Engines:   reportSources{fleet: doctorFleet{machines: d.Machines}, link: d.Link},
		Extra:     checks.KVMReportedChecks(),
		Demand:    quietSource{wd: quietDemand},
		Ephemeral: runnerSvc,
	})

	c.Options.Version, c.Options.DB, c.Options.Logger = version, d.DB, logger
	c.Options.Settings, c.Options.Audit, c.Options.Backup = d.Ops.Settings, d.Ops.Audit, d.Ops.Backup
	c.Options.Machines, c.Options.CA, c.Options.Update = d.Machines, d.CA, d.Updates
	c.Options.Packages = d.Packages
	c.Options.PublicHost, c.Options.ListenAddr = d.PublicHost, d.Cfg.ListenAddr
	if d.Listen != nil {
		c.Options.Listen = d.Listen
	}
	c.Options.Jobs, c.Options.Slots, c.Options.Live = d.DB, d.Queue, live
	c.Options.JobsData = d.DB.Jobs()
	c.Options.Runners, c.Options.Doctor = runnerSvc, docSvc
	c.Options.Caches, c.Options.Events = d.Caches, hub
	c.Options.Trial = newTrial(d.DB, rsvc, gh, machineCapacity{svc: d.Machines, pool: pool})
	c.Options.Notify = notify.New(notify.Config{DB: d.DB, Secrets: d.Secrets, Logger: logger})
	c.Options.Config = configexport.Source{
		Version: version, Config: d.Cfg, Settings: d.Ops.Settings, Secrets: d.MachineSecrets,
		Named: []configexport.NamedSecret{
			{Name: "Controller certificate authority key", Key: tlsca.CAKeySecret},
			{Name: "Controller server key", Key: tlsca.ServerKeySecret},
		},
	}

	// The image registry parts (registry REG11): build records, profiles, key
	// choice, and the registry routes when a registry core is present.
	if c.Registry, err = wireRegistry(ctx, d, c, gh, &repoLookup{store: repoStore}, hub); err != nil {
		c.Close()
		return nil, fmt.Errorf("wire the image registry: %w", err)
	}

	// First run: a one-time setup link while there is no owner (github-integration R5.1).
	code, err := asvc.NewSetupCode(ctx)
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("make the setup link: %w", err)
	}
	if code != "" {
		c.SetupLink = setupLink(console, code)
		logger.Info("first run: open this one-time setup link to connect GitHub", "link", c.SetupLink)
		_, _ = fmt.Fprintf(os.Stderr, "First run: open this one-time setup link to connect GitHub:\n  %s\n", c.SetupLink)
	}
	return c, nil
}

// setupLink is the address of the setup page with its one-time code.
func setupLink(console, code string) string {
	return strings.TrimRight(console, "/") + "/setup?code=" + url.QueryEscape(code)
}

// loopClock makes tickers; a test passes a fake one.
type loopClock interface {
	// NewTicker returns the tick channel and the stop function.
	NewTicker(d time.Duration) (<-chan time.Time, func())
}

type realClock struct{}

func (realClock) NewTicker(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

// runsOnTasks is what the GitHub loops call. A rate-limit error stops the
// round and waits for the next tick (R6.6).
type runsOnTasks struct {
	// Ready is false until a GitHub App exists; the loops do nothing then.
	Ready func() bool
	// Reconcile syncs installations and puts the RUNS_ON variables back; it runs
	// every runsOnRestoreEvery.
	Reconcile []func(ctx context.Context) (name string, err error)
	// Check reads the workflow files of every enabled repository; it runs every
	// runsOnCheckEvery.
	Check func(ctx context.Context) error
	// Fallback evaluates the hosted fallback; it runs every fallbackEvery.
	Fallback func(ctx context.Context) error
}

// runsOnLoop syncs installations and puts the RUNS_ON variables back every 10
// minutes, and runs the runs-on workflow check of every enabled repository
// every hour, until ctx ends. It does nothing until a GitHub App exists.
func runsOnLoop(ctx context.Context, logger *slog.Logger, clk loopClock, t runsOnTasks) {
	restore := func() {
		if !t.Ready() {
			return
		}
		for _, f := range t.Reconcile {
			if name, err := f(ctx); err != nil && ctx.Err() == nil {
				logger.Warn(name, "err", err)
			}
		}
	}
	// The hosted fallback looks at the machines every half minute; it writes
	// only for a repository that opted in (github-integration R5.43).
	fallback := func() {
		if !t.Ready() || t.Fallback == nil {
			return
		}
		if err := t.Fallback(ctx); err != nil && ctx.Err() == nil {
			logger.Warn("hosted fallback", "err", err)
		}
	}
	check := func() {
		if !t.Ready() || t.Check == nil {
			return
		}
		if err := t.Check(ctx); err != nil && ctx.Err() == nil {
			logger.Warn("runs-on workflow check", "err", err)
		}
	}
	restore()
	check()
	fallback()
	restoreC, stopRestore := clk.NewTicker(runsOnRestoreEvery)
	defer stopRestore()
	checkC, stopCheck := clk.NewTicker(runsOnCheckEvery)
	defer stopCheck()
	fallbackC, stopFallback := clk.NewTicker(fallbackEvery)
	defer stopFallback()
	for {
		select {
		case <-ctx.Done():
			return
		case <-restoreC:
			restore()
		case <-checkC:
			check()
		case <-fallbackC:
			fallback()
		}
	}
}

// fallbackEvery is how often the controller looks at the machines for the
// hosted fallback.
const fallbackEvery = 30 * time.Second

// fallbackGrace reads the grace time of the hosted fallback from the settings.
func fallbackGrace(d controllerDeps) func() time.Duration {
	return func() time.Duration {
		if d.Ops == nil || d.Ops.Settings == nil {
			return 0
		}
		st, err := d.Ops.Settings.Get(context.Background())
		if err != nil {
			return 0
		}
		return time.Duration(st.FallbackGraceMinutes) * time.Minute
	}
}

// lightRemover is what the reconcile needs to remove the light runners an older
// version made.
type lightRemover interface {
	RemoveLegacyLight(ctx context.Context, actor string) (runners.LegacyLightResult, error)
}

func removeLightRunners(ctx context.Context, r lightRemover) error {
	if r == nil {
		return nil
	}
	_, err := r.RemoveLegacyLight(ctx, "controller")
	return err
}

// githubLoopTasks wires the loops to the repository service.
func githubLoopTasks(gh *github.REST, rsvc *repos.Service, runnerSvc lightRemover, appGone func() bool) runsOnTasks {
	return runsOnTasks{
		Ready: func() bool { return gh.AppID() != 0 && !appGone() },
		Reconcile: []func(context.Context) (string, error){
			func(ctx context.Context) (string, error) {
				return "reconcile installations", rsvc.ReconcileInstallations(ctx)
			},
			func(ctx context.Context) (string, error) {
				return "restore RUNS_ON variables", rsvc.ReconcileAllVariables(ctx)
			},
			// After the variables: no workflow points at a light runner while
			// the runners are drained and removed (decision D110).
			func(ctx context.Context) (string, error) {
				return "remove light runners", removeLightRunners(ctx, runnerSvc)
			},
		},
		Check:    rsvc.CheckAllRunsOn,
		Fallback: rsvc.EvaluateFallback,
	}
}

// deliveryLog remembers recent webhook delivery ids so a repeat is ignored (R5.36).
type deliveryLog struct {
	mu    sync.Mutex
	seen  map[string]bool
	order []string
	max   int
}

func newDeliveryLog(max int) *deliveryLog { return &deliveryLog{seen: map[string]bool{}, max: max} }

func (l *deliveryLog) first(id, _ string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen[id] {
		return false, nil
	}
	l.seen[id] = true
	l.order = append(l.order, id)
	if len(l.order) > l.max {
		delete(l.seen, l.order[0])
		l.order = l.order[1:]
	}
	return true, nil
}

// webhookEvents receives verified deliveries. An installation change starts a
// reconcile; job events are logged until the scheduler takes them from here.
type webhookEvents struct {
	ctx    context.Context
	logger *slog.Logger
	repos  *repos.Service
	// appGone stops the reconcile while the App or its key is gone (R5.6).
	appGone func() bool
	// jobs records the job and its steps for the Jobs section; nil records nothing.
	jobs jobSink
}

// jobSink takes job events without blocking.
type jobSink interface {
	Job(github.Job)
	JobDetail(github.JobEvent)
}

func (w webhookEvents) Job(j github.Job) {
	w.logger.Debug("workflow_job event", "repository", j.Repository, "job", j.ID, "status", j.Status)
	if w.jobs != nil {
		w.jobs.Job(j)
	}
}

// JobDetail gets the steps and run fields of the same event, after Job.
func (w webhookEvents) JobDetail(ev github.JobEvent) {
	if w.jobs != nil {
		w.jobs.JobDetail(ev)
	}
}

// Repository stops serving a repository that turned public (CRSP82). The
// stop runs in its own goroutine so GitHub gets its answer at once.
func (w webhookEvents) Repository(action, fullName string) {
	if action != "publicized" {
		return
	}
	w.logger.Info("repository turned public", "repository", fullName)
	go func() {
		if err := w.repos.Publicized(github.Background(w.ctx), fullName); err != nil && w.ctx.Err() == nil {
			w.logger.Warn("stop serving a public repository", "repository", fullName, "err", err)
		}
	}()
}

func (w webhookEvents) Installation(event string, body []byte) {
	w.logger.Info("installation event", "event", event)
	if w.appGone != nil && w.appGone() {
		return
	}
	// new_permissions_accepted: the cached installation token is dropped, so
	// the next call carries the permissions just accepted (CRSP139, R5.90).
	accepted := int64(0)
	if ev, err := github.ParseInstallationEvent(body); err == nil && ev.Action == github.NewPermissionsAccepted {
		accepted = ev.InstallationID
	}
	go func() {
		if accepted != 0 {
			if err := w.repos.PermissionsAccepted(w.ctx, accepted); err != nil && w.ctx.Err() == nil {
				w.logger.Warn("new permissions accepted", "err", err)
			}
			return
		}
		if err := w.repos.ReconcileInstallations(w.ctx); err != nil && w.ctx.Err() == nil {
			w.logger.Warn("reconcile installations", "err", err)
		}
	}()
}

// setupOrConsole is the tray's link issuer: while no owner exists the link
// carries the one-time setup code; after that it is the one-time sign-in link
// of the signed-in OS user (next, TRAY1), or the plain console address.
type setupOrConsole struct {
	auth    *auth.Service
	console string
	// next makes the link once an owner exists; nil gives the plain address.
	next interface {
		LinkFor(ctx context.Context, osUser, target string) (string, error)
	}
}

func (s setupOrConsole) LinkFor(ctx context.Context, osUser string, target string) (string, error) {
	if code, err := s.auth.NewSetupCode(ctx); err == nil && code != "" {
		return setupLink(s.console, code), nil
	}
	if s.next != nil {
		return s.next.LinkFor(ctx, osUser, target)
	}
	u, err := url.Parse(s.console)
	if err != nil {
		return "", errors.New("the console address is not valid")
	}
	if len(target) > 0 && target[0] == '/' && (len(target) == 1 || target[1] != '/') {
		u.Path = target
	}
	return u.String(), nil
}

// publicRepositories reads the setting Public repositories (github-integration
// R5.54). Without the operations area it is off.
func publicRepositories(d controllerDeps) func(context.Context) (bool, error) {
	if d.Ops == nil || d.Ops.Settings == nil {
		return nil
	}
	return d.Ops.Settings.PublicRepositoriesOn
}
