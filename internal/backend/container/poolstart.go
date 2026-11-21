package container

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/addresses"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/internal/poolsize"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runnercache"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runners"
)

// The pool start joins what the earlier tasks built (parity plan, What is
// left item 1): the cache lane (caches R5.43, R5.44), the job environment
// (runners R5.53), the Wine prefix (R5.52), the `crsp` mount (R5.48), KVM
// (R5.41), Postgres and db.env (R5.54) and the browsers path (R5.44). Each
// piece turns on by a condition, and a required piece that fails stops the
// start, so no runner is left behind a half-made pool.

// Layer names the pool start reacts to.
const (
	layerKVM      = "kvm"
	layerBrowsers = runners.LayerBrowsers
)

// DBEnvFileName is the file in a runner's folder that holds its database
// settings. It holds a secret and is written with mode 0600 (runners R5.54).
const DBEnvFileName = "db.env"

// ErrNoLane is returned when the pool needs a cache lane the machine cannot
// give.
var ErrNoLane = errors.New("the pool needs a cache lane and none is set up on this machine")

// ErrNoDatabase is returned when a repository declares a database and this
// machine has no way to start one.
var ErrNoDatabase = errors.New("the repository declares a database, but this machine has no secret store for the Postgres passwords")

// Database is the Postgres of a pool as the start uses it. *runners.Postgres
// is the real one; a test passes a fake.
type Database interface {
	EnsureServer(ctx context.Context) error
	Provision(ctx context.Context, s runners.DBSpec) (runners.DBAccess, error)
	Remove(ctx context.Context, runnerID string) error
}

// KVMFacts is what the machine says about /dev/kvm.
type KVMFacts struct {
	Exists bool
	// Usable is true when the device opens for read and write.
	Usable bool
	// GID is the group that owns the device; 0 means unknown.
	GID int
}

// PoolPlan is everything the start decides from the runner spec and the
// machine facts, before any engine call. It has no secret.
type PoolPlan struct {
	Pool PoolSpec
	// WinePrefix is set (a path inside the container) when the Wine prefix is
	// made before the first job.
	WinePrefix string
	Layers     []string
	// RunnerEnv is the environment of one runner's listener: per runner, not
	// per pool.
	RunnerEnv map[string]string
	// Database is set when the repository declares one.
	Database *backend.PoolDatabase
	// KVMNote says why the pool has no KVM device although the layer is on.
	KVMNote string
}

func hasLayer(layers []string, name string) bool {
	for _, l := range layers {
		if l == name {
			return true
		}
	}
	return false
}

// PlanPool turns the runner spec, the backend's config and the machine's
// KVM facts into the plan of one pool start. It is a pure function: the same
// input gives the same arguments, in the same order.
func PlanPool(cfg Config, r backend.RunnerSpec, owner, repo string, kvm KVMFacts) (PoolPlan, error) {
	layers := runners.NormalizeLayers(r.Pool.Layers)
	p := PoolSpec{Owner: owner, Repo: repo, ImageTag: r.Image.ID, ImageKey: keyOf(r.Image.ID), Env: map[string]string{}}
	p.Limits = cfg.Limits
	if r.PoolMemoryMB > 0 {
		p.Limits.MemoryMB = r.PoolMemoryMB
	}
	if r.PoolCPUs > 0 {
		p.Limits.CPUs = r.PoolCPUs
	}
	plan := PoolPlan{Layers: layers, RunnerEnv: map[string]string{}, Database: r.Pool.Database}

	iso, err := ParseIsolation(r.Pool.Isolation)
	if err != nil {
		return PoolPlan{}, err
	}
	p.Isolation = iso

	// The crsp binary mount (R5.48).
	p.CrspHostPath = cfg.CrspHostPath

	// KVM: the device only with the kvm layer and a device the runner can
	// open, or when the machine config forces it (R5.41).
	switch {
	case cfg.KVM:
		p.KVM = true
	case hasLayer(layers, layerKVM):
		d := DecideKVM(kvm.Exists, kvm.Usable, false)
		p.KVM = d.Device
		plan.KVMNote = d.Reason
	}
	if p.KVM && kvm.GID > 0 {
		p.KVMGroup = kvm.GID
	}

	// The cache lane (caches R5.43, R5.44) is taken when the pool container is
	// made; here only the need is set.
	wantLane := cfg.CacheRoot != "" && r.Pool.RepoID != ""
	p.LaneRepoID = ""
	if wantLane {
		p.LaneRepoID = r.Pool.RepoID
	}

	// Browsers (R5.44): the layer's image path.
	if hasLayer(layers, layerBrowsers) {
		for k, v := range runnercache.BrowsersEnv("", true) {
			p.Env[k] = v
		}
	}
	// Wine (R5.52): the prefix lives in the lane.
	if runnersNeedWine(layers) {
		if !wantLane {
			return PoolPlan{}, fmt.Errorf("%w: the %s layer keeps its Wine prefix in the lane", ErrNoLane, WindowsPackagingLayer)
		}
		for k, v := range LaneWineJobEnv() {
			p.Env[k] = v
		}
		plan.WinePrefix = LaneInside.Wine
	}

	// The job environment of one runner (R5.53): CPU count, cache quotas.
	if r.MaxParallel > 0 {
		for k, v := range poolsize.Env(r.MaxParallel) {
			plan.RunnerEnv[k] = v
		}
	}
	if wantLane {
		q := runnercache.Env("", r.CacheQuota)
		for _, k := range []string{runnercache.EnvToolQuota, runnercache.EnvRustCap, runnercache.EnvRustKeyCap,
			runnercache.EnvBudget, runnercache.EnvAlert} {
			plan.RunnerEnv[k] = q[k]
		}
	}
	if r.Pool.Database != nil {
		plan.RunnerEnv["CRSP_DB_ENV_FILE"] = jobDir(r.Name) + "/" + DBEnvFileName
	}
	if cfg.VMImageMirror != "" {
		plan.RunnerEnv[addresses.EnvVMImageMirror] = cfg.VMImageMirror
	}
	plan.Pool = p
	return plan, nil
}

