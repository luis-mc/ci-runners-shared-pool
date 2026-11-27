package repos

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github/fake"
)

func TestSyncListsInstallationsAndAdminFlag(t *testing.T) { // R5.17, R5.18
	e := newEnv(t)
	e.world()
	ctx := context.Background()
	ins, err := e.svc.Installations(ctx, viewerP)
	if err != nil || len(ins) != 1 || ins[0].AccountLogin != "alice" || len(ins[0].MissingPermissions) != 0 {
		t.Fatalf("%+v %v", ins, err)
	}
	rows, err := e.svc.List(ctx, adminP, ListFilter{})
	if err != nil || len(rows) != 3 {
		t.Fatalf("%v %v", rows, err)
	}
	byName := map[string]RepositoryView{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	if !byName["web"].UserIsAdmin || !byName["secret"].UserIsAdmin || byName["other"].UserIsAdmin {
		t.Fatalf("admin flags: %+v", byName)
	}
	if byName["web"].Visibility != "public" || byName["web"].PoolOn {
		t.Fatalf("%+v", byName["web"])
	}
	if e.svc.LastSync(1).IsZero() {
		t.Fatal("the page is as old as the last sync")
	}
}

// OPS13: the list is read with the App's key, so it needs no user token.
func TestSyncNeedsNoGitHubToken(t *testing.T) {
	e := newEnv(t)
	e.world()
	delete(e.tokens.m, 1)
	if err := e.svc.Sync(context.Background(), adminP); err != nil {
		t.Fatalf("%v", err)
	}
	if e.svc.LastSync(1).IsZero() {
		t.Fatal("the sync made the page fresh")
	}
	rows, _ := e.svc.List(context.Background(), adminP, ListFilter{})
	if len(rows) != 3 {
		t.Fatalf("%d rows, want 3", len(rows))
	}
}

// When the App call fails the error is the App's, not a sign-in problem.
func TestSyncAppFailureIsNotSessionExpired(t *testing.T) {
	e := newEnv(t)
	e.world()
	delete(e.tokens.m, 1)
	e.fake.FailMatching("/installation/repositories", 403, 1)
	err := e.svc.Sync(context.Background(), adminP)
	if err == nil || errors.Is(err, auth.ErrGitHubSessionExpired) {
		t.Fatalf("sync error %v, want the App's error", err)
	}
	if !errors.Is(err, github.ErrForbidden) {
		t.Fatalf("sync error %v, want forbidden", err)
	}
}

// The admin flags still come from the user's own token when it is there.
func TestSyncKeepsAdminFlagsWithAToken(t *testing.T) {
	e := newEnv(t)
	e.world()
	if err := e.svc.Sync(context.Background(), adminP); err != nil {
		t.Fatal(err)
	}
	rows, _ := e.svc.List(context.Background(), adminP, ListFilter{})
	admins := 0
	for _, r := range rows {
		if r.UserIsAdmin {
			admins++
		}
	}
	if admins != 2 {
		t.Fatalf("%d admin flags, want 2", admins)
	}
}

func TestListManyRepositoriesPaged(t *testing.T) { // R6.11
	e := newEnv(t)
	tok := e.fake.AddUser(1, "alice")
	e.tokens.m[1] = github.UserToken{Value: tok}
	e.fake.AddInstall(10, "alice", "User", 1)
	for i := 1; i <= 1100; i++ {
		e.fake.AddRepo(int64(i), 10, "alice", fmt.Sprintf("r%04d", i), true, 1)
	}
	if err := e.svc.Sync(context.Background(), adminP); err != nil {
		t.Fatal(err)
	}
	var seen, after = 0, ""
	for pages := 0; pages < 20; pages++ { // 1100 repos at 100 a page: 11 full pages and an empty one
		page, err := e.svc.List(context.Background(), adminP, ListFilter{AfterName: after, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		seen += len(page)
		after = page[len(page)-1].FullName
	}
	if seen != 1100 {
		t.Fatalf("paged %d", seen)
	}
}

func TestListFilters(t *testing.T) {
	e := newEnv(t)
	e.world()
	e.turnOn(100)
	yes := true
	on, _ := e.svc.List(context.Background(), adminP, ListFilter{Pool: &yes})
	if len(on) != 1 || on[0].Name != "web" {
		t.Fatalf("%+v", on)
	}
	q, _ := e.svc.List(context.Background(), adminP, ListFilter{Query: "SECR"})
	if len(q) != 1 || q[0].Name != "secret" {
		t.Fatalf("%+v", q)
	}
}

func TestPlanChangesNothingChanges(t *testing.T) { // R5.20
	e := newEnv(t)
	e.world()
	ctx := context.Background()
	plan, err := e.svc.PlanChanges(ctx, adminP, []Change{{RepositoryID: 100, Pool: true}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.State != PlanPlanned || len(plan.Steps) < 2 {
		t.Fatalf("%+v", plan)
	}
	r, _ := e.store.GetRepository(ctx, 100)
	if r.PoolOn {
		t.Fatal("a plan changes nothing")
	}
	if _, ok := e.fake.Variable("alice/web", "CRSP_RUNS_ON_LINUX"); ok {
		t.Fatal("a plan writes nothing to GitHub")
	}
	if e.fake.Count("PATCH", "/repos") != 0 || e.fake.Count("POST", "/repos/alice/web/actions/variables") != 0 {
		t.Fatal("no write call during planning")
	}
	if plan.Steps[0].Action != ActPoolOn || plan.Steps[1].Action != ActWriteVariable || plan.Steps[1].Target != "CRSP_RUNS_ON_LINUX" {
		t.Fatalf("steps: %+v", plan.Steps)
	}
}

func TestEnableWritesVariablesAndGuardsForks(t *testing.T) { // R5.20, R5.26, R5.28
	e := newEnv(t)
	e.world()
	ctx := context.Background()
	e.turnOn(100, 101)
	v, ok := e.fake.Variable("alice/web", "CRSP_RUNS_ON_LINUX")
	// alice/web is public: it defaults to ephemeral runners only (R5.60).
	if !ok || v != `["self-hosted","linux","x64","docker","crsp-home","ephemeral"]` {
		t.Fatalf("variable %q %v", v, ok)
	}
	if _, ok := e.fake.Variable("alice/web", "CRSP_RUNS_ON_WINDOWS"); ok {
		t.Fatal("no variable for an OS no machine serves")
	}
	r, _ := e.store.GetRepository(ctx, 100)
	if !r.PoolOn || r.PoolChangedBy != "alice" {
		t.Fatalf("%+v", r)
	}
	pol, err := e.store.GetPolicy(ctx, 100)
	if err != nil || pol.MaxParallelJobs != 2 || pol.Priority != 0 || pol.AllowForkPRs {
		t.Fatalf("defaults (R5.25): %+v %v", pol, err)
	}
	if e.fake.ForkApprovalOf("alice/web") != github.ForkApprovalAllOutside {
		t.Fatal("web is public: outside contributors' runs wait for approval (R5.26)")
	}
	if e.fake.ForkApprovalOf("alice/secret") != "" {
		t.Fatal("a private repository needs no setting")
	}
	for _, a := range []string{"pool.on", "variable.written"} {
		if !e.audit.has(a) {
			t.Errorf("audit %s", a)
		}
	}
	mv, _ := e.store.ListManagedVariables(ctx, 100)
	if len(mv) != 1 || mv[0].Status != VarOK {
		t.Fatalf("%+v", mv)
	}
}

func TestForkApprovalSetOnPublicRepository(t *testing.T) { // R5.26
	e := newEnv(t)
	tok := e.fake.AddUser(1, "alice")
	e.tokens.m[1] = github.UserToken{Value: tok}
	e.fake.AddInstall(10, "alice", "User", 1)
	e.fake.AddRepo(100, 10, "alice", "open", false, 1)
	e.fake.Lock(func() { e.fake.Repos[100].Private = false })
	// The fake marks private=false as public.
	if err := e.svc.Sync(context.Background(), adminP); err != nil {
		t.Fatal(err)
	}
	r, _ := e.store.GetRepository(context.Background(), 100)
	if r.Visibility != "public" {
		t.Fatalf("%s", r.Visibility)
	}
	e.turnOn(100)
	if got := e.fake.ForkApprovalOf("alice/open"); got != github.ForkApprovalAllOutside {
		t.Fatalf("fork approval policy %q", got)
	}
}

func TestEnableNeedsGitHubAdminFresh(t *testing.T) { // R5.19
	e := newEnv(t)
	e.world()
	ctx := context.Background()
	// 102: alice is not a GitHub admin.
	plan, err := e.svc.PlanChanges(ctx, adminP, []Change{{RepositoryID: 102, Pool: true}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.ApplyPlan(ctx, adminP, plan.ID, nil)
	var na *ErrNotAdmin
	if !errors.As(err, &na) || !errors.Is(err, auth.ErrForbidden) || !strings.Contains(err.Error(), "alice/other") {
		t.Fatalf("%v", err)
	}
	if r, _ := e.store.GetRepository(ctx, 102); r.PoolOn {
		t.Fatal("nothing may change")
	}
	// Rights read fresh: lose admin on 100 after the listing says admin.
	plan, _ = e.svc.PlanChanges(ctx, adminP, []Change{{RepositoryID: 100, Pool: true}})
	e.fake.Lock(func() { delete(e.fake.Repos[100].Admins, 1) })
	if _, err := e.svc.ApplyPlan(ctx, adminP, plan.ID, nil); !errors.As(err, &na) {
		t.Fatalf("the cached admin flag must not be trusted: %v", err)
	}
	if len(e.fake.Requests()) == 0 || e.fake.Count("GET", "/repos/alice/web") == 0 {
		t.Fatal("GitHub must be asked at the moment of the change")
	}
}

func TestAdminCheckFallsBackToAppWhenTokenGone(t *testing.T) { // R5.19
	e := newEnv(t)
	e.world()
	ctx := context.Background()
	delete(e.tokens.m, 1)
	plan, _ := e.svc.PlanChanges(ctx, adminP, []Change{{RepositoryID: 100, Pool: true}})
	if _, err := e.svc.ApplyPlan(ctx, adminP, plan.ID, nil); err != nil {
		t.Fatalf("a CLI user or an expired token is checked as the App: %v", err)
	}
	plan2, _ := e.svc.PlanChanges(ctx, adminP, []Change{{RepositoryID: 102, Pool: true}})
	var na *ErrNotAdmin
	if _, err := e.svc.ApplyPlan(ctx, adminP, plan2.ID, nil); !errors.As(err, &na) {
		t.Fatalf("%v", err)
	}
}

func TestViewerCannotPlanOrApply(t *testing.T) { // R5.12
	e := newEnv(t)
	e.world()
	ctx := context.Background()
	if _, err := e.svc.PlanChanges(ctx, viewerP, []Change{{RepositoryID: 100, Pool: true}}); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal(err)
	}
	plan, _ := e.svc.PlanChanges(ctx, adminP, []Change{{RepositoryID: 100, Pool: true}})
	if _, err := e.svc.ApplyPlan(ctx, viewerP, plan.ID, nil); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdatePolicy(ctx, viewerP, 100, PolicyUpdate{}); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := e.svc.GetPlan(ctx, viewerP, plan.ID); err != nil {
		t.Fatal("a viewer reads plans")
	}
}

func TestPlanApplyOnceAndStale(t *testing.T) { // R5.20
	e := newEnv(t)
	e.world()
	ctx := context.Background()
	p1, _ := e.svc.PlanChanges(ctx, adminP, []Change{{RepositoryID: 100, Pool: true}})
	p2, _ := e.svc.PlanChanges(ctx, adminP, []Change{{RepositoryID: 100, Pool: true}})
	if _, err := e.svc.ApplyPlan(ctx, adminP, p1.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ApplyPlan(ctx, adminP, p1.ID, nil); !errors.Is(err, ErrPlanNotPlanned) {
		t.Fatalf("applies once: %v", err)
	}
	got, err := e.svc.ApplyPlan(ctx, adminP, p2.ID, nil)
	if !errors.Is(err, ErrPlanStale) || got.State != PlanStale {
		t.Fatalf("the state changed after the plan: %v %+v", err, got)
	}
	if _, err := e.svc.PlanChanges(ctx, adminP, []Change{{RepositoryID: 100, Pool: true}}); !errors.Is(err, ErrNothingToChange) {
		t.Fatalf("%v", err)
	}
	if _, err := e.svc.PlanChanges(ctx, adminP, nil); err == nil {
		t.Fatal("empty")
	}
	if _, err := e.svc.PlanChanges(ctx, adminP, []Change{{RepositoryID: 999, Pool: true}}); !errors.Is(err, auth.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := e.svc.PlanChanges(ctx, adminP, []Change{{RepositoryID: 101, Pool: true}, {RepositoryID: 101, Pool: true}}); err == nil {
		t.Fatal("a repository twice")
	}
	saved, _ := e.svc.GetPlan(ctx, adminP, p1.ID)
	if saved.State != PlanDone || saved.Steps[0].State != StepDone {
		t.Fatalf("done after reload shows the stored outcome: %+v", saved)
	}
}

func TestFailedStepStopsPlan(t *testing.T) { // R6.7, section 8
	e := newEnv(t)
	e.world()
	ctx := context.Background()
	plan, _ := e.svc.PlanChanges(ctx, adminP, []Change{{RepositoryID: 101, Pool: true}})
	e.fake.FailMatching("/actions/variables", 500, 10) // the variable write fails for good
	got, err := e.svc.ApplyPlan(ctx, adminP, plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != PlanFailed {
		t.Fatalf("%+v", got)
	}
	var failed, skipped int
	for _, s := range got.Steps {
		switch s.State {
		case StepFailed:
			failed++
			if s.Error == "" || strings.Contains(s.Error, "http") {
				t.Fatalf("a failed step says why in words: %q", s.Error)
			}
		case StepSkipped:
			skipped++
		}
	}
	if failed != 1 {
		t.Fatalf("%+v", got.Steps)
	}
	_ = skipped
}

func TestPoolOffNamesVariablesAndDeletesOnlyAfterYes(t *testing.T) { // R5.20, R5.29
	e := newEnv(t)
	e.world()
	ctx := context.Background()
	e.turnOn(101)
	e.fake.SetWorkflow("alice/secret", ".github/workflows/ci.yml",
		"on: push\njobs:\n  build:\n    runs-on: ${{ fromJSON(vars.CRSP_RUNS_ON_LINUX) }}\n    steps: []\n")
	e.fake.SetWorkflow("alice/secret", ".github/workflows/lint.yml", "on: push\njobs:\n  l:\n    runs-on: ubuntu-latest\n")

	plan, err := e.svc.PlanChanges(ctx, adminP, []Change{{RepositoryID: 101, Pool: false}})
	if err != nil {
		t.Fatal(err)
	}
	var del *Step
	for i := range plan.Steps {
		if plan.Steps[i].Action == ActDeleteVariable {
			del = &plan.Steps[i]
		}
	}
	if del == nil || !del.NeedsYes || del.Target != "CRSP_RUNS_ON_LINUX" {
		t.Fatalf("%+v", plan.Steps)
	}
	for _, want := range []string{"CRSP_RUNS_ON_LINUX", "fail to start", ".github/workflows/ci.yml"} {
		if !strings.Contains(del.Text, want) {
			t.Errorf("the question must name %q: %s", want, del.Text)
		}
	}
	if strings.Contains(del.Text, "lint.yml") {
		t.Error("only workflows that read the variable are named")
	}
	if _, ok := e.fake.Variable("alice/secret", "CRSP_RUNS_ON_LINUX"); !ok {
		t.Fatal("planning deletes nothing")
	}

	// Without the yes: the pool goes off, the variable stays.
	got, err := e.svc.ApplyPlan(ctx, adminP, plan.ID, nil)
	if err != nil || got.State != PlanDone {
		t.Fatalf("%v %+v", err, got)
	}
	if _, ok := e.fake.Variable("alice/secret", "CRSP_RUNS_ON_LINUX"); !ok {
		t.Fatal("a variable is deleted only after a yes")
	}
	if r, _ := e.store.GetRepository(ctx, 101); r.PoolOn {
		t.Fatal("pool is off")
	}
	skipped := false
	for _, s := range got.Steps {
		if s.Action == ActDeleteVariable && s.State == StepSkipped {
			skipped = true
		}
	}
	if !skipped {
		t.Fatalf("%+v", got.Steps)
	}

	// With the yes.
	e.turnOnAgain(101)
	plan, _ = e.svc.PlanChanges(ctx, adminP, []Change{{RepositoryID: 101, Pool: false}})
	var id string
	for _, s := range plan.Steps {
		if s.Action == ActDeleteVariable {
			id = s.ID
		}
	}
	got, err = e.svc.ApplyPlan(ctx, adminP, plan.ID, []string{id})
	if err != nil || got.State != PlanDone {
		t.Fatalf("%v %+v", err, got)
	}
	if _, ok := e.fake.Variable("alice/secret", "CRSP_RUNS_ON_LINUX"); ok {
		t.Fatal("deleted after the yes")
	}
	mv, _ := e.store.ListManagedVariables(ctx, 101)
	if len(mv) != 0 {
		t.Fatalf("%+v", mv)
	}
	if !e.audit.has("variable.deleted") || !e.audit.has("pool.off") {
		t.Fatal("audit")
	}
}

func (e *env) turnOnAgain(id int64) {
	e.t.Helper()
	e.turnOn(id)
}

func TestPoolOffGitHubDownFailsClosed(t *testing.T) { // section 8
	e := newEnv(t)
	e.world()
	e.turnOn(101)
	e.fake.FailNext(503)
	if _, err := e.svc.PlanChanges(context.Background(), adminP, []Change{{RepositoryID: 101, Pool: false}}); !errors.Is(err, github.ErrUnavailable) {
		t.Fatalf("%v", err)
	}
	_ = fake.Job{}
}

// OPS15: after a restart the user's token is gone; the flags are read with the
// App, the way requireGitHubAdmin does, so the owner is not told "not admin".
func TestSyncWithoutTokenReadsAdminWithTheApp(t *testing.T) {
	e := newEnv(t)
	e.world()
	delete(e.tokens.m, 1)
	e.svc.admin = map[int64]map[int64]bool{} // a fresh process knows nothing
	ctx := context.Background()
	if err := e.svc.Sync(ctx, adminP); err != nil {
		t.Fatal(err)
	}
	rows, _ := e.svc.List(ctx, adminP, ListFilter{})
	for _, r := range rows {
		if !r.AdminKnown {
			t.Fatalf("%s: flag unknown, the App could say", r.Name)
		}
		// alice owns the account installation: admin on every repository in it.
		if !r.UserIsAdmin {
			t.Fatalf("%s: owner shown as not admin", r.Name)
		}
	}
}

// A user who does not own the account is asked with CollaboratorPermission.
func TestSyncWithoutTokenAsksPermissionForOthers(t *testing.T) {
	e := newEnv(t)
	e.world()
	bob := auth.Principal{UserID: 2, Login: "bob", Role: auth.RoleAdmin, Source: "web"}
	ctx := context.Background()
	if err := e.svc.Sync(ctx, bob); err != nil {
		t.Fatal(err)
	}
	rows, _ := e.svc.List(ctx, bob, ListFilter{})
	if len(rows) != 3 {
		t.Fatalf("%d rows", len(rows))
	}
	for _, r := range rows {
		if !r.AdminKnown || r.UserIsAdmin {
			t.Fatalf("%s: known=%v admin=%v, want a known non-admin", r.Name, r.AdminKnown, r.UserIsAdmin)
		}
	}
}

// A GitHub error is "not checked", never "not admin".
func TestSyncWithoutTokenAppErrorLeavesFlagUnknown(t *testing.T) {
	e := newEnv(t)
	e.world()
	bob := auth.Principal{UserID: 2, Login: "bob", Role: auth.RoleAdmin, Source: "web"}
	e.fake.FailMatching("/collaborators/", 500, 100)
	ctx := context.Background()
	if err := e.svc.Sync(ctx, bob); err != nil {
		t.Fatalf("the list does not depend on the flags: %v", err)
	}
	rows, _ := e.svc.List(ctx, bob, ListFilter{})
	for _, r := range rows {
		if r.AdminKnown || r.UserIsAdmin {
			t.Fatalf("%s: known=%v admin=%v, want unknown", r.Name, r.AdminKnown, r.UserIsAdmin)
		}
	}
}

// R5.106: opting a repository in calls the routing hook once, after the pool
// is on and the variables are written; the hook's work never blocks the opt-in.
func TestEnableCallsRoutingHook(t *testing.T) {
	e := newEnv(t)
	e.world()
	var called []string
	e.svc.cfg.OnPoolOn = func(_ context.Context, r Repository) {
		v, _ := e.fake.Variable(r.FullName, "CRSP_RUNS_ON_LINUX")
		called = append(called, r.FullName+" "+v)
	}
	e.turnOn(101)
	if len(called) != 1 || !strings.HasPrefix(called[0], "alice/secret [\"self-hosted\"") {
		t.Fatalf("hook calls %v", called)
	}
}
