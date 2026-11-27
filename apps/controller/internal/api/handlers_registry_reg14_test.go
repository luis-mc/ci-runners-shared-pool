package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/records"
)

// REG14: the fields the screens used to work out themselves.

func TestRegistryReadNewFields(t *testing.T) {
	days := 31
	unused := regImage("img_u", "uuuuuuuuuuuu")
	unused.UnusedDays = &days
	d := records.ImageDetail{ImageSummary: regImage("img_1", "111111111111"), InUse: true, SharedBytes: 800, VariantGoneDays: 7}
	d.State = registry.StateVariant
	d.Replaces, d.ReplacedBy = "img_0", "img_2"
	d.Lineage = []records.LineageEntry{
		{ImageID: "img_0", Key: "000000000000", State: registry.StateSuperseded, PushedAt: regT0, Relation: "replaces"},
		{ImageID: "img_2", Key: "222222222222", State: registry.StateCurrent, PushedAt: regT0, Relation: "replaced_by"},
		{ImageID: "img_3", Key: "333333333333", State: registry.StateCurrent, PushedAt: regT0, Relation: "same_profile"}}
	f := &fakeRegistryRead{
		images: []records.ImageSummary{unused, regImage("img_1", "111111111111")},
		detail: map[string]records.ImageDetail{"img_1": d},
		overview: records.Overview{
			Recommendations: []registry.Recommendation{
				{ID: "r1", Rule: registry.RuleUnused, ImageID: "img_u", Decider: registry.DecidedByOwner, CreatedAt: regT0},
				{ID: "r2", Rule: registry.RuleOverBudget, Kind: registry.KindStandard, Decider: registry.DecidedByOwner, CreatedAt: regT0}},
			RecommendedImages: map[string]records.ImageSummary{"img_u": unused},
		},
	}
	mux := registryReadMux(f)

	var list gen.RegistryImageList
	if err := json.Unmarshal(do(mux, http.MethodGet, "/api/v1/registry/images", "").Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Items[0].UnusedDays == nil || *list.Items[0].UnusedDays != 31 || list.Items[1].UnusedDays != nil {
		t.Errorf("unused_days: %+v", list.Items)
	}

	var im gen.RegistryImage
	if err := json.Unmarshal(do(mux, http.MethodGet, "/api/v1/registry/images/img_1", "").Body.Bytes(), &im); err != nil {
		t.Fatal(err)
	}
	if im.InUse == nil || !*im.InUse || im.SharedBytes == nil || *im.SharedBytes != 800 || im.VariantGoneDays == nil || *im.VariantGoneDays != 7 {
		t.Errorf("detail: in_use %v shared %v variant %v", im.InUse, im.SharedBytes, im.VariantGoneDays)
	}
	want := []gen.RegistryLineageEntryRelation{"replaces", "replaced_by", "same_profile"}
	for i, l := range im.Lineage {
		if l.Relation == nil || *l.Relation != want[i] {
			t.Errorf("lineage %d: %+v", i, l)
		}
	}

	var ov gen.RegistryOverview
	if err := json.Unmarshal(do(mux, http.MethodGet, "/api/v1/registry/overview", "").Body.Bytes(), &ov); err != nil {
		t.Fatal(err)
	}
	r := ov.Recommendations
	if r[0].Image == nil || r[0].Image.Id != "img_u" || r[0].Image.UnusedDays == nil || *r[0].Image.UnusedDays != 31 || r[1].Image != nil {
		t.Errorf("recommendation image: %+v", r)
	}
	// A variant-less image does not carry the variant days.
	cur := records.ImageDetail{ImageSummary: regImage("img_c", "cccccccccccc")}
	f.detail["img_c"] = cur
	if body := do(mux, http.MethodGet, "/api/v1/registry/images/img_c", "").Body.String(); strings.Contains(body, "variant_gone_days") {
		t.Errorf("variant days on a current image: %s", body)
	}
}

func TestReplaceStepsFollowThePlan(t *testing.T) {
	plan := func(state registry.PlanState, done int) registry.ReplacePlan {
		return registry.ReplacePlan{State: state, StepsDone: done, StepsTotal: 4,
			Repositories: []string{"a/one", "a/two"}, Machines: []string{"m1", "m2"}}
	}
	states := func(p registry.ReplacePlan) string {
		var out []string
		for _, s := range replaceSteps(p, p.Machines) {
			out = append(out, string(s.Kind)+":"+string(s.State))
		}
		return strings.Join(out, " ")
	}
	for _, c := range []struct {
		name string
		p    registry.ReplacePlan
		want string
	}{
		{"draft", plan(registry.PlanDraft, 0), "point:waiting point:waiting warm:waiting supersede:waiting"},
		{"first pointer", plan(registry.PlanRunning, 0), "point:running point:waiting warm:waiting supersede:waiting"},
		{"second pointer", plan(registry.PlanRunning, 1), "point:done point:running warm:waiting supersede:waiting"},
		{"warming", plan(registry.PlanRunning, 2), "point:done point:done warm:running supersede:waiting"},
		{"one machine asked", plan(registry.PlanRunning, 3), "point:done point:done warm:running supersede:waiting"},
		{"marking", plan(registry.PlanRunning, 4), "point:done point:done warm:done supersede:running"},
		{"stopped", plan(registry.PlanStopped, 1), "point:done point:waiting warm:waiting supersede:waiting"},
		{"failed on the second pointer", plan(registry.PlanFailed, 1), "point:done point:failed warm:waiting supersede:waiting"},
		{"done", plan(registry.PlanDone, 4), "point:done point:done warm:done supersede:done"},
		{"rolled back", plan(registry.PlanRolledBack, 4), "point:done point:done warm:done supersede:done"},
	} {
		if got := states(c.p); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	// No machine to warm: the step stays in the list and is done once the pointers are.
	p := registry.ReplacePlan{State: registry.PlanRunning, StepsDone: 1, StepsTotal: 1, Repositories: []string{"a/one"}}
	if got := states(p); got != "point:done warm:done supersede:running" {
		t.Errorf("no machines: %s", got)
	}
}

func TestReplacePlanKeepDays(t *testing.T) {
	ctx := context.Background()
	p := registry.ReplacePlan{ID: "rpl_1", NewImageID: "n", OldImageID: "o", KeepDays: 3, State: registry.PlanDraft,
		Repositories: []string{"acme/app"}, StepsTotal: 1, CreatedAt: time.Now()}
	var got int
	svc := &fakeReplace{plan: p, keepGot: &got, keepDef: 9}
	s := replaceServer(svc)

	// Without keep_days the days of the settings are used.
	if _, err := s.CreateRegistryReplacePlan(ctx, gen.CreateRegistryReplacePlanRequestObject{Body: &gen.RegistryReplacePlanInput{NewImageId: "n"}}); err != nil || got != 9 {
		t.Errorf("default: %d %v", got, err)
	}
	two := 2
	if _, err := s.CreateRegistryReplacePlan(ctx, gen.CreateRegistryReplacePlanRequestObject{Body: &gen.RegistryReplacePlanInput{NewImageId: "n", KeepDays: &two}}); err != nil || got != 2 {
		t.Errorf("named: %d %v", got, err)
	}

	// A plan that has not ended says the date it would keep the old image until; steps are always there.
	r, _ := s.CreateRegistryReplacePlan(ctx, gen.CreateRegistryReplacePlanRequestObject{Body: &gen.RegistryReplacePlanInput{NewImageId: "n"}})
	body := gen.RegistryReplacePlan(r.(gen.CreateRegistryReplacePlan201JSONResponse))
	if body.KeepUntil == nil || body.KeepUntil.Before(time.Now().Add(71*time.Hour)) || body.KeepUntil.After(time.Now().Add(73*time.Hour)) {
		t.Errorf("keep_until of a draft: %v", body.KeepUntil)
	}
	if body.Steps == nil || len(*body.Steps) != 3 || (*body.Steps)[0].Kind != gen.RegistryReplaceStepKindPoint || (*body.Steps)[0].Targets[0] != "acme/app" {
		t.Errorf("steps: %+v", body.Steps)
	}
}
