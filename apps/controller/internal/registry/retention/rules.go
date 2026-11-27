package retention

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

func days(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

type recKey struct {
	rule  registry.Rule
	image string
	kind  registry.Kind
}

func keyOf(r registry.Recommendation) recKey { return recKey{r.Rule, r.ImageID, r.Kind} }

func newRecID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "rec_" + hex.EncodeToString(b[:])
}

// lastUse is when an image was last pulled, else when it was promoted.
func lastUse(img registry.Image) time.Time {
	switch {
	case !img.LastPullAt.IsZero():
		return img.LastPullAt
	case !img.PromotedAt.IsZero():
		return img.PromotedAt
	}
	return img.PushedAt
}

// variantDue is the day a branch variant goes: 7 days after its branch merged
// or was deleted, or 14 days unused, whichever is first (R5.43).
func variantDue(img registry.Image, st registry.Settings) time.Time {
	var due time.Time
	if !img.BranchGoneAt.IsZero() {
		due = img.BranchGoneAt.Add(days(st.VariantGoneDays))
	}
	if ref := lastUse(img); !ref.IsZero() {
		if u := ref.Add(days(st.VariantUnused)); due.IsZero() || u.Before(due) {
			due = u
		}
	}
	return due
}

// nextCleanup is the next weekly cleanup after now, in now's zone.
func nextCleanup(now time.Time, st registry.Settings) time.Time {
	t := prevCleanup(now, st).AddDate(0, 0, 7)
	for !t.After(now) {
		t = t.AddDate(0, 0, 7)
	}
	return t
}

// prevCleanup is the latest scheduled cleanup instant not after now.
func prevCleanup(now time.Time, st registry.Settings) time.Time {
	d := time.Date(now.Year(), now.Month(), now.Day(), st.CleanupHour, 0, 0, 0, now.Location())
	d = d.AddDate(0, 0, -int((now.Weekday()-st.CleanupWeekday+7)%7))
	for d.After(now) {
		d = d.AddDate(0, 0, -7)
	}
	return d
}

// NextCleanup is when the next weekly cleanup runs.
func NextCleanup(now time.Time, st registry.Settings) time.Time { return nextCleanup(now, st) }

// Evaluate refreshes every recommendation as of now, closes those whose
// condition ended and acts on the due ones of registry.AloneRules (R5.43). A
// recommendation is never dismissed (R5.45). An image with a pointer, one a
// runner uses, or one inside keep_until is never deleted here (R5.47).
func (s *Service) Evaluate(ctx context.Context, now time.Time) error {
	st, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	images, err := s.o.Repo.Images(ctx)
	if err != nil {
		return err
	}
	pointers := map[string][]registry.Pointer{}
	for _, img := range images {
		p, err := s.o.Repo.PointersTo(ctx, img.ID)
		if err != nil {
			return err
		}
		pointers[img.ID] = p
	}

	want := map[recKey]registry.Recommendation{}
	add := func(r registry.Recommendation) {
		if registry.AloneRules[r.Rule] {
			r.Decider = registry.DecidedByCrsp
		} else {
			r.Decider = registry.DecidedByOwner
			r.DueAt = time.Time{}
		}
		want[keyOf(r)] = r
	}

	for _, img := range images {
		pointed := len(pointers[img.ID]) > 0
		switch img.State {
		case registry.StateSuperseded:
			if !pointed && !img.KeepUntil.IsZero() {
				add(registry.Recommendation{Rule: registry.RuleSuperseded, ImageID: img.ID, Kind: "", FreesBytes: img.UniqueBytes, DueAt: img.KeepUntil})
			}
		case registry.StateVariant:
			if due := variantDue(img, st); !pointed && !due.IsZero() {
				add(registry.Recommendation{Rule: registry.RuleVariant, ImageID: img.ID, FreesBytes: img.UniqueBytes, DueAt: due})
			}
		case registry.StateCurrent:
			if !pointed && !lastUse(img).IsZero() && lastUse(img).Before(now.Add(-days(st.UnusedDays))) {
				add(registry.Recommendation{Rule: registry.RuleUnused, ImageID: img.ID, FreesBytes: img.UniqueBytes})
			}
			if s.o.Profiles != nil && img.Kind == registry.KindStandard {
				ok, err := s.o.Profiles.Exists(ctx, img.Profile)
				if err != nil {
					return err
				}
				if !ok {
					add(registry.Recommendation{Rule: registry.RuleProfileRemoved, ImageID: img.ID, FreesBytes: img.UniqueBytes})
				}
			}
		}
	}

	report, err := s.BudgetReport(ctx)
	if err != nil {
		return err
	}
	diskLow := report.FreeDiskBytes < s.o.DiskFloor
	for _, b := range []registry.Budget{report.Standard, report.Custom} {
		if b.UsedBytes > b.LimitBytes || diskLow {
			r := s.overBudget(b.Kind, b.UsedBytes-b.LimitBytes, images, pointers, st, now)
			add(r)
		}
	}

	dangling, err := s.o.Repo.DanglingLayers(ctx)
	if err != nil {
		return err
	}
	if len(dangling) > 0 {
		var sum int64
		for _, l := range dangling {
			sum += l.SizeBytes
		}
		add(registry.Recommendation{Rule: registry.RuleDangling, FreesBytes: sum, DueAt: nextCleanup(now, st)})
	}

	open, err := s.o.Repo.Recommendations(ctx)
	if err != nil {
		return err
	}
	ids := map[recKey]string{}
	for _, r := range open {
		ids[keyOf(r)] = r.ID
	}
	order := make([]recKey, 0, len(want))
	for k := range want {
		order = append(order, k)
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if a.rule != b.rule {
			return a.rule < b.rule
		}
		if a.image != b.image {
			return a.image < b.image
		}
		return a.kind < b.kind
	})
	for _, k := range order {
		r := want[k]
		r.ID = ids[k]
		if r.ID == "" {
			r.ID = newRecID()
		}
		if err := s.o.Repo.Upsert(ctx, r); err != nil {
			return err
		}
	}
	for _, r := range open {
		if _, ok := want[keyOf(r)]; !ok {
			if err := s.o.Repo.Close(ctx, r.ID, WhyGone); err != nil {
				return err
			}
		}
	}

	byID := map[string]registry.Image{}
	for _, img := range images {
		byID[img.ID] = img
	}
	for _, k := range order {
		r := want[k]
		if !registry.AloneRules[r.Rule] || r.ImageID == "" || r.DueAt.IsZero() || r.DueAt.After(now) {
			continue
		}
		if err := s.actAlone(ctx, byID[r.ImageID]); err != nil {
			s.o.Logger.Warn("registry retention: could not remove an image", "image", r.ImageID, "rule", string(r.Rule), "err", err)
		}
	}
	return nil
}