func runnersNeedWine(layers []string) bool { return NeedsWinePrefix(layers) }

// envArgs turns an environment map into sorted `-e K=V` arguments.
func envArgs(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var a []string
	for _, k := range keys {
		a = append(a, "-e", k+"="+env[k])
	}
	return a
}

// kvmFacts reads the machine's KVM facts, waiting a bounded time for the
// device when the pool has the kvm layer and it is not there yet (R5.41).
func (b *Backend) kvmFacts(ctx context.Context, layers []string) KVMFacts {
	if b.cfg.KVMProbe == nil || b.cfg.KVM || !hasLayer(layers, layerKVM) {
		return KVMFacts{}
	}
	f := b.cfg.KVMProbe()
	if !f.Exists {
		w := KVMWait{Exists: func() bool { f = b.cfg.KVMProbe(); return f.Exists }, Sleep: b.d.Sleep,
			Interval: b.cfg.KVMWaitInterval, Bound: b.cfg.KVMWaitBound, Log: func(m string) { b.log("start", m) }}
		_ = w.Wait(ctx)
	}
	return f
}

// database returns the Postgres of a pool, or ErrNoDatabase.
func (b *Backend) database(ctx context.Context, pool string) (Database, error) {
	if b.cfg.Database != nil {
		return b.cfg.Database(ctx, pool)
	}
	if b.cfg.OpenSecrets == nil {
		return nil, ErrNoDatabase
	}
	st, err := b.cfg.OpenSecrets(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoDatabase, err)
	}
	return &runners.Postgres{Engine: b.eng, Cmd: b.d.Cmd, Secrets: st, Owners: &b.owners, Pool: pool}, nil
}

// connectDB puts the pool container on the Postgres network, so it reaches the
// server by name. A container that is on it already is not an error.
func (b *Backend) connectDB(ctx context.Context, pool string) error {
	net := "crsp-net-" + pool
	c, err := b.eng.Command("network", "connect", net, pool)
	if err != nil {
		return err
	}
	res, err := b.d.Cmd.Run(ctx, c)
	if err != nil {
		return fmt.Errorf("database network: %s", b.redact(err.Error()))
	}
	if res.ExitCode != 0 && !strings.Contains(strings.ToLower(res.Stderr), "already exists") &&
		!strings.Contains(strings.ToLower(res.Stderr), "already connected") {
		return fmt.Errorf("database network failed (exit %d): %s", res.ExitCode, b.redact(strings.TrimSpace(res.Stderr)))
	}
	return nil
}

