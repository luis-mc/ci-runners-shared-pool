package container

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// fakeDB is a Postgres that records what the start asks of it. Nothing runs.
type fakeDB struct {
	calls      []string
	failServer error
	failProv   error
	failRemove error
	pw         string
}

func (d *fakeDB) EnsureServer(context.Context) error {
	d.calls = append(d.calls, "server")
	return d.failServer
}

func (d *fakeDB) Provision(_ context.Context, s runners.DBSpec) (runners.DBAccess, error) {
	d.calls = append(d.calls, "provision "+s.Repo+" "+s.RunnerID)
	if d.failProv != nil {
		return runners.DBAccess{}, d.failProv
	}
	return runners.DBAccess{Host: "crsp-pg-x", Port: "5432", User: "r_" + s.RunnerID, Database: "r_" + s.RunnerID, Password: d.pw}, nil
}

func (d *fakeDB) Remove(_ context.Context, id string) error {
	d.calls = append(d.calls, "remove "+id)
	return d.failRemove
}

const fakePW = "FAKE-PG-PASSWORD-0001"

func withLane(t *testing.T) func(*Config) {
	root := t.TempDir()
	return func(c *Config) { c.CacheRoot = root }
}

func withDB(d *fakeDB) func(*Config) {
	return func(c *Config) { c.Database = func(context.Context, string) (Database, error) { return d, nil } }
}

func (r *rig) specWith(name string, mod func(*backend.RunnerSpec)) backend.RunnerSpec {
	s := r.spec(name, "app", r.image())
	s.Pool.RepoID = "42"
	if mod != nil {
		mod(&s)
	}
	return s
}

func (r *rig) startSpec(s backend.RunnerSpec) (backend.Handle, error) {
	return r.b.Start(context.Background(), s, backend.StartConfig{JITConfig: jitSecret})
}

func runCall(f *fakeDocker) string {
	for _, c := range f.calls {
		if strings.HasPrefix(c, "docker run ") {
			return c
		}
	}
	return ""
}

func startCall(f *fakeDocker) string {
	for _, c := range f.calls {
		if strings.HasPrefix(c, "docker exec -i ") && strings.Contains(c, "IFS= read -r c") {
			return c
		}
	}
	return ""
}

func has(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
}

func hasNot(t *testing.T, got string, not ...string) {
	t.Helper()
	for _, w := range not {
		if strings.Contains(got, w) {
			t.Errorf("unexpected %q in:\n%s", w, got)
		}
	}
}

// Rows 13, 36: the base pool takes the lane of its repository and gives the
// job the cache variables and the pool CPU count, and nothing else.
func TestPoolStartMountsLaneAndSetsJobEnvironment(t *testing.T) {
	r := newRig(t, withLane(t))
	s := r.specWith("j1", func(s *backend.RunnerSpec) { s.MaxParallel = 3 })
	if _, err := r.startSpec(s); err != nil {
		t.Fatal(err)
	}
	run := runCall(r.f)
	has(t, run, "target=/ci-tools", "target=/rust-target", "target=/opt/hostedtoolcache", "target=/wine", "target=/ms-playwright",
		"-e CI_TOOLS_DIR=/ci-tools", "-e CARGO_TARGET_DIR=/rust-target", "-e RUNNER_TOOL_CACHE=/opt/hostedtoolcache")
	hasNot(t, run, "PLAYWRIGHT_BROWSERS_PATH", "WINEPREFIX", "--device", "--device-cgroup-rule", "--group-add", "crsp/usr/local")
	st := startCall(r.f)
	has(t, st, "-e CI_MAX_PARALLEL=3", "-e CRSP_CACHE_BUDGET_GIB=", "-e CI_TOOLS_QUOTA_GIB=", "-e CRSP_RUST_CACHE_CAP_GIB=")
	hasNot(t, st, "CRSP_DB_ENV_FILE", jitSecret)
	if err := CheckRunArgs(runFields(run)); err != nil {
		t.Fatalf("the composed run is refused: %v", err)
	}
}

