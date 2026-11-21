package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/agentlink"
	"github.com/luis-mc/ci-runners-shared-pool/internal/api"
	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/events"
	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsize"
	"github.com/luis-mc/ci-runners-shared-pool/internal/profiles"
	"github.com/luis-mc/ci-runners-shared-pool/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/internal/registry/builds"
	"github.com/luis-mc/ci-runners-shared-pool/internal/registry/core"
	"github.com/luis-mc/ci-runners-shared-pool/internal/registry/keys"
	"github.com/luis-mc/ci-runners-shared-pool/internal/registry/mirror"
	"github.com/luis-mc/ci-runners-shared-pool/internal/registry/records"
	"github.com/luis-mc/ci-runners-shared-pool/internal/registry/replace"
	"github.com/luis-mc/ci-runners-shared-pool/internal/registry/retention"
	"github.com/luis-mc/ci-runners-shared-pool/internal/registry/scan"
	"github.com/luis-mc/ci-runners-shared-pool/internal/registry/sink"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runnercache"
	"github.com/luis-mc/ci-runners-shared-pool/internal/secrets"
	"github.com/luis-mc/ci-runners-shared-pool/internal/stackscan"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

// The join of the registry wave (REG11) and the registry core (REG12). The
// core (REG1: Distribution under /v2/, the token issuer, the promoter, the
// disk gate, the event log) is opened by openRegistryCore. Without it (the
// listen address is not a private LAN one, R5.2, or it did not start) there is
// no registry: agents are told so, the registry routes answer 501, and the
// controller runs as it did. What does not need the core is wired at once: the
// build records (IMG1), the image profiles (IMG3) and the key choice over the
// stack scan (IMG2 into REG3).

// registryTokenKey is the secret-store entry of the key that signs registry
// tokens (registry R5.10).
const registryTokenKey secrets.Key = "crsp/registry/token-key"

// leasePrune is how often the token leases that ended are deleted.
const leasePrune = 5 * time.Minute

// registryCore is what REG1 hands the controller.
type registryCore struct {
	// Registry is the mounted Distribution instance.
	Registry registry.Registry
	// Tokens issues and checks the machine tokens.
	Tokens registry.TokenIssuer
	// Promoter promotes a pushed image by a manifest copy.
	Promoter registry.Promoter
	// Layers lists the layers of a manifest in the storage (records R5.31).
	Layers records.LayerSource
	// Referrers stores the SBOM and the scan report as OCI referrers (REG8);
	// nil leaves the scan without referrers.
	Referrers scan.Referrers
	// AddPromotedHook registers a hook the Promoter calls after each promotion.
	AddPromotedHook func(registry.PromotedHook)
	// MirrorGuard puts the token check in front of the pull-through mirror (R5.3).
	MirrorGuard mirror.Guard
	// budget is the gate REG6's budgets are wired into; nil in a test.
	budget *budgetGate
	// close stops the registry and its background work; nil in a test.
	close func()
}

// registryCoreDeps is what the controller gives the core to open.
type registryCoreDeps struct {
	DB         *store.Store
	Images     registry.ImageStore
	Secrets    secrets.Store
	DataDir    string
	ListenAddr string
	Host       string
	Logger     *slog.Logger
}

// openRegistryCore opens the registry core; nil means this build has none. It
// is a variable so a test can stand in for the core.
var openRegistryCore = openCore

// registryAvailable says whether a registry core can be opened; the agent
// handshake offers protocol version 3 (the registry path) only then.
func registryAvailable() bool { return openRegistryCore != nil }

// tokenKey reads the key that signs registry tokens, making it on the first start.
func tokenKey(ctx context.Context, sec secrets.Store) ([]byte, error) {
	k, err := sec.Get(ctx, registryTokenKey)
	if err == nil && len(k) >= 32 {
		return k, nil
	}
	if err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return nil, err
	}
	k = make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	return k, sec.Put(ctx, registryTokenKey, k)
}