// actAlone deletes an image crsp may delete on its own. It skips, and keeps the
// recommendation, when anything uses the image.
func (s *Service) actAlone(ctx context.Context, img registry.Image) error {
	u, err := s.inUse(ctx, img)
	if err != nil {
		return err
	}
	if !u.Empty() {
		return nil
	}
	return s.removeImage(ctx, img, CrspActor, u)
}

// overBudget builds the recommendation of a kind over its budget; OtherID names
// what to delete first.
func (s *Service) overBudget(kind registry.Kind, over int64, images []registry.Image, pointers map[string][]registry.Pointer, st registry.Settings, now time.Time) registry.Recommendation {
	r := registry.Recommendation{Rule: registry.RuleOverBudget, Kind: kind}
	if over > 0 {
		r.FreesBytes = over
	}
	if order := deleteOrder(kind, images, pointers, st, now); len(order) > 0 {
		r.OtherID = order[0].ID
	}
	return r
}

// DeleteFirst names what to delete first in a kind that is over its budget:
// unused images, then the oldest superseded, then variants, then the least
// pulled (R5.49). Images with a pointer are never named.
func (s *Service) DeleteFirst(ctx context.Context, kind registry.Kind, now time.Time) ([]registry.Image, error) {
	st, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	images, err := s.o.Repo.Images(ctx)
	if err != nil {
		return nil, err
	}
	pointers := map[string][]registry.Pointer{}
	for _, img := range images {
		p, err := s.o.Repo.PointersTo(ctx, img.ID)
		if err != nil {
			return nil, err
		}
		pointers[img.ID] = p
	}
	return deleteOrder(kind, images, pointers, st, now), nil
}

func deleteOrder(kind registry.Kind, images []registry.Image, pointers map[string][]registry.Pointer, st registry.Settings, now time.Time) []registry.Image {
	var unused, superseded, variants, rest []registry.Image
	for _, img := range images {
		if img.Kind != kind || len(pointers[img.ID]) > 0 {
			continue
		}
		switch img.State {
		case registry.StateSuperseded:
			superseded = append(superseded, img)
		case registry.StateVariant:
			variants = append(variants, img)
		case registry.StateCurrent:
			if lastUse(img).Before(now.Add(-days(st.UnusedDays))) {
				unused = append(unused, img)
			} else {
				rest = append(rest, img)
			}
		}
	}
	oldestUse := func(l []registry.Image) {
		sort.SliceStable(l, func(i, j int) bool { return lastUse(l[i]).Before(lastUse(l[j])) })
	}
	oldestPromoted := func(l []registry.Image) {
		sort.SliceStable(l, func(i, j int) bool { return l[i].PromotedAt.Before(l[j].PromotedAt) })
	}
	oldestUse(unused)
	oldestPromoted(superseded)
	oldestUse(variants)
	oldestUse(rest)
	out := append(append(append(unused, superseded...), variants...), rest...)
	return out
}

// PushRefused opens an over_budget recommendation for the kind of a push the
// registry refused because the disk or the budget was full (R5.50).
func (s *Service) PushRefused(ctx context.Context, kind registry.Kind) error {
	if kind != registry.KindStandard && kind != registry.KindCustom {
		return fmt.Errorf("retention: unknown kind %q", kind)
	}
	now := s.o.Now()
	st, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	images, err := s.o.Repo.Images(ctx)
	if err != nil {
		return err
	}
	pointers := map[string][]registry.Pointer{}
	for _, img := range images {
		if pointers[img.ID], err = s.o.Repo.PointersTo(ctx, img.ID); err != nil {
			return err
		}
	}
	open, err := s.o.Repo.Recommendations(ctx)
	if err != nil {
		return err
	}
	r := s.overBudget(kind, 0, images, pointers, st, now)
	r.Decider = registry.DecidedByOwner
	r.ID = newRecID()
	for _, o := range open {
		if keyOf(o) == keyOf(r) {
			r.ID = o.ID
		}
	}
	return s.o.Repo.Upsert(ctx, r)
}

// Recommendations lists the open recommendations, owner's first.
func (s *Service) Recommendations(ctx context.Context) ([]registry.Recommendation, error) {
	return s.o.Repo.Recommendations(ctx)
}
