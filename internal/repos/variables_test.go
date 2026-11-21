package repos

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
)

func TestDesiredVariables(t *testing.T) { // R5.28
	e := newEnv(t)
	e.cap.m = map[string][]string{"Linux": {"X64", "crsp-home"}, "Windows": {"X64", "crsp-home"}}
	ctx := context.Background()
	got, err := e.svc.Desired(ctx, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["RUNS_ON_LINUX"] != `["self-hosted","linux","x64","crsp-home"]` ||
		got["RUNS_ON_WINDOWS"] != `["self-hosted","windows","x64","crsp-home"]` {
		t.Fatalf("%v", got)
	}
	if _, ok := got["RUNS_ON_MACOS"]; ok {
		t.Fatal("no variable for an OS no machine serves")
	}
	// The policy enables only the OS it names.
	got, _ = e.svc.Desired(ctx, Policy{Labels: []string{"windows", "gpu"}})
	if len(got) != 1 || got["RUNS_ON_WINDOWS"] != `["self-hosted","windows","x64","crsp-home","gpu"]` {
		t.Fatalf("%v", got)
	}
	// Policy that names an OS no machine serves: nothing to write.
	got, _ = e.svc.Desired(ctx, Policy{Labels: []string{"macos"}})
	if len(got) != 0 {
		t.Fatalf("%v", got)
	}
	// Valid JSON lists of strings, always.
	for _, v := range []string{`["self-hosted","Linux"]`} {
		var l []string
		if json.Unmarshal([]byte(v), &l) != nil {
			t.Fatal("json")
		}
	}
}

func TestVariableDriftRestoredAndAudited(t *testing.T) { // R5.28
	e := newEnv(t)
	e.world()
	e.turnOn(101)
	ctx := context.Background()
	good, _ := e.fake.Variable("alice/secret", "RUNS_ON_LINUX")

	if err := e.svc.ReconcileVariables(ctx, 101); err != nil {
		t.Fatal(err)
	}
	if mv, _ := e.store.ListManagedVariables(ctx, 101); mv[0].Status != VarOK {
		t.Fatalf("%+v", mv)
	}
	writes := e.fake.Count("PATCH", "/repos/alice/secret/actions/variables")

	e.fake.SetVariable("alice/secret", "RUNS_ON_LINUX", `["ubuntu-latest"]`) // changed outside the product
	if err := e.svc.ReconcileVariables(ctx, 101); err != nil {
		t.Fatal(err)
	}
	if v, _ := e.fake.Variable("alice/secret", "RUNS_ON_LINUX"); v != good {
		t.Fatalf("restored: %s", v)
	}
	if e.fake.Count("PATCH", "/repos/alice/secret/actions/variables") != writes+1 {
		t.Fatal("exactly one restoring write")
	}
	found := false
	for _, ev := range e.audit.events {
		if strings.HasPrefix(ev, "variable.restored|controller|alice/secret|") && strings.Contains(ev, "ubuntu-latest") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the audit log records the drift: %v", e.audit.events)
	}
	mv, _ := e.store.ListManagedVariables(ctx, 101)
	if mv[0].Status != VarWrong {
		t.Fatalf("the row shows drift until the next check finds it right: %+v", mv)
	}
	if err := e.svc.ReconcileVariables(ctx, 101); err != nil {
		t.Fatal(err)
	}
	if mv, _ := e.store.ListManagedVariables(ctx, 101); mv[0].Status != VarOK {
		t.Fatalf("%+v", mv)
	}
	v, _ := e.svc.Get(ctx, adminP, 101)
	if v.VariablesDrift {
		t.Fatal("no drift left")
	}
}

func TestMissingVariableRestored(t *testing.T) { // R5.28
	e := newEnv(t)
	e.world()
	e.turnOn(101)
	ctx := context.Background()
	e.fake.Lock(func() { delete(e.fake.Variables["alice/secret"], "RUNS_ON_LINUX") })
	if err := e.svc.ReconcileVariables(ctx, 101); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.fake.Variable("alice/secret", "RUNS_ON_LINUX"); !ok {
		t.Fatal("deleted outside the product: written back")
	}
	if mv, _ := e.store.ListManagedVariables(ctx, 101); mv[0].Status != VarMissing {
		t.Fatalf("%+v", mv)
	}
}

func TestReconcileSkipsPoolOff(t *testing.T) { // R5.28
	e := newEnv(t)
	e.world()
	before := len(e.fake.Requests())
	if err := e.svc.ReconcileVariables(context.Background(), 101); err != nil {
		t.Fatal(err)
	}
	if len(e.fake.Requests()) != before {
		t.Fatal("a repository with the pool off is not touched")
	}
}

func TestReconcileAllStopsAtRateLimit(t *testing.T) { // R6.6
	e := newEnv(t)
	e.world()
	e.turnOn(100, 101)
	e.fake.Lock(func() { e.fake.RateRemaining = 100 })
	// One user-driven call records the low budget.
	_, _ = e.gh.ListVariables(context.Background(), 10, "alice", "web")
	e.fake.SetVariable("alice/web", "RUNS_ON_LINUX", "[]")
	err := e.svc.ReconcileAllVariables(context.Background())
	var rl *github.RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("background work stops past half the limit: %v", err)
	}
	if v, _ := e.fake.Variable("alice/web", "RUNS_ON_LINUX"); v != "[]" {
		t.Fatal("no write while the budget is spent")
	}
	e.advance(3 * time.Hour)
	e.fake.Lock(func() { e.fake.RateRemaining = 5000 })
	if err := e.svc.ReconcileAllVariables(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v, _ := e.fake.Variable("alice/web", "RUNS_ON_LINUX"); v == "[]" {
		t.Fatal("restored after the reset")
	}
}

func varNames(e *env, repo string) string {
	return strings.Join(e.fake.VariableNames(repo), ",")
}

func TestVariableSetAndDeletedPerOS(t *testing.T) { // R5.28
	e := newEnv(t)
	e.world()
	e.cap.m = map[string][]string{"Linux": {"X64"}}
	e.turnOn(101)
	if got := varNames(e, "alice/secret"); got != "RUNS_ON_LINUX" {
		t.Fatalf("one runner OS, one variable: %s", got)
	}
	ctx := context.Background()

	// A first Windows runner arrives: its variable appears, Linux stays.
	e.cap.m = map[string][]string{"Linux": {"X64"}, "Windows": {"X64"}}
	if err := e.svc.RunnersChanged(ctx); err != nil {
		t.Fatal(err)
	}
	if got := varNames(e, "alice/secret"); got != "RUNS_ON_LINUX,RUNS_ON_WINDOWS" {
		t.Fatalf("runner added: %s", got)
	}
	// The last Linux runner goes: its variable is deleted, not left unset-looking.
	e.cap.m = map[string][]string{"Windows": {"X64"}}
	e.advance(CapacitySettle)
	for i := 0; i < 2; i++ { // two syncs, DeleteHold apart
		if err := e.svc.RunnersChanged(ctx); err != nil {
			t.Fatal(err)
		}
		e.advance(DeleteHold)
	}
	if got := varNames(e, "alice/secret"); got != "RUNS_ON_WINDOWS" {
		t.Fatalf("runner removed: %s", got)
	}
	if mv, _ := e.store.ListManagedVariables(ctx, 101); len(mv) != 1 || mv[0].Name != "RUNS_ON_WINDOWS" {
		t.Fatalf("the store forgets a deleted variable: %+v", mv)
	}
	if !e.audit.has("variable.deleted") {
		t.Fatal("audit")
	}
	// No runner at all: no variable, workflows fall back to GitHub-hosted.
	e.cap.m = map[string][]string{}
	for i := 0; i < 2; i++ {
		if err := e.svc.RunnersChanged(ctx); err != nil {
			t.Fatal(err)
		}
		e.advance(DeleteHold)
	}
	if got := varNames(e, "alice/secret"); got != "" {
		t.Fatalf("no runner: %s", got)
	}
}

func TestVariableDeletedWhenPoolTurnedOffForAnOS(t *testing.T) { // R5.28
	e := newEnv(t)
	e.world()
	e.cap.m = map[string][]string{"Linux": {"X64"}, "Windows": {"X64"}}
	e.turnOn(101)
	if got := varNames(e, "alice/secret"); got != "RUNS_ON_LINUX,RUNS_ON_WINDOWS" {
		t.Fatalf("%s", got)
	}
	// The policy now enables Windows only: the pool is off for Linux.
	if _, err := e.svc.UpdatePolicy(context.Background(), adminP, 101, PolicyUpdate{Labels: sp("windows")}); err != nil {
		t.Fatal(err)
	}
	if got := varNames(e, "alice/secret"); got != "RUNS_ON_WINDOWS" {
		t.Fatalf("pool off for Linux: %s", got)
	}
	// Back to every OS: the variable returns.
	if _, err := e.svc.UpdatePolicy(context.Background(), adminP, 101, PolicyUpdate{Labels: sp()}); err != nil {
		t.Fatal(err)
	}
	if got := varNames(e, "alice/secret"); got != "RUNS_ON_LINUX,RUNS_ON_WINDOWS" {
		t.Fatalf("pool on again: %s", got)
	}
}

func TestVariablesDeletedWhenPoolOff(t *testing.T) { // R5.28, R5.29
	e := newEnv(t)
	e.world()
	e.turnOn(101)
	ctx := context.Background()
	plan, err := e.svc.PlanChanges(ctx, adminP, []Change{{RepositoryID: 101, Pool: false}})
	if err != nil {
		t.Fatal(err)
	}
	var yes []string
	for _, st := range plan.Steps {
		if st.Action == ActDeleteVariable {
			yes = append(yes, st.ID)
		}
	}
	if _, err := e.svc.ApplyPlan(ctx, adminP, plan.ID, yes); err != nil {
		t.Fatal(err)
	}
	if got := varNames(e, "alice/secret"); got != "" {
		t.Fatalf("pool off: %s", got)
	}
}

func TestLegacyVariablesMigrated(t *testing.T) { // R5.28 (CRSP25)
	e := newEnv(t)
	e.world()
	ctx := context.Background()
	// An older version wrote CRSP_RUNS_ON_LINUX and recorded it; the user also
	// has a variable of their own with a similar name that crsp never wrote.
	e.fake.SetVariable("alice/secret", "CRSP_RUNS_ON_LINUX", `["self-hosted","Linux"]`)
	e.fake.SetVariable("alice/secret", "CRSP_RUNS_ON_WINDOWS", `["mine"]`)
	_ = e.store.UpsertManagedVariable(ctx, ManagedVariable{RepositoryID: 101, Name: "CRSP_RUNS_ON_LINUX", Value: `["self-hosted","Linux"]`, Status: VarOK})
	e.turnOn(101)
	if got := varNames(e, "alice/secret"); got != "CRSP_RUNS_ON_WINDOWS,RUNS_ON_LINUX" {
		t.Fatalf("old name deleted, new written, foreign one kept: %s", got)
	}
	mv, _ := e.store.ListManagedVariables(ctx, 101)
	if len(mv) != 1 || mv[0].Name != "RUNS_ON_LINUX" {
		t.Fatalf("%+v", mv)
	}

	// The upgrade also reaches a repository whose pool is off.
	e.fake.SetVariable("alice/web", "CRSP_RUNS_ON_MACOS", `["self-hosted","macOS"]`)
	_ = e.store.UpsertManagedVariable(ctx, ManagedVariable{RepositoryID: 100, Name: "CRSP_RUNS_ON_MACOS", Value: "x", Status: VarOK})
	if err := e.svc.ReconcileAllVariables(ctx); err != nil {
		t.Fatal(err)
	}
	if got := varNames(e, "alice/web"); got != "" {
		t.Fatalf("pool-off repository: %s", got)
	}
}

func TestLabelsCompareCaseInsensitively(t *testing.T) { // R5.28
	if !sameLabels(`["self-hosted","Linux","X64"]`, `["self-hosted","linux","x64"]`) {
		t.Fatal("case is not a difference")
	}
	if sameLabels(`["self-hosted","linux"]`, `["self-hosted","linux","x64"]`) || sameLabels("not json", `[]`) {
		t.Fatal("different labels differ")
	}
	e := newEnv(t)
	e.world()
	e.turnOn(101)
	// Changed outside the product only in case: not drift, no write.
	e.fake.SetVariable("alice/secret", "RUNS_ON_LINUX", `["Self-Hosted","LINUX","X64","docker","crsp-home"]`)
	writes := e.fake.Count("PATCH", "/repos/alice/secret/actions/variables")
	if err := e.svc.ReconcileVariables(context.Background(), 101); err != nil {
		t.Fatal(err)
	}
	if e.fake.Count("PATCH", "/repos/alice/secret/actions/variables") != writes {
		t.Fatal("a case-only difference is left alone")
	}
	if mv, _ := e.store.ListManagedVariables(context.Background(), 101); mv[0].Status != VarOK {
		t.Fatalf("%+v", mv)
	}
	// Labels are written lowercase whatever case the machine reports.
	e.cap.m = map[string][]string{"Linux": {"X64", "Crsp-Home"}}
	got, _ := e.svc.Desired(context.Background(), DefaultPolicy())
	if got["RUNS_ON_LINUX"] != `["self-hosted","linux","x64","crsp-home"]` {
		t.Fatalf("%v", got)
	}
}

// R5.39: legacy_vars writes the old names with the same labels, removes them
// with the setting, and never touches a CI_RUNS_ON the controller did not write.
func TestLegacyNamesWrittenAndRemovedWithSetting(t *testing.T) {
	e := newEnv(t)
	e.world()
	e.cap.m = map[string][]string{"Linux": {"X64"}, "Windows": {"X64"}}
	ctx := context.Background()
	e.turnOn(101)
	if got := varNames(e, "alice/secret"); got != "RUNS_ON_LINUX,RUNS_ON_WINDOWS" {
		t.Fatalf("off by default: %s", got)
	}
	// NIGHTLY_RUNS_ON is the owner's: never touched.
	e.fake.SetVariable("alice/secret", "NIGHTLY_RUNS_ON", `["mine"]`)

	on := true
	if _, err := e.svc.UpdatePolicy(ctx, adminP, 101, PolicyUpdate{LegacyVars: &on}); err != nil {
		t.Fatal(err)
	}
	if got := varNames(e, "alice/secret"); got != "CI_RUNS_ON,CI_RUNS_ON_WINDOWS,NIGHTLY_RUNS_ON,RUNS_ON_LINUX,RUNS_ON_WINDOWS" {
		t.Fatalf("on: %s", got)
	}
	lin, _ := e.fake.Variable("alice/secret", "RUNS_ON_LINUX")
	old, _ := e.fake.Variable("alice/secret", "CI_RUNS_ON")
	win, _ := e.fake.Variable("alice/secret", "RUNS_ON_WINDOWS")
	oldWin, _ := e.fake.Variable("alice/secret", "CI_RUNS_ON_WINDOWS")
	if lin != old || win != oldWin || lin == "" {
		t.Fatalf("same labels: %q %q %q %q", lin, old, win, oldWin)
	}

	// Drift in an old name is put back.
	e.fake.SetVariable("alice/secret", "CI_RUNS_ON", `["x"]`)
	if err := e.svc.ReconcileVariables(ctx, 101); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.fake.Variable("alice/secret", "CI_RUNS_ON"); got != lin {
		t.Fatalf("restored: %q", got)
	}

	// The OS loses its policy: its old name goes with it (same rules as R5.28).
	if _, err := e.svc.UpdatePolicy(ctx, adminP, 101, PolicyUpdate{Labels: sp("windows")}); err != nil {
		t.Fatal(err)
	}
	if got := varNames(e, "alice/secret"); got != "CI_RUNS_ON_WINDOWS,NIGHTLY_RUNS_ON,RUNS_ON_WINDOWS" {
		t.Fatalf("linux gone: %s", got)
	}

	// Setting off: the old names go, the new ones stay.
	off := false
	if _, err := e.svc.UpdatePolicy(ctx, adminP, 101, PolicyUpdate{Labels: sp(), LegacyVars: &off}); err != nil {
		t.Fatal(err)
	}
	if got := varNames(e, "alice/secret"); got != "NIGHTLY_RUNS_ON,RUNS_ON_LINUX,RUNS_ON_WINDOWS" {
		t.Fatalf("off: %s", got)
	}

	// An old name the old pool wrote (not in the store) is left alone.
	e.fake.SetVariable("alice/secret", "CI_RUNS_ON", `["old-pool"]`)
	if err := e.svc.ReconcileVariables(ctx, 101); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.fake.Variable("alice/secret", "CI_RUNS_ON"); got != `["old-pool"]` {
		t.Fatalf("not ours: %q", got)
	}
}

func TestRestartWithNoFactsDeletesNothing(t *testing.T) { // R5.28
	e := newEnv(t)
	e.world()
	e.turnOn(101)
	ctx := context.Background()
	if got := varNames(e, "alice/secret"); got != "RUNS_ON_LINUX" {
		t.Fatalf("setup: %s", got)
	}
	// Restart: a new service, no machine has reported yet, so no capacity.
	e.cap.m = map[string][]string{}
	e.svc = New(Config{Store: e.store, GitHub: e.gh, Tokens: e.tokens, Audit: e.audit, Capacity: e.cap, Now: e.clock,
		PublicRepositories: func(context.Context) (bool, error) { return true, nil }})
	before := e.audit.count("variable.deleted")
	for i := 0; i < 5; i++ { // many syncs inside the settle period
		if err := e.svc.RunnersChanged(ctx); err != nil {
			t.Fatal(err)
		}
		e.advance(CapacitySettle / 6)
	}
	if got := varNames(e, "alice/secret"); got != "RUNS_ON_LINUX" {
		t.Fatalf("deleted before capacity was known: %q", got)
	}
	// Facts arrive late: nothing was deleted, so nothing is restored.
	e.cap.m = map[string][]string{"Linux": {"X64", "docker", "crsp-home"}}
	if err := e.svc.RunnersChanged(ctx); err != nil {
		t.Fatal(err)
	}
	if e.audit.count("variable.deleted") != before || e.audit.has("variable.restored") {
		t.Fatalf("a delete or restore happened: %v", e.audit.events)
	}
	if got := varNames(e, "alice/secret"); got != "RUNS_ON_LINUX" {
		t.Fatalf("after facts: %q", got)
	}
}

func TestRealLossDeletesOnlyAfterHold(t *testing.T) { // R5.28
	e := newEnv(t)
	e.world()
	e.turnOn(101)
	ctx := context.Background()
	e.advance(CapacitySettle)
	e.cap.m = map[string][]string{}
	sync := func() {
		if err := e.svc.RunnersChanged(ctx); err != nil {
			t.Fatal(err)
		}
	}
	sync() // first missing reading
	if got := varNames(e, "alice/secret"); got != "RUNS_ON_LINUX" {
		t.Fatalf("one reading deleted: %q", got)
	}
	e.advance(DeleteHold - time.Minute)
	sync()
	if got := varNames(e, "alice/secret"); got != "RUNS_ON_LINUX" {
		t.Fatalf("deleted before the hold: %q", got)
	}
	// The OS comes back for one reading: the count starts again.
	e.cap.m = map[string][]string{"Linux": {"X64"}}
	sync()
	e.cap.m = map[string][]string{}
	e.advance(DeleteHold)
	sync()
	if got := varNames(e, "alice/secret"); got != "RUNS_ON_LINUX" {
		t.Fatalf("a flap deleted: %q", got)
	}
	e.advance(DeleteHold)
	sync()
	if got := varNames(e, "alice/secret"); got != "" {
		t.Fatalf("a real loss was not deleted: %q", got)
	}
	if d := e.audit.detail("variable.deleted"); !strings.Contains(d, "no Linux runner for 15 min") {
		t.Fatalf("the audit says why: %q", d)
	}
}
