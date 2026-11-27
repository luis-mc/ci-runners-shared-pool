package retention

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// R5.43: a replaced image goes after its rollback days, alone, and the date is shown ahead (R5.44).
func TestSupersededActsAloneAfterKeepUntil(t *testing.T) {
	e := newEnv(t)
	e.repo.add(registry.Image{ID: "old", State: registry.StateSuperseded, KeepUntil: day(2), PromotedAt: day(-10)})
	must(t, e.svc.Evaluate(context.Background(), t0))
	r, ok := e.recFor(registry.RuleSuperseded, "old")
	if !ok || r.Decider != registry.DecidedByCrsp || !r.DueAt.Equal(day(2)) {
		t.Fatalf("want a crsp recommendation due in 2 days, got %+v ok=%v", r, ok)
	}
	if len(e.reg.deleted) != 0 {
		t.Fatal("deleted before the rollback days ended")
	}
	e.now = day(2)
	must(t, e.svc.Evaluate(context.Background(), e.now))
	if len(e.reg.deleted) != 1 || e.repo.images["old"].State != registry.StateDeleted {
		t.Fatalf("not deleted after keep_until: %v", e.reg.deleted)
	}
	if !e.aud.has(ActionDelete) {
		t.Fatal("no audit entry for the delete (R5.51)")
	}
	if _, ok := e.recFor(registry.RuleSuperseded, "old"); ok {
		t.Fatal("recommendation stays after it was acted on")
	}
}

// R5.43: variants go 7 days after the branch is gone, or 14 days unused.
func TestVariantRules(t *testing.T) {
	st := registry.Settings{VariantGoneDays: 7, VariantUnused: 14}
	gone := registry.Image{State: registry.StateVariant, BranchGoneAt: day(-8), LastPullAt: day(-1)}
	if due := variantDue(gone, st); !due.Equal(day(-1)) {
		t.Fatalf("merged 8 days ago: due %v", due)
	}
	unused := registry.Image{State: registry.StateVariant, LastPullAt: day(-15)}
	if due := variantDue(unused, st); !due.Equal(day(-1)) {
		t.Fatalf("unused 15 days: due %v", due)
	}
	fresh := registry.Image{State: registry.StateVariant, LastPullAt: day(-3)}
	if due := variantDue(fresh, st); !due.Equal(day(11)) {
		t.Fatalf("fresh variant due %v", due)
	}
	neverPulled := registry.Image{State: registry.StateVariant, PromotedAt: day(-20)}
	if due := variantDue(neverPulled, st); !due.Equal(day(-6)) {
		t.Fatalf("never pulled: due %v", due)
	}

	e := newEnv(t)
	e.repo.add(registry.Image{ID: "v_gone", State: registry.StateVariant, Kind: registry.KindCustom, BranchGoneAt: day(-8), LastPullAt: day(-1)})
	e.repo.add(registry.Image{ID: "v_fresh", State: registry.StateVariant, Kind: registry.KindCustom, LastPullAt: day(-3)})
	must(t, e.svc.Evaluate(context.Background(), t0))
	if e.repo.images["v_gone"].State != registry.StateDeleted {
		t.Fatal("a variant 8 days after its branch merged stays")
	}
	if e.repo.images["v_fresh"].State == registry.StateDeleted {
		t.Fatal("a fresh variant was deleted")
	}
	if r, ok := e.recFor(registry.RuleVariant, "v_fresh"); !ok || !r.DueAt.Equal(day(11)) {
		t.Fatalf("the fresh variant shows no date ahead: %+v", r)
	}
}

// R5.43: crsp never acts alone on replace, unused, over_budget, profile_removed.
func TestOwnerRulesNeverAct(t *testing.T) {
	e := newEnv(t)
	e.repo.add(registry.Image{ID: "idle", State: registry.StateCurrent, PromotedAt: day(-90), LastPullAt: day(-45), UniqueBytes: 5})
	e.repo.add(registry.Image{ID: "gone_profile", State: registry.StateCurrent, PromotedAt: day(-1), LastPullAt: day(-1)})
	e.svc.o.Profiles = profilesMissing{"gone"}
	e.repo.images["gone_profile"] = func() registry.Image { i := e.repo.images["gone_profile"]; i.Profile = "gone"; return i }()
	e.repo.use[registry.KindStandard] = KindUse{UsedBytes: registry.DefaultStandardBudget + 10}
	must(t, e.svc.Evaluate(context.Background(), t0))
	for _, c := range []struct {
		rule  registry.Rule
		image string
	}{{registry.RuleUnused, "idle"}, {registry.RuleProfileRemoved, "gone_profile"}, {registry.RuleOverBudget, ""}} {
		r, ok := e.recFor(c.rule, c.image)
		if !ok || r.Decider != registry.DecidedByOwner || !r.DueAt.IsZero() {
			t.Fatalf("%s: want an owner recommendation with no date, got %+v ok=%v", c.rule, r, ok)
		}
	}
	if len(e.reg.deleted) != 0 {
		t.Fatalf("crsp deleted on an owner rule: %v", e.reg.deleted)
	}
	// Even a forced call to act on an owner rule is not reachable: AloneRules is the gate.
	for _, r := range []registry.Rule{registry.RuleReplace, registry.RuleUnused, registry.RuleOverBudget, registry.RuleProfileRemoved} {
		if registry.AloneRules[r] {
			t.Fatalf("%s is an alone rule", r)
		}
	}
}

