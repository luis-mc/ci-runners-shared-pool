package runners

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
)

type rig struct {
	svc   *Service
	store *memStore
	gh    *fakeGitHub
	ag    *fakeAgent
	au    *fakeAudit
	clock time.Time
	n     int
}

func machine(os, arch string, allowed bool) Machine {
	return Machine{ID: "m1", Name: "box", OS: os, Arch: arch, Backends: []BackendOption{
		{Name: backend.Persistent, Usable: true, Allowed: allowed, Kinds: []string{"persistent"}, OS: []string{os}},
	}}
}

func newRig(m Machine) *rig {
	r := &rig{store: newMemStore(), gh: &fakeGitHub{}, ag: &fakeAgent{}, au: &fakeAudit{}, clock: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	r.svc = &Service{Store: r.store, Machines: fakeMachines{"m1": m}, Resolver: fakeResolver{}, GitHub: r.gh, Agent: r.ag, Audit: r.au,
		PoolName: "pool-a", Poll: time.Minute, WarnAfter: 30 * time.Minute, DrainTimeout: 90 * time.Minute,
		Now:   func() time.Time { return r.clock },
		Sleep: func(_ context.Context, d time.Duration) error { r.clock = r.clock.Add(d); return nil },
		NewID: func() string { r.n++; return fmt.Sprintf("%04d", r.n) }}
	return r
}

func (r *rig) add(t *testing.T) Record {
	t.Helper()
	ctx := context.Background()
	p, err := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "persistent", MachineID: "m1", Count: 1, Scope: Scope{Kind: "repository", RepositoryID: 1}}, "luis-mc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Apply(ctx, p.ID, nil, "luis-mc"); err != nil {
		t.Fatal(err)
	}
	all, _ := r.store.ListRunners(ctx, Filter{})
	if len(all) != 1 {
		t.Fatalf("runners = %v", all)
	}
	return all[0]
}

// R5.1: only where supported, refused with the reason.
func TestCreateOnlyOnSupportingMachine(t *testing.T) {
	ctx := context.Background()
	scope := Scope{Kind: "repository", RepositoryID: 1}
	r := newRig(machine("macos", "arm64", true))
	r.svc.Machines = fakeMachines{"m1": {ID: "m1", Name: "mac-1", OS: "macos", Arch: "arm64", Backends: machine("macos", "arm64", true).Backends}}
	_, err := r.svc.PlanAdd(ctx, AddRequest{OS: "windows", Kind: "persistent", MachineID: "m1", Count: 1, Scope: scope}, "u")
	var re *RefusalError
	if !errors.As(err, &re) || re.Reason != "machine mac-1: no allowed backend for Windows" {
		t.Fatalf("err = %v", err)
	}
	// Present but not allowed: never used.
	r = newRig(machine("linux", "x64", false))
	if _, err := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "persistent", MachineID: "m1", Count: 1, Scope: scope}, "u"); err == nil {
		t.Error("a backend that is not allowed must not be used")
	}
	// Unusable.
	m := machine("linux", "x64", true)
	m.Backends[0].Usable = false
	r = newRig(m)
	if _, err := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "persistent", MachineID: "m1", Count: 1, Scope: scope}, "u"); err == nil {
		t.Error("an unusable backend must not be used")
	}
	// Ephemeral needs the container backend in the allow list (R5.27); a machine with persistent only refuses it.
	r = newRig(machine("linux", "x64", true))
	if _, err := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "ephemeral", MachineID: "m1", Count: 1, Scope: scope}, "u"); err == nil {
		t.Error("ephemeral")
	}
	// Good request: nothing changed yet.
	p, err := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "persistent", MachineID: "m1", Count: 2, Scope: scope}, "u")
	if err != nil || len(p.Steps) != 2 || p.State != PlanPlanned || p.Steps[0].Target != "box-1" || p.Steps[1].Target != "box-2" {
		t.Fatalf("%+v %v", p, err)
	}
	if len(r.ag.added) != 0 || r.gh.regCalls != 0 {
		t.Error("a plan changes nothing")
	}
	for _, bad := range []int{0, 65} {
		if _, err := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "persistent", MachineID: "m1", Count: bad, Scope: scope}, "u"); err == nil {
			t.Errorf("count %d", bad)
		}
	}
}