// The lane needs a cache folder and a repository id; without either the pool
// takes none and sets none of its variables.
func TestPoolStartWithoutLaneConditions(t *testing.T) {
	r := newRig(t) // no CacheRoot
	if _, err := r.startSpec(r.specWith("j1", nil)); err != nil {
		t.Fatal(err)
	}
	hasNot(t, runCall(r.f), "--mount", "CI_TOOLS_DIR")
	hasNot(t, startCall(r.f), "CI_MAX_PARALLEL", "CI_TOOLS_QUOTA_GIB")

	r2 := newRig(t, withLane(t))
	if _, err := r2.startSpec(r2.specWith("j1", func(s *backend.RunnerSpec) { s.Pool.RepoID = "" })); err != nil {
		t.Fatal(err)
	}
	hasNot(t, runCall(r2.f), "--mount")
}

// A second runner of the pool reuses the container: no second lane, no second run.
func TestSecondRunnerKeepsThePoolAndItsLane(t *testing.T) {
	r := newRig(t, withLane(t))
	for _, n := range []string{"j1", "j2"} {
		if _, err := r.startSpec(r.specWith(n, nil)); err != nil {
			t.Fatal(err)
		}
	}
	if n := r.f.count("run "); n != 1 {
		t.Fatalf("pool made %d times", n)
	}
}

// A pool made again (a swap) takes over the lock its earlier container left.
func TestPoolMadeAgainTakesOverItsLane(t *testing.T) {
	r := newRig(t, withLane(t))
	if _, err := r.startSpec(r.specWith("j1", nil)); err != nil {
		t.Fatal(err)
	}
	delete(r.f.containers, PoolName("luis-mc", "app"))
	if _, err := r.startSpec(r.specWith("j2", nil)); err != nil {
		t.Fatalf("the lane of the same pool was not taken over: %v", err)
	}
}

// Row 15: the crsp mount is in the run only when the config has the binary.
func TestPoolStartMountsCrspOnlyWhenSet(t *testing.T) {
	r := newRig(t, func(c *Config) { c.CrspHostPath = "/usr/local/bin/crsp-agent" })
	if _, err := r.startSpec(r.specWith("j1", nil)); err != nil {
		t.Fatal(err)
	}
	has(t, runCall(r.f), "source=/usr/local/bin,target=/opt/crsp-bin,readonly", "CRSP_BIN=/opt/crsp-bin/crsp-agent")
	r2 := newRig(t)
	if _, err := r2.startSpec(r2.specWith("j1", nil)); err != nil {
		t.Fatal(err)
	}
	hasNot(t, runCall(r2.f), "target=/opt/crsp-bin")
}

// Row 24: the browsers layer sets the image path; no layer sets none.
func TestPoolStartSetsBrowsersPathForTheLayer(t *testing.T) {
	r := newRig(t)
	s := r.specWith("j1", func(s *backend.RunnerSpec) { s.Pool.Layers = []string{"browsers"} })
	if _, err := r.startSpec(s); err != nil {
		t.Fatal(err)
	}
	has(t, runCall(r.f), "-e PLAYWRIGHT_BROWSERS_PATH="+runnercache.ImageBrowsersPath)
}

// Row 28: the device only with the kvm layer and a usable /dev/kvm.
func TestPoolStartKVMConditions(t *testing.T) {
	good := KVMFacts{Exists: true, Usable: true, GID: 108}
	cases := []struct {
		name   string
		layers []string
		facts  KVMFacts
		force  bool
		device bool
		group  bool
		note   string
	}{
		{"layer and usable device", []string{"kvm"}, good, false, true, true, ""},
		{"device but no layer", nil, good, false, false, false, ""},
		{"layer, device not usable", []string{"kvm"}, KVMFacts{Exists: true}, false, false, false, ReasonKVMNotUsable},
		{"layer, no device", []string{"kvm"}, KVMFacts{}, false, false, false, ReasonKVMWaitedOut},
		{"machine config forces it", nil, KVMFacts{}, true, true, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, func(cfg *Config) {
				cfg.KVMProbe = func() KVMFacts { return c.facts }
				cfg.KVM = c.force
				cfg.KVMWaitBound = 1
			})
			r.b.d.Sleep = nil
			s := r.specWith("j1", func(s *backend.RunnerSpec) { s.Pool.Layers = c.layers })
			if _, err := r.startSpec(s); err != nil {
				t.Fatal(err)
			}
			run := runCall(r.f)
			if got := strings.Contains(run, "--device-cgroup-rule "+KVMCgroupRule); got != c.device {
				t.Errorf("device = %v, want %v: %s", got, c.device, run)
			}
			if got := strings.Contains(run, "--group-add 108"); got != c.group {
				t.Errorf("group = %v, want %v", got, c.group)
			}
			if err := CheckRunArgs(runFields(run)); err != nil {
				t.Errorf("refused: %v", err)
			}
			if c.note != "" && !strings.Contains(strings.Join(r.logs, "\n"), "no KVM device") {
				t.Errorf("no log line about KVM: %v", r.logs)
			}
		})
	}
}