// openCore builds REG1's core: the token issuer with its disk gate, the
// Distribution registry on the private listener address, the promoter, the
// event log and the pruning of ended token leases. It returns nil without an
// error when the listen address is not a private LAN one (R5.2): the controller
// then runs with no registry and agents build locally.
func openCore(ctx context.Context, d registryCoreDeps) (*registryCore, error) {
	logger := d.Logger
	if err := core.CheckListen(d.ListenAddr); err != nil {
		logger.Info("the image registry is not served", "addr", d.ListenAddr, "why", err.Error())
		return nil, nil
	}
	folder := filepath.Join(d.DataDir, "registry")
	if st, err := d.DB.RegistryRetention().Settings(ctx); err == nil && st.Folder != "" {
		folder = st.Folder
	}
	key, err := tokenKey(ctx, d.Secrets)
	if err != nil {
		return nil, fmt.Errorf("the token key: %w", err)
	}
	ledger := d.DB.RegistryCore()
	budget := newBudgetGate(d.DB.RegistryRetention(), logger)
	issuer := &core.Issuer{Secret: key, Leases: ledger, Gate: &core.DiskGate{
		Folder: folder, OverBudget: budget.OverBudget, Refused: budget.Refused,
	}}
	reg, err := core.New(ctx, core.Options{Dir: folder, ListenAddr: d.ListenAddr, Tokens: issuer, Logger: d.Logger})
	if err != nil {
		return nil, err
	}
	promoter := &core.Promoter{Registry: reg, Ledger: ledger, Log: logger, Room: budget.Room}
	runCtx, stop := context.WithCancel(ctx)
	go core.LogEvents(runCtx, reg.Events(), logger)
	pruned := make(chan struct{})
	go func() {
		defer close(pruned)
		t := time.NewTicker(leasePrune)
		defer t.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case now := <-t.C:
				if _, err := ledger.PruneLeases(runCtx, now); err != nil && runCtx.Err() == nil {
					logger.Warn("registry: could not prune the token leases", "err", err)
				}
			}
		}
	}()
	logger.Info("image registry ready", "addr", d.Host)
	return &registryCore{
		Registry: reg, Tokens: issuer, Promoter: promoter, Layers: reg,
		AddPromotedHook: promoter.OnPromoted, MirrorGuard: issuer.MirrorGuard, budget: budget,
		close: func() { stop(); <-pruned; _ = reg.Close() },
	}, nil
}

// registryWiring is what the join built; the controller keeps it for the
// routes it mounts and for shutdown.
type registryWiring struct {
	// Core is nil without a registry core.
	Core *registryCore
	// Keyer chooses the image key of a job (REG3 over IMG3 and IMG2).
	Keyer *keys.Keyer
	// Stacks is the stack scan at a run's commit (IMG2).
	Stacks *stackscan.Scanner
	// Builds keeps the image build records (IMG1).
	Builds *builds.Recorder
}

// registryHost is host:port of the registry on the controller's HTTPS listener.
func registryHost(listenAddr, publicHost string) string {
	_, port, err := net.SplitHostPort(listenAddr)
	if err != nil || publicHost == "" {
		return ""
	}
	return net.JoinHostPort(publicHost, port)
}