// R5.36 in the plan: generic labels only.
func TestPlanRefusesProjectLabels(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	_, err := r.svc.PlanAdd(context.Background(), AddRequest{OS: "linux", Kind: "persistent", MachineID: "m1", Count: 1,
		Labels: []string{"my-project"}, Scope: Scope{Kind: "repository", RepositoryID: 1}}, "u")
	var re *RefusalError
	if !errors.As(err, &re) || !strings.Contains(re.Reason, "my-project") {
		t.Fatalf("err = %v", err)
	}
}

// R5.2 and R5.7: the record fields and the isolation.
func TestRunnerRecordFields(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	rec := r.add(t)
	if rec.Name != "box-1" || rec.MachineID != "m1" || rec.OS != "linux" || rec.Arch != "x64" || rec.Kind != "persistent" ||
		rec.Backend != "persistent" || rec.Isolation != "persistent" || rec.State != Idle || !rec.Managed || !rec.KeepsState() ||
		strings.Join(rec.Labels, ",") != "linux,self-hosted,x64" || rec.RunnerVersion != "2.338.2" ||
		rec.ServiceName != "actions.runner.luis-mc-demo.box-1" || rec.Account != "crsp-box-1" || rec.Scope.RepositoryID != 1 {
		t.Errorf("%+v", rec)
	}
	spec := r.ag.added[0]
	if spec.RunnerSHA256 == "" || !ValidSHA256(spec.RunnerSHA256) || spec.RunnerVersion != "2.338.2" || spec.GitHubURL != "https://github.com/luis-mc/demo" {
		t.Errorf("%+v", spec)
	}
	if r.ag.cfgs[0].Registration != "REG-TOKEN-1" || r.ag.cfgs[0].JITConfig != "" {
		t.Error("a persistent runner starts with a registration token")
	}
}

// A failed add leaves a failed runner and says why; the audit has the failure.
func TestAddFailureMarksFailed(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	r.ag.addErr = errors.New("agent says no")
	ctx := context.Background()
	p, _ := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "persistent", MachineID: "m1", Count: 1, Scope: Scope{Kind: "repository", RepositoryID: 1}}, "u")
	got, _ := r.svc.Apply(ctx, p.ID, nil, "u")
	if got.State != PlanFailed || got.Steps[0].State != "failed" || !strings.Contains(got.Steps[0].Error, "agent says no") {
		t.Errorf("%+v", got)
	}
	all, _ := r.store.ListRunners(ctx, Filter{})
	if len(all) != 1 || all[0].State != Failed || !strings.Contains(all[0].FailedReason, "agent says no") {
		t.Errorf("%+v", all)
	}
}

// A failed add removes the GitHub registration it made, never an older runner
// of the same name, and says what it left when it cannot.
func TestRegistrationCleanupAfterFailedAdd(t *testing.T) {
	r := newRig(machine("windows", "x64", true))
	ctx := context.Background()
	cause := errors.New("register service: the account has no right to log on as a service")
	tg := Target{InstallationID: 7}

	r.gh.runners = []github.Runner{{ID: 5, Name: "box-1"}}
	err := r.svc.withRegistrationCleanup(ctx, tg, "box-1", nil, nil, cause)
	if len(r.gh.deleted) != 1 || r.gh.deleted[0] != 5 || !strings.Contains(err.Error(), "Cleaned up: GitHub registration box-1") || !strings.Contains(err.Error(), "no right to log on") {
		t.Errorf("deleted = %v, err = %v", r.gh.deleted, err)
	}

	r.gh.deleted = nil
	err = r.svc.withRegistrationCleanup(ctx, tg, "box-1", []github.Runner{{ID: 5, Name: "box-1"}}, nil, cause)
	if len(r.gh.deleted) != 0 || err.Error() != cause.Error() {
		t.Errorf("an older runner of that name stays: deleted = %v, err = %v", r.gh.deleted, err)
	}

	r.gh.delErr = errors.New("GitHub refused")
	err = r.svc.withRegistrationCleanup(ctx, tg, "box-1", nil, nil, cause)
	if !strings.Contains(err.Error(), "Left behind: GitHub registration box-1 (GitHub refused)") {
		t.Errorf("err = %v", err)
	}

	err = r.svc.withRegistrationCleanup(ctx, tg, "box-1", nil, errors.New("offline"), cause)
	if !strings.Contains(err.Error(), "Left behind: GitHub registration box-1 (could not read the runners on GitHub: offline)") {
		t.Errorf("err = %v", err)
	}
}