type profilesMissing struct{ missing string }

func (p profilesMissing) Exists(_ context.Context, id string) (bool, error) {
	return id != p.missing, nil
}

// R5.47: keep wins over delete, an image with a pointer, a running job, keep_until or a live token is not deleted alone.
func TestKeepWinsOverAutomaticDelete(t *testing.T) {
	e := newEnv(t)
	e.repo.add(registry.Image{ID: "running", State: registry.StateSuperseded, KeepUntil: day(-1)})
	e.repo.running["running"] = []string{"o/app ci.yml test"}
	e.repo.add(registry.Image{ID: "leased", Name: "crsp/std/leased", State: registry.StateSuperseded, KeepUntil: day(-1)})
	e.repo.leases["crsp/std/leased"] = 1
	e.repo.add(registry.Image{ID: "pointed", State: registry.StateVariant, BranchGoneAt: day(-30)})
	e.repo.pointers["pointed"] = []registry.Pointer{{Repository: "o/app", Profile: "node", Arch: "amd64", ImageID: "pointed"}}
	e.repo.add(registry.Image{ID: "rollback", State: registry.StateSuperseded, KeepUntil: day(3)})
	must(t, e.svc.Evaluate(context.Background(), t0))
	if len(e.reg.deleted) != 0 {
		t.Fatalf("deleted a kept image: %v", e.reg.deleted)
	}
	if _, ok := e.recFor(registry.RuleSuperseded, "running"); !ok {
		t.Fatal("the recommendation of an image a job uses must stay for the next pass")
	}
	// The job ends: the next pass acts.
	e.repo.running["running"] = nil
	must(t, e.svc.Evaluate(context.Background(), t0))
	if e.repo.images["running"].State != registry.StateDeleted {
		t.Fatal("not deleted after the job ended")
	}
}

// R5.45: a recommendation goes away only when its condition ends; nothing dismisses it.
func TestRecommendationClosesWhenConditionEnds(t *testing.T) {
	e := newEnv(t)
	e.repo.add(registry.Image{ID: "idle", State: registry.StateCurrent, PromotedAt: day(-90), LastPullAt: day(-45)})
	must(t, e.svc.Evaluate(context.Background(), t0))
	r, ok := e.recFor(registry.RuleUnused, "idle")
	if !ok {
		t.Fatal("no unused recommendation")
	}
	// Evaluating again keeps the same row (not dismissed, not duplicated).
	must(t, e.svc.Evaluate(context.Background(), t0))
	if len(e.repo.recs) != 1 {
		t.Fatalf("duplicated: %d", len(e.repo.recs))
	}
	// A pull ends the condition.
	i := e.repo.images["idle"]
	i.LastPullAt = t0
	e.repo.images["idle"] = i
	must(t, e.svc.Evaluate(context.Background(), t0))
	if _, ok := e.recFor(registry.RuleUnused, "idle"); ok {
		t.Fatal("recommendation stays after its condition ended")
	}
	if e.repo.closed[r.ID] != WhyGone {
		t.Fatalf("closed as %q, want gone", e.repo.closed[r.ID])
	}
}

// R5.43: unreferenced layers show as dangling, crsp's, due at the next weekly cleanup.
func TestDanglingRecommendation(t *testing.T) {
	e := newEnv(t)
	e.repo.layers = []registry.Layer{{Digest: "sha256:a", SizeBytes: 100}, {Digest: "sha256:b", SizeBytes: 50}}
	must(t, e.svc.Evaluate(context.Background(), t0))
	r, ok := e.recFor(registry.RuleDangling, "")
	if !ok || r.FreesBytes != 150 || r.Decider != registry.DecidedByCrsp {
		t.Fatalf("got %+v ok=%v", r, ok)
	}
	want := time.Date(2026, 11, 22, 3, 0, 0, 0, time.UTC) // Sunday 03:00 after Thu 19 Nov
	if !r.DueAt.Equal(want) {
		t.Fatalf("due %v, want %v", r.DueAt, want)
	}
}