// Row 26: the Wine prefix is made once in the lane for the windows-packaging
// layer, kept while the line stays, and the job gets WINEPREFIX under both names.
func TestPoolStartMakesWinePrefixForTheLayer(t *testing.T) {
	r := newRig(t, withLane(t))
	r.f.wineLine = "9.0"
	s := func(n string) backend.RunnerSpec {
		return r.specWith(n, func(s *backend.RunnerSpec) { s.Pool.Layers = []string{"windows-packaging"} })
	}
	if _, err := r.startSpec(s("j1")); err != nil {
		t.Fatal(err)
	}
	run := runCall(r.f)
	has(t, run, "-e WINEPREFIX=/wine", "-e POOL_WINE_PREFIX=/wine")
	hasNot(t, run, "TMPDIR=")
	if r.f.wineBoots != 1 {
		t.Fatalf("wineboot ran %d times", r.f.wineBoots)
	}
	has(t, r.f.text(), "env -u TMPDIR xvfb-run -a wineboot --init", "env -u TMPDIR wineserver -w")
	if got := r.pool("app").files["/wine/"+WinePrefixStamp]; strings.TrimSpace(got) != "9.0" {
		t.Errorf("stamp = %q", got)
	}
	if _, err := r.startSpec(s("j2")); err != nil {
		t.Fatal(err)
	}
	if r.f.wineBoots != 1 {
		t.Errorf("the prefix was made again: %d boots", r.f.wineBoots)
	}
}

func TestPoolStartNoWineWithoutTheLayer(t *testing.T) {
	r := newRig(t, withLane(t))
	if _, err := r.startSpec(r.specWith("j1", nil)); err != nil {
		t.Fatal(err)
	}
	hasNot(t, r.f.text(), "wineboot", "wine --version")
}

// A wine prefix that cannot be made is a log line and the job starts (R5.52).
func TestPoolStartWineProblemDoesNotStopTheJob(t *testing.T) {
	r := newRig(t, withLane(t)) // no wineLine: wine --version fails
	s := r.specWith("j1", func(s *backend.RunnerSpec) { s.Pool.Layers = []string{"windows-packaging"} })
	if _, err := r.startSpec(s); err != nil {
		t.Fatalf("the start failed on a wine problem: %v", err)
	}
	if !strings.Contains(strings.Join(r.logs, "\n"), "wine prefix") {
		t.Errorf("the problem is not logged: %v", r.logs)
	}
}

// The windows-packaging layer keeps its prefix in the lane; with no lane the
// start fails and no pool is made.
func TestPoolStartWineNeedsALane(t *testing.T) {
	r := newRig(t)
	s := r.specWith("j1", func(s *backend.RunnerSpec) { s.Pool.Layers = []string{"windows-packaging"} })
	_, err := r.startSpec(s)
	if !errors.Is(err, ErrNoLane) {
		t.Fatalf("err = %v", err)
	}
	if r.f.count("run ") != 0 {
		t.Error("a pool was made")
	}
}