// R5.4: remove needs a yes naming what goes, drains, and uses a remove token.
func TestRemoveDrainsAndUsesRemoveToken(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	rec := r.add(t)
	ctx := context.Background()

	_, _, err := r.svc.BeginRemove(ctx, rec.ID, false, "u")
	var nc *NeedsConfirmation
	if !errors.As(err, &nc) {
		t.Fatalf("err = %v", err)
	}
	var kinds []string
	for _, it := range nc.Items {
		kinds = append(kinds, it.Kind)
	}
	if strings.Join(kinds, ",") != "registration,service,account,folder" {
		t.Errorf("items = %v", nc.Items)
	}
	if r.gh.rmCalls != 0 || len(r.ag.removed) != 0 {
		t.Error("nothing is removed without a yes")
	}

	// Busy for 3 polls, then idle: it drains, then removes.
	polls := 0
	r.gh.runners = []github.Runner{{Name: "box-1", Status: "online", Busy: true}}
	r.svc.Sleep = func(_ context.Context, d time.Duration) error {
		r.clock = r.clock.Add(d)
		if polls++; polls == 3 {
			r.gh.runners = []github.Runner{{Name: "box-1", Status: "online", Busy: false}}
		}
		return nil
	}
	got, finish, err := r.svc.BeginRemove(ctx, rec.ID, true, "luis-mc")
	if err != nil || got.State != Draining {
		t.Fatalf("%+v %v", got, err)
	}
	if err := finish(ctx); err != nil {
		t.Fatal(err)
	}
	if polls != 3 {
		t.Errorf("polled %d times", polls)
	}
	if len(r.ag.removed) != 1 || r.ag.removed[0].RemoveToken != "REMOVE-TOKEN-1" || r.gh.rmCalls != 1 {
		t.Errorf("removed = %+v", r.ag.removed)
	}
	for _, tok := range r.gh.tokens {
		if tok == "REG-TOKEN-1" && len(r.ag.removed) > 0 && r.ag.removed[0].RemoveToken == tok {
			t.Error("a registration token must never be used to remove")
		}
	}
	if _, err := r.store.GetRunner(ctx, rec.ID); !errors.Is(err, ErrNotFound) {
		t.Error("record kept")
	}
}

// R5.19 and section 8: a failed removal keeps the runner, working.
func TestRemoveFailureKeepsRunner(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	rec := r.add(t)
	r.ag.rmErr = errors.New("exit 1")
	ctx := context.Background()
	_, finish, _ := r.svc.BeginRemove(ctx, rec.ID, true, "u")
	err := finish(ctx)
	if err == nil || !strings.Contains(err.Error(), "left as it was") {
		t.Fatalf("err = %v", err)
	}
	got, gerr := r.store.GetRunner(ctx, rec.ID)
	if gerr != nil || got.State != Idle {
		t.Errorf("%+v %v", got, gerr)
	}
}

// R6.4: an unreadable runner list stops the removal; a timeout asks, never kills.
func TestRemoveFailsClosedAndTimeoutAsks(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	rec := r.add(t)
	ctx := context.Background()
	r.gh.listErr = errors.New("github down")
	_, finish, _ := r.svc.BeginRemove(ctx, rec.ID, true, "u")
	if err := finish(ctx); err == nil || len(r.ag.removed) != 0 {
		t.Fatalf("err=%v removed=%v", err, r.ag.removed)
	}

	r.gh.listErr = nil
	r.gh.runners = []github.Runner{{Name: "box-1", Busy: true}}
	var warned []string
	r.svc.OnDrainWarn = func(_, job string) { warned = append(warned, job) }
	_, finish, _ = r.svc.BeginRemove(ctx, rec.ID, true, "u")
	err := finish(ctx)
	if !IsDrainTimeout(err) || len(r.ag.removed) != 0 || len(warned) != 1 {
		t.Fatalf("err=%v removed=%v warned=%v", err, r.ag.removed, warned)
	}
	got, _ := r.store.GetRunner(ctx, rec.ID)
	if got.State != Idle {
		t.Errorf("state after timeout = %s", got.State)
	}
}