// R5.49: an over-budget kind names unused, then the oldest superseded, then variants, then least pulled.
func TestOverBudgetDeleteOrder(t *testing.T) {
	e := newEnv(t)
	e.repo.add(registry.Image{ID: "a_current", State: registry.StateCurrent, PromotedAt: day(-5), LastPullAt: day(-2)})
	e.repo.add(registry.Image{ID: "b_current_old", State: registry.StateCurrent, PromotedAt: day(-9), LastPullAt: day(-4)})
	e.repo.add(registry.Image{ID: "c_variant", State: registry.StateVariant, PromotedAt: day(-3), LastPullAt: day(-1)})
	e.repo.add(registry.Image{ID: "d_super_new", State: registry.StateSuperseded, PromotedAt: day(-6), KeepUntil: day(1)})
	e.repo.add(registry.Image{ID: "e_super_old", State: registry.StateSuperseded, PromotedAt: day(-20), KeepUntil: day(1)})
	e.repo.add(registry.Image{ID: "f_unused", State: registry.StateCurrent, PromotedAt: day(-60), LastPullAt: day(-50)})
	e.repo.add(registry.Image{ID: "g_pointed", State: registry.StateCurrent, PromotedAt: day(-90), LastPullAt: day(-80)})
	e.repo.pointers["g_pointed"] = []registry.Pointer{{Repository: "o/r", ImageID: "g_pointed"}}
	e.repo.add(registry.Image{ID: "h_custom", Kind: registry.KindCustom, State: registry.StateCurrent, PromotedAt: day(-90)})
	got, err := e.svc.DeleteFirst(context.Background(), registry.KindStandard, t0)
	must(t, err)
	var ids []string
	for _, i := range got {
		ids = append(ids, i.ID)
	}
	want := []string{"f_unused", "e_super_old", "d_super_new", "c_variant", "b_current_old", "a_current"}
	if len(ids) != len(want) {
		t.Fatalf("got %v want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("got %v want %v", ids, want)
		}
	}
	e.repo.use[registry.KindStandard] = KindUse{UsedBytes: registry.DefaultStandardBudget + 7}
	must(t, e.svc.Evaluate(context.Background(), t0))
	r, ok := e.recFor(registry.RuleOverBudget, "")
	if !ok || r.Kind != registry.KindStandard || r.OtherID != "f_unused" || r.FreesBytes != 7 {
		t.Fatalf("over_budget: %+v ok=%v", r, ok)
	}
}

// R5.50: a disk-full push refusal opens an over_budget recommendation and it stays while the disk is low.
func TestPushRefusedOpensOverBudget(t *testing.T) {
	e := newEnv(t)
	e.repo.add(registry.Image{ID: "idle", Kind: registry.KindCustom, Name: "crsp/custom/o-r/node", State: registry.StateCurrent, PromotedAt: day(-90), LastPullAt: day(-45)})
	must(t, e.svc.PushRefused(context.Background(), registry.KindCustom))
	r, ok := e.recFor(registry.RuleOverBudget, "")
	if !ok || r.Kind != registry.KindCustom || r.Decider != registry.DecidedByOwner || r.OtherID != "idle" {
		t.Fatalf("got %+v ok=%v", r, ok)
	}
	// Plenty of disk and under budget: the next pass closes it.
	must(t, e.svc.Evaluate(context.Background(), t0))
	if _, ok := e.recFor(registry.RuleOverBudget, ""); ok {
		t.Fatal("recommendation stays with a free disk and a budget that holds")
	}
	// A nearly full disk keeps it open on every pass.
	e.svc.o.Disk = fakeDisk{free: 1_000_000}
	must(t, e.svc.Evaluate(context.Background(), t0))
	if _, ok := e.recFor(registry.RuleOverBudget, ""); !ok {
		t.Fatal("no over_budget recommendation on a nearly full disk")
	}
	if err := e.svc.PushRefused(context.Background(), "odd"); err == nil {
		t.Fatal("unknown kind accepted")
	}
}

func TestNextCleanupIsSunday3am(t *testing.T) {
	st := registry.Settings{CleanupWeekday: time.Sunday, CleanupHour: 3}
	got := NextCleanup(t0, st)
	if want := time.Date(2026, 11, 22, 3, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	sun := time.Date(2026, 11, 22, 3, 0, 0, 0, time.UTC)
	if got := NextCleanup(sun, st); !got.Equal(sun.AddDate(0, 0, 7)) {
		t.Fatalf("at the time itself the next one is a week on, got %v", got)
	}
}