// wireRegistry builds the registry parts, sets the API options of the parts
// that are ready and starts their background work under ctx. A nil gh (a test)
// makes the stack scan report every repository as not served.
func wireRegistry(ctx context.Context, d controllerDeps, c *controller, gh *github.REST, lookup *repoLookup, pub events.Publisher) (*registryWiring, error) {
	logger := d.Logger.With("component", "registry")
	db := d.DB
	w := &registryWiring{}

	// IMG3: the standard profiles are there from the first start.
	if err := profiles.Seed(ctx, db.Profiles()); err != nil {
		logger.Warn("the standard image profiles were not seeded", "err", err)
	}

	// IMG1: the build records. Agents report their builds over the link; the
	// read routes show them.
	w.Builds = &builds.Recorder{Q: db.ImageBuilds(), Events: pub}
	c.Options.Builds = w.Builds
	if d.Link != nil {
		d.Link.Builds = w.Builds
	}

	// IMG2 into REG3: the key of a job comes from its profile (IMG3) and the
	// stack files at its commit (IMG2).
	var reader stackscan.Reader = stackscan.GitHub{C: gh, Target: func(ctx context.Context, repository string) (int64, error) {
		if gh == nil || lookup == nil {
			return 0, errNoRegistryRepo
		}
		r, ok := lookup.find(ctx, repository)
		if !ok {
			return 0, errNoRegistryRepo
		}
		return r.InstallationID, nil
	}}
	w.Stacks = stackscan.New(reader)

	// OPS67: the job size scan reads the same commit files; the sizes feed the
	// scheduler through the store and the console through the API.
	sizeSvc := &jobsize.Service{Store: db.JobSizes(), Scanner: &jobsize.Scanner{R: reader}, Now: d.Now}
	c.Options.JobSizes = sizeSvc
	c.sizes.set(sizeSvc.ScanRepo)
	w.Keyer = &keys.Keyer{Profiles: &profiles.Resolver{Store: db.Profiles()}, Stacks: w.Stacks}

	// The source label of an image names the GitHub of this controller.
	keys.RepoPage = d.Cfg.Addresses.RepoPage

	if openRegistryCore == nil {
		return w, nil
	}
	host := registryHost(d.Cfg.ListenAddr, d.PublicHost)
	// core is set once the registry opens; the overview reads it to say whether
	// the registry runs.
	var core *registryCore
	rs := records.New(db.RegistryQueries(), records.Config{
		Host: host, DefaultFolder: filepath.Join(d.Cfg.DataDir, "registry"), Logger: logger,
		Running: func(context.Context) (bool, string) {
			if core == nil || core.Registry == nil {
				return false, "The image registry is not running."
			}
			return true, ""
		},
	})
	images := records.NewImageStore(rs)
	opened, err := openRegistryCore(ctx, registryCoreDeps{
		DB: db, Images: images, Secrets: d.Secrets, DataDir: d.Cfg.DataDir, ListenAddr: d.Cfg.ListenAddr, Host: host, Logger: logger,
	})
	if err != nil {
		// The controller runs without the registry; agents build locally.
		logger.Warn("the image registry did not start", "err", err)
		return w, nil
	}
	if opened == nil {
		return w, nil
	}
	core = opened
	w.Core = core
	if core.close != nil {
		c.closers = append(c.closers, core.close)
	}

	audits := audit.New(db)
	pointers := &keys.Pointers{Lineage: db.RegistryKeys(), Images: images}
	detector := &keys.Detector{Pointers: pointers, Images: images, Recs: db.RegistryRetention()}
	scanner := scan.New(db.RegistryQueries(), core.Referrers, nil)

	// What happens after each promotion: layer accounting (REG4), update-of
	// detection and the pointer rules (REG3), the scan queue (REG8). A hook
	// error is logged by the core and never undoes the promotion.
	if core.AddPromotedHook != nil {
		if core.Layers != nil {
			core.AddPromotedHook(rs.PromotedHook(core.Layers))
		}
		core.AddPromotedHook(detector.OnPromoted)
		core.AddPromotedHook(scanner.Hook())
	}

	var reg retention.Registry = noRegistry{}
	if core.Registry != nil {
		reg = core.Registry
	}
	ret := retention.New(retention.Options{
		Repo: db.RegistryRetention(), Registry: reg, Disk: registryDisk{}, Audit: api.RetentionAuditor(audits),
		Profiles: profileExists{db.Profiles()}, Logger: logger, DataDir: d.Cfg.DataDir,
	})
	if core.budget != nil {
		core.budget.set(ret)
	}
	repl := replace.New(replace.Config{
		Images: images, Pointers: db.RegistryKeys(), Plans: db, Settings: db.RegistryRetention(),
		Stacks: w.Stacks, Logger: logger,
	})
	if err := repl.Recover(ctx); err != nil {
		logger.Warn("replace plans that were running were not stopped", "err", err)
	}

	c.Options.RegistryRead = rs
	c.Options.RegistryRetention = ret
	c.Options.RegistryReplace = repl
	if d.Link != nil {
		d.Link.Registry = &sink.Sink{
			Locator: rs, Tokens: core.Tokens, Recorder: rs, Promoter: core.Promoter, Outcomes: rs,
			Profiles: db.Profiles(), Host: host, Logger: logger,
		}
	}

	// Background work: pull metrics from the registry events, the retention
	// tick, and the end of what is running when the controller stops.
	loopCtx, stopLoop := context.WithCancel(ctx)
	done := make(chan struct{})
	if core.Registry != nil {
		go func() { rs.Consume(loopCtx, core.Registry.Events()) }()
	}
	go func() {
		defer close(done)
		retentionLoop(loopCtx, logger, ret, realClock{})
	}()
	c.closers = append(c.closers, func() { stopLoop(); <-done; ret.Wait() })
	return w, nil
}

// retentionLoop calls Tick every retentionTickEvery until ctx ends.
func retentionLoop(ctx context.Context, logger *slog.Logger, ret *retention.Service, clk loopClock) {
	tick, stop := clk.NewTicker(retentionTickEvery)
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick:
			if err := ret.Tick(ctx, now); err != nil && ctx.Err() == nil {
				logger.Warn("registry retention tick", "err", err)
			}
		}
	}
}

var errNoRegistryRepo = errors.New("repository is not served")

// registryDisk gives the free bytes of a volume to the retention rules.
type registryDisk struct{}

func (registryDisk) Free(path string) (int64, error) {
	d, err := runnercache.OSDisk(path)
	if err != nil {
		return 0, err
	}
	return d.Free, nil
}

// profileExists tells retention whether an image profile still exists.
type profileExists struct{ s profiles.Store }

func (p profileExists) Exists(ctx context.Context, id string) (bool, error) {
	_, err := p.s.Get(ctx, id)
	if errors.Is(err, profiles.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// noRegistry stands for the registry core in the retention service while there
// is none: nothing is deleted and nothing is collected.
type noRegistry struct{}

func (noRegistry) SetReadOnly(bool) {}
func (noRegistry) DeleteManifest(context.Context, string, string) error {
	return errors.New(agentlink.ErrNoRegistry)
}
func (noRegistry) GC(context.Context, bool) (int64, error) {
	return 0, errors.New(agentlink.ErrNoRegistry)
}

// Tick every few minutes: the recommendations and the weekly cleanup.
const retentionTickEvery = 5 * time.Minute