func unmanaged(r *rig) Record {
	rec := Record{ID: "r_old", Name: "hand-made", MachineID: "m1", OS: "linux", Arch: "x64", Kind: "persistent", Backend: "persistent",
		Isolation: "persistent", State: Idle, Managed: false, ServiceName: "actions.runner.luis-mc-demo.hand-made",
		Account: "pi", Folder: "/home/pi/actions-runner", Scope: Scope{Kind: "repository", RepositoryID: 1}}
	_ = r.store.InsertRunner(context.Background(), rec)
	return rec
}

// R5.6: replace only after the new runner is online; each step needs its own yes.
func TestReplaceOnlyAfterNewOnline(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	old := unmanaged(r)
	ctx := context.Background()

	p, err := r.svc.PlanReplacement(ctx, old.ID, "luis-mc")
	if err != nil {
		t.Fatal(err)
	}
	var acts []string
	for _, s := range p.Steps {
		acts = append(acts, s.Action)
	}
	if strings.Join(acts, ",") != "add_runner,stop_old_service,remove_old_runner,delete_old_folder" {
		t.Fatalf("steps = %v", acts)
	}
	if p.Steps[0].NeedsYes || !p.Steps[1].NeedsYes || !p.Steps[2].NeedsYes || !p.Steps[3].NeedsYes {
		t.Error("each removal step needs its own yes; the add does not")
	}
	if p.Steps[1].WaitsFor != "s1" || p.Steps[3].WaitsFor != "s3" {
		t.Error("waits_for")
	}

	approve := []string{"s2", "s3", "s4"}
	// 1. The new runner is not online: the removal steps wait.
	got, err := r.svc.Apply(ctx, p.ID, approve, "luis-mc")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != PlanApplying || got.Steps[0].State != "done" || got.Steps[1].State != "waiting" ||
		!strings.Contains(got.Steps[1].Error, "not online") {
		t.Fatalf("%+v", got.Steps)
	}
	if len(r.ag.stopped) != 0 || len(r.ag.removed) != 0 {
		t.Error("nothing of the old runner is touched before the new one is online")
	}

	// 2. The list cannot be read: wait.
	r.gh.listErr = errors.New("down")
	got, _ = r.svc.Apply(ctx, p.ID, approve, "luis-mc")
	if got.Steps[1].State != "waiting" || !strings.Contains(got.Steps[1].Error, "cannot be read") || len(r.ag.stopped) != 0 {
		t.Fatalf("%+v", got.Steps[1])
	}

	// 3. The old runner has a job: wait.
	r.gh.listErr = nil
	r.gh.runners = []github.Runner{{Name: "box-1", Status: "online"}, {Name: "hand-made", Status: "online", Busy: true}}
	got, _ = r.svc.Apply(ctx, p.ID, approve, "luis-mc")
	if got.Steps[1].State != "waiting" || !strings.Contains(got.Steps[1].Error, "has a job") || len(r.ag.stopped) != 0 {
		t.Fatalf("%+v", got.Steps[1])
	}

	// 4. All clear: stop, remove with a remove token, delete the folder.
	r.gh.runners = []github.Runner{{Name: "box-1", Status: "online"}, {Name: "hand-made", Status: "online"}}
	got, err = r.svc.Apply(ctx, p.ID, approve, "luis-mc")
	if err != nil || got.State != PlanDone {
		t.Fatalf("%+v %v", got, err)
	}
	if len(r.ag.stopped) != 1 || r.ag.stopped[0] != old.ServiceName {
		t.Errorf("stopped = %v", r.ag.stopped)
	}
	if len(r.ag.removed) != 1 || r.ag.removed[0].RemoveToken != "REMOVE-TOKEN-1" || !r.ag.removed[0].KeepFiles {
		t.Errorf("removed = %+v", r.ag.removed)
	}
	if _, err := r.store.GetRunner(ctx, old.ID); !errors.Is(err, ErrNotFound) {
		t.Error("the old record goes with the cleanup")
	}
	if len(r.ag.folders) != 1 || r.ag.folders[0] != old.ServiceName {
		t.Errorf("folders = %v", r.ag.folders)
	}
	if len(r.ag.added) != 1 {
		t.Errorf("the new runner is added once, not on every apply: %d", len(r.ag.added))
	}
}