// Rows 34, 35: a declared database starts the server, joins the network,
// writes db.env (only the job user reads it) and points the job at it.
func TestPoolStartWithDatabase(t *testing.T) {
	db := &fakeDB{pw: fakePW}
	r := newRig(t, withDB(db))
	s := r.specWith("j1", func(s *backend.RunnerSpec) {
		s.Pool.Database = &backend.PoolDatabase{CreateDB: true, Roles: []string{"app_rw"}}
	})
	if _, err := r.startSpec(s); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(db.calls, ","); got != "server,provision luis-mc/app r_j1" {
		t.Errorf("db calls = %s", got)
	}
	has(t, r.f.text(), "docker network connect crsp-net-"+PoolName("luis-mc", "app")+" "+PoolName("luis-mc", "app"))
	st := startCall(r.f)
	has(t, st, "-e CRSP_DB_ENV_FILE=/home/runner/jobs/j1/db.env")
	hasNot(t, st, fakePW)
	env := r.pool("app").files["/home/runner/jobs/j1/db.env"]
	has(t, env, "PGPASSWORD="+fakePW, "PGHOST=crsp-pg-x")
	// The password is not in any argument, and the log never shows it.
	for _, c := range r.f.calls {
		hasNot(t, c, fakePW)
	}
	hasNot(t, strings.Join(r.logs, "\n"), fakePW)
	if len(r.learn) == 0 || !contains(r.learn, fakePW) {
		t.Errorf("the redactor did not learn the password: %v", r.learn)
	}
	hasNot(t, runCall(r.f), fakePW)
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

func TestPoolStartWithoutDatabaseTouchesNoPostgres(t *testing.T) {
	db := &fakeDB{pw: fakePW}
	r := newRig(t, withDB(db))
	if _, err := r.startSpec(r.specWith("j1", nil)); err != nil {
		t.Fatal(err)
	}
	if len(db.calls) != 0 {
		t.Errorf("postgres was used: %v", db.calls)
	}
	hasNot(t, r.f.text(), "network connect")
}

// A repository that declares a database on a machine with no secret store
// cannot start: the error names the cause and no pool is made.
func TestPoolStartDatabaseWithoutSecretStoreFails(t *testing.T) {
	r := newRig(t)
	s := r.specWith("j1", func(s *backend.RunnerSpec) { s.Pool.Database = &backend.PoolDatabase{} })
	_, err := r.startSpec(s)
	if !errors.Is(err, ErrNoDatabase) {
		t.Fatalf("err = %v", err)
	}
	if r.f.count("run ") != 0 {
		t.Error("a pool was made")
	}
}

// One pool start that fails leaves no runner: folder, config file and login go.
func TestPoolStartFailuresLeaveNoRunner(t *testing.T) {
	cases := []struct {
		name  string
		db    *fakeDB
		mod   func(*rig)
		want  string
		login bool // the login is removed again
	}{
		{"server", &fakeDB{failServer: errors.New("no server")}, nil, "database server", false},
		{"provision", &fakeDB{failProv: errors.New("no login")}, nil, "database", true},
		{"network", &fakeDB{pw: fakePW}, func(r *rig) { r.f.failNetwork = "Error: network not found" }, "database network", false},
		{"runner start", &fakeDB{pw: fakePW}, func(r *rig) { r.f.failStart = "boom" }, "start failed", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, withDB(c.db), withLane(t))
			if c.mod != nil {
				c.mod(r)
			}
			s := r.specWith("j1", func(s *backend.RunnerSpec) { s.Pool.Database = &backend.PoolDatabase{} })
			_, err := r.startSpec(s)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if err != nil && strings.Contains(err.Error(), fakePW) {
				t.Error("the error shows the password")
			}
			if got := r.pool("app"); got != nil {
				for p := range got.files {
					if strings.HasPrefix(p, "/home/runner/jobs/j1") {
						t.Errorf("left behind: %s", p)
					}
				}
			}
			removed := contains(c.db.calls, "remove r_j1")
			if c.login && !removed {
				t.Errorf("the login stays: %v", c.db.calls)
			}
		})
	}
}

// Every piece at once: one run with the lane, the browsers path, Wine, KVM and
// the crsp mount, and still a run the guard accepts.
func TestPoolStartAllPiecesTogether(t *testing.T) {
	db := &fakeDB{pw: fakePW}
	r := newRig(t, withLane(t), withDB(db), func(c *Config) {
		c.CrspHostPath = "/opt/crsp/crsp"
		c.KVMProbe = func() KVMFacts { return KVMFacts{Exists: true, Usable: true, GID: 108} }
	})
	r.f.wineLine = "9.0"
	s := r.specWith("j1", func(s *backend.RunnerSpec) {
		s.MaxParallel = 2
		s.Pool.Layers = []string{"windows-packaging", "browsers", "kvm"}
		s.Pool.Database = &backend.PoolDatabase{}
	})
	if _, err := r.startSpec(s); err != nil {
		t.Fatal(err)
	}
	run := runCall(r.f)
	has(t, run, "--device-cgroup-rule "+KVMCgroupRule, "--group-add 108", "target=/ci-tools", "target=/opt/crsp-bin,readonly",
		"PLAYWRIGHT_BROWSERS_PATH=/opt/crsp/browsers", "WINEPREFIX=/wine")
	if err := CheckRunArgs(runFields(run)); err != nil {
		t.Fatal(err)
	}
	has(t, startCall(r.f), "CI_MAX_PARALLEL=2", "CRSP_DB_ENV_FILE=")
}

