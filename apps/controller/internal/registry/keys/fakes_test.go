package keys

import (
	"context"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

type fakeProfiles struct{ ref registry.ProfileRef }

func (f fakeProfiles) ProfileFor(context.Context, string, []string) (registry.ProfileRef, error) {
	return f.ref, nil
}

// fakeStacks gives the files of a commit: IMG2's StackSource.
type fakeStacks struct {
	byCommit map[string][]registry.StackFile
}

func (f fakeStacks) StackFiles(_ context.Context, _, commit string) ([]registry.StackFile, error) {
	return f.byCommit[commit], nil
}

// fakeWorld is one in-memory store that is every interface the rules call:
// REG4's ImageStore and REG6's RecommendationStore (other tasks, so fakes),
// and the LineageStore.
type fakeWorld struct {
	images   map[string]registry.Image
	pointers map[[3]string]registry.Pointer
	recs     map[string]registry.Recommendation
	moves    int
}

func newWorld(imgs ...registry.Image) *fakeWorld {
	w := &fakeWorld{images: map[string]registry.Image{}, pointers: map[[3]string]registry.Pointer{}, recs: map[string]registry.Recommendation{}}
	for _, i := range imgs {
		w.images[i.ID] = i
	}
	return w
}

func (w *fakeWorld) Image(_ context.Context, id string) (registry.Image, error) {
	i, ok := w.images[id]
	if !ok {
		return registry.Image{}, registry.ErrNotFound
	}
	return i, nil
}
func (w *fakeWorld) ImageByKey(context.Context, registry.Key) (registry.Image, error) {
	return registry.Image{}, registry.ErrNotFound
}
func (w *fakeWorld) Images(_ context.Context, f registry.ImageFilter) ([]registry.Image, error) {
	var out []registry.Image
	for _, i := range w.images {
		if f.Kind != "" && i.Kind != f.Kind {
			continue
		}
		if len(f.States) > 0 {
			ok := false
			for _, s := range f.States {
				ok = ok || s == i.State
			}
			if !ok {
				continue
			}
		}
		if f.Repository != "" && i.SourceRepository != f.Repository {
			continue
		}
		out = append(out, i)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out, nil
}
func (w *fakeWorld) PutImage(_ context.Context, i registry.Image) error {
	w.images[i.ID] = i
	return nil
}
func (w *fakeWorld) SetState(_ context.Context, id string, s registry.State, k time.Time) error {
	i := w.images[id]
	i.State, i.KeepUntil = s, k
	w.images[id] = i
	return nil
}
func (w *fakeWorld) Layers(context.Context, string) ([]registry.Layer, error)  { return nil, nil }
func (w *fakeWorld) PutLayers(context.Context, string, []registry.Layer) error { return nil }

func (w *fakeWorld) Pointer(_ context.Context, r, p, a string) (registry.Pointer, error) {
	ptr, ok := w.pointers[[3]string{r, p, a}]
	if !ok {
		return registry.Pointer{}, registry.ErrNotFound
	}
	return ptr, nil
}
func (w *fakeWorld) PointersTo(_ context.Context, id string) ([]registry.Pointer, error) {
	var out []registry.Pointer
	for _, p := range w.pointers {
		if p.ImageID == id {
			out = append(out, p)
		}
	}
	return out, nil
}
func (w *fakeWorld) PointersOfProfile(_ context.Context, profile, arch string) ([]registry.Pointer, error) {
	var out []registry.Pointer
	for _, p := range w.pointers {
		if p.Profile == profile && p.Arch == arch {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Repository < out[b].Repository })
	return out, nil
}
func (w *fakeWorld) MovePointer(_ context.Context, p registry.Pointer) error {
	k := [3]string{p.Repository, p.Profile, p.Arch}
	p.Previous = w.pointers[k].ImageID
	w.pointers[k] = p
	w.moves++
	return nil
}
func (w *fakeWorld) SetReplaces(_ context.Context, n, o string) error {
	i := w.images[n]
	i.Replaces = o
	w.images[n] = i
	return nil
}
func (w *fakeWorld) MarkBranchGone(_ context.Context, repo, branch string, at time.Time) (int64, error) {
	var n int64
	for id, i := range w.images {
		if i.SourceRepository == repo && i.Branch == branch && i.BranchGoneAt.IsZero() {
			i.BranchGoneAt = at
			w.images[id] = i
			n++
		}
	}
	return n, nil
}

func (w *fakeWorld) Recommendations(context.Context) ([]registry.Recommendation, error) {
	var out []registry.Recommendation
	for _, r := range w.recs {
		out = append(out, r)
	}
	return out, nil
}
func (w *fakeWorld) Upsert(_ context.Context, r registry.Recommendation) error {
	w.recs[r.ID] = r
	return nil
}
func (w *fakeWorld) Close(_ context.Context, id, _ string) error { delete(w.recs, id); return nil }

func (w *fakeWorld) detector() *Detector {
	now := func() time.Time { return time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC) }
	return &Detector{Pointers: &Pointers{Lineage: w, Images: w, Now: now}, Images: w, Recs: w, Now: now}
}

func (w *fakeWorld) point(repo string, img registry.Image) {
	w.pointers[[3]string{repo, img.Profile, img.Arch}] = registry.Pointer{Repository: repo, Profile: img.Profile, Arch: img.Arch, ImageID: img.ID, MovedBy: "luis"}
}