// R5.6: no yes, no removal; nothing changes silently.
func TestReplaceWithoutYesChangesNothing(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	old := unmanaged(r)
	ctx := context.Background()
	p, _ := r.svc.PlanReplacement(ctx, old.ID, "u")
	r.gh.runners = []github.Runner{{Name: "box-1", Status: "online"}}
	got, err := r.svc.Apply(ctx, p.ID, []string{"s2"}, "u") // only the stop is approved
	if err != nil {
		t.Fatal(err)
	}
	if got.Steps[1].State != "done" || got.Steps[2].State != "skipped" || got.Steps[3].State != "skipped" {
		t.Errorf("%+v", got.Steps)
	}
	if len(r.ag.removed) != 0 || len(r.ag.folders) != 0 {
		t.Error("a step without its yes must not run")
	}
	if _, err := r.svc.Apply(ctx, p.ID, []string{"s9"}, "u"); err == nil {
		t.Error("an unknown step id")
	}
}

// A removal that fails stops the plan, leaves the old runner and its folder.
func TestReplaceRemovalFailureStops(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	old := unmanaged(r)
	ctx := context.Background()
	p, _ := r.svc.PlanReplacement(ctx, old.ID, "u")
	r.gh.runners = []github.Runner{{Name: "box-1", Status: "online"}}
	r.ag.rmErr = errors.New("exit 1")
	got, _ := r.svc.Apply(ctx, p.ID, []string{"s2", "s3", "s4"}, "u")
	if got.State != PlanFailed || got.Steps[2].State != "failed" || got.Steps[3].State != "skipped" || len(r.ag.folders) != 0 {
		t.Errorf("%+v", got)
	}
	if _, err := r.store.GetRunner(ctx, old.ID); err != nil {
		t.Error("the old runner record stays")
	}
}

func TestReplaceRefusesManagedRunner(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	rec := r.add(t)
	if _, err := r.svc.PlanReplacement(context.Background(), rec.ID, "u"); err == nil {
		t.Error("a managed runner is removed, not replaced")
	}
	old := unmanaged(r)
	if _, _, err := r.svc.BeginRemove(context.Background(), old.ID, true, "u"); err == nil {
		t.Error("an unmanaged runner is replaced, not removed")
	}
}

// R5.5: found runners that CRSP did not make are stored as not managed.
func TestReportFoundStoresNotManaged(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	ctx := context.Background()
	found := []backend.Found{
		{ServiceName: "actions.runner.a-b.hand", Name: "hand", Folder: "/home/pi/ar", Account: "pi", Running: true},
		{ServiceName: "actions.runner.a-b.locked", Name: "locked", FolderUnreadable: true},
	}
	if err := r.svc.ReportFound(ctx, "m1", found); err != nil {
		t.Fatal(err)
	}
	f := false
	list, _ := r.svc.List(ctx, Filter{Managed: &f})
	if len(list) != 2 || list[0].Managed || list[0].State != Idle || list[1].State != Offline {
		t.Fatalf("%+v", list)
	}
	// One vanishes.
	if err := r.svc.ReportFound(ctx, "m1", found[:1]); err != nil {
		t.Fatal(err)
	}
	list, _ = r.svc.List(ctx, Filter{})
	if len(list) != 1 {
		t.Errorf("%+v", list)
	}
}

// R6.2: no token or password is in a record, a plan or the audit log.
func TestNoTokenOrPasswordStoredOrLogged(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	rec := r.add(t)
	ctx := context.Background()
	_, finish, _ := r.svc.BeginRemove(ctx, rec.ID, true, "u")
	_ = finish(ctx)
	var dump []string
	for _, rr := range r.store.runners {
		b, _ := json.Marshal(rr)
		dump = append(dump, string(b))
	}
	for _, p := range r.store.plans {
		b, _ := json.Marshal(p)
		dump = append(dump, string(b))
	}
	dump = append(dump, r.au.entries...)
	dump = append(dump, r.store.events...)
	all := strings.Join(dump, "\n")
	for _, secret := range []string{"REG-TOKEN-1", "REMOVE-TOKEN-1", "password", "Password"} {
		if strings.Contains(all, secret) {
			t.Errorf("%q found in stored or logged data", secret)
		}
	}
}

