package records

import (
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// REG14: the figures the screens used to work out from several calls.
func TestReadModelsCarryWhatScreensNeeded(t *testing.T) {
	e := newEnv(t)
	e.svc.cfg.Machines = dir{}
	e.image(seed{id: "img_old", kind: "custom", key: "111111111111", repo: "o/r", state: "superseded", promoted: t0.Add(-72 * time.Hour)})
	e.image(seed{id: "img_new", kind: "custom", key: "222222222222", repo: "o/r", promoted: t0.Add(-24 * time.Hour)})
	e.image(seed{id: "img_br", kind: "custom", key: "333333333333", repo: "o/r", state: "variant", branch: "fix", promoted: t0.Add(-time.Hour)})
	e.image(seed{id: "img_idle", kind: "standard", key: "444444444444", promoted: t0.Add(-40 * 24 * time.Hour)})
	e.layers("img_new", layer("1", 800), layer("3", 100))
	e.layers("img_old", layer("1", 800), layer("2", 100))
	if err := e.q.SetRegistryImageLineage(e.ctx, sqlcgen.SetRegistryImageLineageParams{ReplacesID: "img_old", ID: "img_new"}); err != nil {
		t.Fatal(err)
	}
	// m1 (a runner uses it, see dir) pulled img_new.
	p := pull("p1", "done", registry.SourcePulled, t0)
	p.MachineID, p.Key = "m1", "222222222222"
	if err := e.svc.RecordPull(e.ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := e.q.UpsertRegistryRecommendation(e.ctx, sqlcgen.UpsertRegistryRecommendationParams{ID: "r_unused", Rule: "unused", ImageID: "img_idle", Decider: "owner"}); err != nil {
		t.Fatal(err)
	}
	if err := e.q.UpsertRegistryRecommendation(e.ctx, sqlcgen.UpsertRegistryRecommendationParams{ID: "r_budget", Rule: "over_budget", Kind: "standard", Decider: "owner"}); err != nil {
		t.Fatal(err)
	}

	// The list: the idle image says how many days it was idle; the others say nothing.
	list, err := e.svc.Images(e.ctx, ImageFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range list {
		switch {
		case i.ID == "img_idle" && (i.UnusedDays == nil || *i.UnusedDays != 40):
			t.Errorf("idle image: %v", i.UnusedDays)
		case i.ID != "img_idle" && i.UnusedDays != nil:
			t.Errorf("%s must not say unused: %d", i.ID, *i.UnusedDays)
		}
	}
	// A pull makes it used: the days count from the last pull.
	if err := e.q.SetRegistryImageLastPull(e.ctx, sqlcgen.SetRegistryImageLastPullParams{LastPullAt: stamp(t0.Add(-10 * 24 * time.Hour)), ID: "img_idle", LastPullAt_2: stamp(t0.Add(-10 * 24 * time.Hour))}); err != nil {
		t.Fatal(err)
	}
	list, _ = e.svc.Images(e.ctx, ImageFilter{Profile: "base", Kind: registry.KindStandard})
	if len(list) != 1 || list[0].UnusedDays == nil || *list[0].UnusedDays != 10 {
		t.Errorf("days since the last pull: %+v", list)
	}

	// The overview names the image of each recommendation that has one.
	ov, err := e.svc.Overview(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := ov.RecommendedImages["img_idle"]; !ok || got.Key != "444444444444" || got.UnusedDays == nil || len(ov.RecommendedImages) != 1 {
		t.Errorf("recommended images: %+v", ov.RecommendedImages)
	}

	// The image page: in use, shared bytes, relation, variant days.
	d, err := e.svc.ImageDetail(e.ctx, "img_new")
	if err != nil {
		t.Fatal(err)
	}
	if !d.InUse || d.SharedBytes != 800 || d.VariantGoneDays != 0 {
		t.Errorf("new: in use %v shared %d variant days %d", d.InUse, d.SharedBytes, d.VariantGoneDays)
	}
	rel := map[string]string{}
	for _, l := range d.Lineage {
		rel[l.ImageID] = l.Relation
	}
	if rel["img_old"] != "replaces" || rel["img_br"] != "same_profile" || rel["img_new"] != "same_profile" {
		t.Errorf("relations: %v", rel)
	}
	d, _ = e.svc.ImageDetail(e.ctx, "img_br")
	if d.VariantGoneDays != registry.DefaultVariantGoneDays || d.InUse {
		t.Errorf("variant: days %d in use %v", d.VariantGoneDays, d.InUse)
	}
	d, _ = e.svc.ImageDetail(e.ctx, "img_old")
	if d.InUse {
		t.Error("no machine pulled the old image")
	}
}