// PlanPool is pure: the same input gives the same plan.
func TestPlanPoolIsDeterministic(t *testing.T) {
	cfg := Config{CacheRoot: "/c"}
	r := backend.RunnerSpec{Name: "j1", Image: backend.ImageRef{ID: "crsp-pool:k"}, MaxParallel: 2,
		Pool: backend.PoolSetup{RepoID: "1", Layers: []string{"browsers", "windows-packaging"}}}
	a, err := PlanPool(cfg, r, "o", "p", KVMFacts{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := PlanPool(cfg, r, "o", "p", KVMFacts{})
	ra, _ := RunArgs(a.Pool)
	rb, _ := RunArgs(b.Pool)
	if strings.Join(ra, " ") != strings.Join(rb, " ") {
		t.Error("run arguments differ between two plans")
	}
}

// CleanUp drops the runner's login in a pool that has a database, once.
func TestCleanUpRemovesTheLogin(t *testing.T) {
	db := &fakeDB{pw: fakePW}
	r := newRig(t, withDB(db))
	s := r.specWith("j1", func(s *backend.RunnerSpec) { s.Pool.Database = &backend.PoolDatabase{} })
	h, err := r.startSpec(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.b.CleanUp(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	if !contains(db.calls, "remove r_j1") {
		t.Errorf("login not removed: %v", db.calls)
	}
	// Done once: a second clean up of the same runner asks Postgres nothing.
	n := len(db.calls)
	if err := r.b.CleanUp(context.Background(), h); err != nil || len(db.calls) != n {
		t.Errorf("second clean up: %v %v", err, db.calls)
	}
	// A failed removal is an error and is tried again by the next clean up.
	h2, err := r.startSpec(r.specWith("j2", func(s *backend.RunnerSpec) { s.Pool.Database = &backend.PoolDatabase{} }))
	if err != nil {
		t.Fatal(err)
	}
	db.failRemove = errors.New("psql down")
	if err := r.b.CleanUp(context.Background(), h2); err == nil {
		t.Error("a failed login removal was hidden")
	}
	db.failRemove = nil
	if err := r.b.CleanUp(context.Background(), h2); err != nil || !contains(db.calls, "remove r_j2") {
		t.Errorf("retry: %v %v", err, db.calls)
	}
}

// OPS37: CRSP_VM_IMAGE_MIRROR crosses into the job process; unset adds nothing.
func TestPlanPoolPassesTheVMImageMirrorToTheJob(t *testing.T) {
	r := backend.RunnerSpec{Name: "j1", Image: backend.ImageRef{ID: "crsp-pool:k"}}
	plan, err := PlanPool(Config{VMImageMirror: "https://mirror.corp/vm"}, r, "o", "p", KVMFacts{})
	if err != nil || plan.RunnerEnv["CRSP_VM_IMAGE_MIRROR"] != "https://mirror.corp/vm" {
		t.Fatalf("%v %v", plan.RunnerEnv, err)
	}
	plan, err = PlanPool(Config{}, r, "o", "p", KVMFacts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := plan.RunnerEnv["CRSP_VM_IMAGE_MIRROR"]; ok {
		t.Errorf("default adds a variable: %v", plan.RunnerEnv)
	}
}

func TestStartExportsTheMirrorFromTheDepsAddresses(t *testing.T) {
	r := newRig(t, func(c *Config) { c.VMImageMirror = "https://mirror.corp/vm" })
	if _, err := r.startSpec(r.spec("j1", "app", r.image())); err != nil {
		t.Fatal(err)
	}
	has(t, startCall(r.f), "-e CRSP_VM_IMAGE_MIRROR=https://mirror.corp/vm")
}