// R6.8: every add and remove writes an audit entry.
func TestAuditEntries(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	rec := r.add(t)
	_, finish, _ := r.svc.BeginRemove(context.Background(), rec.ID, true, "luis-mc")
	if err := finish(context.Background()); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(r.au.entries, "\n")
	for _, want := range []string{"luis-mc|runner.add|m1|" + rec.ID + "|box-1", "luis-mc|runner.remove|m1|" + rec.ID + "|box-1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("audit lacks %q:\n%s", want, joined)
		}
	}
}

// Runners R5.39: a new runner gets the declared cache quota of the service.
func TestAddPassesCacheQuotaToTheAgent(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	r.svc.CacheQuota = runnercache.Quota{ToolGiB: 7, RustGiB: 50}
	r.add(t)
	if len(r.ag.added) != 1 || r.ag.added[0].CacheQuota.ToolGiB != 7 || r.ag.added[0].CacheQuota.RustGiB != 50 {
		t.Fatalf("spec %+v", r.ag.added)
	}
}

// CRSP38, scheduling R5.64: an operation on runner set X waits only for X. A
// busy runner of the same repository outside X never holds the removal.
func TestRemoveWaitsOnlyForItsOwnRunner(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	rec := r.add(t)
	ctx := context.Background()
	r.gh.runners = []github.Runner{
		{Name: "box-1", Status: "online", Busy: false},
		{Name: "box-2", Status: "online", Busy: true}, // same repository, outside the set
		{Name: "box-3", Status: "online", Busy: true},
	}
	polls := 0
	r.svc.Sleep = func(_ context.Context, d time.Duration) error { polls++; return nil }
	_, finish, err := r.svc.BeginRemove(ctx, rec.ID, true, "luis-mc")
	if err != nil {
		t.Fatal(err)
	}
	if err := finish(ctx); err != nil {
		t.Fatalf("busy runners outside the set held the removal: %v", err)
	}
	if polls != 0 || len(r.ag.removed) != 1 {
		t.Errorf("polls %d removed %v", polls, r.ag.removed)
	}
}

// CRSP38, scheduling R5.63: the drain writes each decision before the slow step.
func TestDrainLogsItsDecisionsBeforeTheSlowSteps(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	rec := r.add(t)
	ctx := context.Background()
	var lines []string
	r.svc.OnDrainStep = func(_, line string) { lines = append(lines, line) }
	polls := 0
	r.gh.runners = []github.Runner{{Name: "box-1", Status: "online", Busy: true}}
	r.svc.Sleep = func(_ context.Context, d time.Duration) error {
		r.clock = r.clock.Add(d)
		if polls++; polls == 2 {
			r.gh.runners = []github.Runner{{Name: "box-1", Status: "online"}}
		}
		return nil
	}
	_, finish, _ := r.svc.BeginRemove(ctx, rec.ID, true, "u")
	if err := finish(ctx); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(lines, "\n")
	first := strings.Index(all, "asking whether the runner is idle")
	wait := strings.Index(all, "waiting for job")
	act := strings.Index(all, "the runner is idle; acting now")
	if first < 0 || wait < first || act < wait || !strings.Contains(all, "nothing is stopped") {
		t.Fatalf("drain log:\n%s", all)
	}
}

// runners R5.53: a runner's CPU count is the pool CPUs split over the job slots.
func TestAddedRunnerGetsItsCPUCount(t *testing.T) {
	m := machine("linux", "x64", true)
	m.PoolCPUs, m.JobSlots = 8, 2
	r := newRig(m)
	r.add(t)
	if got := r.ag.added[0].MaxParallel; got != 4 {
		t.Fatalf("MaxParallel = %d, want 4", got)
	}
	r = newRig(machine("linux", "x64", true))
	r.add(t)
	if got := r.ag.added[0].MaxParallel; got != 0 {
		t.Fatalf("no pool size must export no CPU count, got %d", got)
	}
}