// provisionDB makes the login and database of one runner and writes db.env
// into the runner's folder, readable by the job user only.
func (b *Backend) provisionDB(ctx context.Context, db Database, pool, owner, repo string, r backend.RunnerSpec) error {
	acc, err := db.Provision(ctx, runners.DBSpec{Repo: owner + "/" + repo, RunnerID: r.ID,
		CreateDB: r.Pool.Database.CreateDB, Roles: r.Pool.Database.Roles})
	if err != nil {
		return fmt.Errorf("database: %s", b.redact(err.Error()))
	}
	b.learn(acc.Password)
	args := []string{"exec", "-i", pool, "sh", "-c", `umask 077 && cat > "$1"`, "sh", jobDir(r.Name) + "/" + DBEnvFileName}
	return b.step(ctx, "write db.env", args, acc.DBEnvFile())
}

// containerCmd runs the Wine tools inside the pool container as the job user:
// the host commands of PrepareWinePrefix become `docker exec` calls.
type containerCmd struct {
	b    *Backend
	pool string
}

func (c containerCmd) Run(ctx context.Context, cmd sys.Command) (sys.Result, error) {
	args := []string{"exec"}
	args = append(args, envArgs(cmd.Env)...)
	argv := cmd.Args
	if cmd.Name == "runuser" && len(argv) >= 2 && argv[0] == "-u" {
		argv = argv[2:] // the exec is already the job user
	} else {
		argv = append([]string{cmd.Name}, argv...)
	}
	args = append(args, c.pool)
	args = append(args, argv...)
	e, err := c.b.eng.Command(args...)
	if err != nil {
		return sys.Result{}, err
	}
	return c.b.d.Cmd.Run(ctx, e)
}

// containerFS reads and writes files of the pool container through the engine.
// A path is a path inside the container.
type containerFS struct {
	b    *Backend
	pool string
	ctx  context.Context
}

func (f containerFS) exec(stdin string, argv ...string) (sys.Result, error) {
	args := append([]string{"exec", "-i", f.pool}, argv...)
	c, err := f.b.eng.Command(args...)
	if err != nil {
		return sys.Result{}, err
	}
	c.Stdin = stdin
	return f.b.d.Cmd.Run(f.ctx, c)
}

func (f containerFS) check(res sys.Result, err error, what string) error {
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s: exit %d: %s", what, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func (f containerFS) MkdirAll(path string, _ fs.FileMode) error {
	res, err := f.exec("", "mkdir", "-p", path)
	return f.check(res, err, "mkdir")
}

func (f containerFS) WriteFile(path string, data []byte, _ fs.FileMode) error {
	res, err := f.exec(string(data), "sh", "-c", `cat > "$1"`, "sh", path)
	return f.check(res, err, "write")
}

func (f containerFS) ReadFile(path string) ([]byte, error) {
	res, err := f.exec("", "cat", path)
	if e := f.check(res, err, "read"); e != nil {
		return nil, e
	}
	return []byte(res.Stdout), nil
}

func (f containerFS) Exists(path string) bool {
	res, err := f.exec("", "test", "-e", path)
	return err == nil && res.ExitCode == 0
}

func (f containerFS) RemoveAll(path string) error {
	res, err := f.exec("", "rm", "-rf", "--", path)
	return f.check(res, err, "remove")
}

// ensureWine makes the Wine prefix of the lane for the first job that needs it
// (R5.52). A problem is logged and the job starts anyway, as the spec says.
func (b *Backend) ensureWine(ctx context.Context, pool string, plan PoolPlan) {
	res := EnsureWinePrefix(ctx, containerCmd{b: b, pool: pool}, containerFS{b: b, pool: pool, ctx: ctx},
		WinePrefixSpec{Prefix: plan.WinePrefix, JobUser: JobUser, Layers: plan.Layers})
	switch {
	case res.Problem != "":
		b.log("start", "wine prefix: "+res.Problem+"; the job starts anyway")
	case res.Made:
		b.log("start", "wine prefix made for Wine "+res.Line)
	}
}

// takeLane takes the lane of a repository for its new pool container and
// returns the host folders. The holder is the pool name: a lock left by an
// earlier container of this same pool is taken over, so a swap or a crash does
// not keep the lane.
func (b *Backend) takeLane(pool, repoID string) (runnercache.LaneDirs, error) {
	if b.cfg.CacheRoot == "" || repoID == "" {
		return runnercache.LaneDirs{}, ErrNoLane
	}
	set := runnercache.LaneSet{CacheRoot: b.cfg.CacheRoot, RepoID: repoID, Count: 1,
		Alive: func(holder string) bool { return holder != pool }}
	l, err := set.Acquire(pool)
	if err != nil {
		return runnercache.LaneDirs{}, fmt.Errorf("cache lane: %w", err)
	}
	// The lock stays for the life of the container; a restart of the agent
	// finds the same holder and takes it over.
	return l.LaneDirs, nil
}