// The controller's store keeps the reason of a failed runner and drops it when
// the runner leaves the failed state.
func TestMemStoreKeepsFailedReason(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	if err := m.InsertRunner(ctx, Record{ID: "r1", Name: "box-1", State: Creating}); err != nil {
		t.Fatal(err)
	}
	if err := m.SetRunnerState(ctx, "r1", Failed, "the account has no right to log on as a service"); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.GetRunner(ctx, "r1"); got.FailedReason != "the account has no right to log on as a service" {
		t.Errorf("reason = %q", got.FailedReason)
	}
	if err := m.SetRunnerState(ctx, "r1", Removing, "removing"); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.GetRunner(ctx, "r1"); got.FailedReason != "" {
		t.Errorf("reason = %q", got.FailedReason)
	}
}

// A runner registered on GitHub whose local record cannot be written (the
// 20 Nov "permission" failure) is rolled back: the registration, the service
// and the folder go, and the runner shows failed (runners R5.94).
func TestLocalRecordFailureAfterRegistrationRollsBack(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	r.store.svcErr = errors.New("permission denied")
	ctx := context.Background()
	p, _ := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "persistent", MachineID: "m1", Count: 1, Scope: Scope{Kind: "repository", RepositoryID: 1}}, "u")
	got, _ := r.svc.Apply(ctx, p.ID, nil, "u")
	if got.State != PlanFailed || !strings.Contains(got.Steps[0].Error, "permission denied") || !strings.Contains(got.Steps[0].Error, "Cleaned up: GitHub registration box-1") {
		t.Fatalf("%+v", got)
	}
	if len(r.ag.removed) != 1 || r.ag.removed[0].RemoveToken == "" || r.ag.removed[0].KeepFiles {
		t.Errorf("the agent must deregister with a remove token and remove the files: %+v", r.ag.removed)
	}
	all, _ := r.store.ListRunners(ctx, Filter{})
	if len(all) != 1 || all[0].State != Failed {
		t.Errorf("%+v", all)
	}
}

// When the rollback itself fails the error names what is left, for the Doctor.
func TestRollbackThatFailsNamesWhatIsLeft(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	r.store.svcErr = errors.New("permission denied")
	r.ag.rmErr = errors.New("agent offline")
	ctx := context.Background()
	p, _ := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "persistent", MachineID: "m1", Count: 1, Scope: Scope{Kind: "repository", RepositoryID: 1}}, "u")
	got, _ := r.svc.Apply(ctx, p.ID, nil, "u")
	if !strings.Contains(got.Steps[0].Error, "Left behind: GitHub registration box-1") {
		t.Fatalf("%+v", got.Steps[0])
	}
}

// Caches R5.57: a repository runner on Linux or Windows is added with the
// repository's shared folders and group; the switch and a missing repository
// turn it off.
func TestAddPassesSharedToolFoldersOfTheRepository(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	r.add(t)
	st := r.ag.added[0].SharedTools
	if st.Root != "/var/lib/crsp/tools/1" || st.Group != "crsp-repo-1" || !st.On() {
		t.Errorf("%+v", st)
	}
	r.svc.SharedToolsRoot = "/srv/tools/"
	if got := r.svc.sharedTools(AddSpec{OS: "linux", Scope: Scope{Kind: "repository", RepositoryID: 7}}, Target{}); got.Root != "/srv/tools/7" {
		t.Errorf("custom root: %+v", got)
	}
	r.svc.SharedToolsRoot = ""
	win := r.svc.sharedTools(AddSpec{OS: "windows", Scope: Scope{Kind: "repository", RepositoryID: 7}}, Target{})
	if win.Root != `C:\ProgramData\crsp\tools\7` || win.Group != "crsp-runners" {
		t.Errorf("windows: %+v", win)
	}
	for name, a := range map[string]AddSpec{
		"macos":      {OS: "macos", Scope: Scope{Kind: "repository", RepositoryID: 1}},
		"pool":       {OS: "linux", Scope: Scope{Kind: "pool"}},
		"no repo id": {OS: "linux", Scope: Scope{Kind: "repository"}},
	} {
		if r.svc.sharedTools(a, Target{}).On() {
			t.Errorf("%s: shared folders must be off", name)
		}
	}
	r.svc.NoSharedTools = true
	if r.svc.sharedTools(AddSpec{OS: "linux", Scope: Scope{Kind: "repository", RepositoryID: 1}}, Target{}).On() {
		t.Error("the switch turns it off")
	}
}
