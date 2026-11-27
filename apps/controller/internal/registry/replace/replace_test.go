package replace

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// fake is every store part the service needs, in memory.
type fake struct {
	mu     sync.Mutex
	images map[string]registry.Image
	layers map[string][]registry.Layer
	ptrs   map[string]registry.Pointer // repo|profile|arch
	plans  map[string]registry.ReplacePlan
	moves  []string
}

func newFake() *fake {
	return &fake{images: map[string]registry.Image{}, layers: map[string][]registry.Layer{}, ptrs: map[string]registry.Pointer{}, plans: map[string]registry.ReplacePlan{}}
}

func pk(repo, profile, arch string) string { return repo + "|" + profile + "|" + arch }

func (f *fake) Image(_ context.Context, id string) (registry.Image, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	img, ok := f.images[id]
	if !ok {
		return img, registry.ErrNotFound
	}
	return img, nil
}
func (f *fake) ImageByKey(context.Context, registry.Key) (registry.Image, error) {
	return registry.Image{}, registry.ErrNotFound
}
func (f *fake) Images(context.Context, registry.ImageFilter) ([]registry.Image, error) {
	return nil, nil
}
func (f *fake) PutImage(context.Context, registry.Image) error                    { return nil }
func (f *fake) SetState(context.Context, string, registry.State, time.Time) error { return nil }
func (f *fake) PutLayers(context.Context, string, []registry.Layer) error         { return nil }
func (f *fake) Layers(_ context.Context, id string) ([]registry.Layer, error) {
	return f.layers[id], nil
}
func (f *fake) Pointer(_ context.Context, repo, profile, arch string) (registry.Pointer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.ptrs[pk(repo, profile, arch)]
	if !ok {
		return p, registry.ErrNotFound
	}
	return p, nil
}
func (f *fake) PointersTo(_ context.Context, id string) ([]registry.Pointer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []registry.Pointer
	for _, p := range f.ptrs {
		if p.ImageID == id {
			out = append(out, p)
		}
	}
	return out, nil
}
func (f *fake) MovePointer(_ context.Context, p registry.Pointer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := pk(p.Repository, p.Profile, p.Arch)
	p.Previous = f.ptrs[k].ImageID
	f.ptrs[k] = p
	f.moves = append(f.moves, p.Repository+"->"+p.ImageID)
	return nil
}

func (f *fake) InsertReplacePlan(_ context.Context, p registry.ReplacePlan) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p.CreatedAt = time.Now()
	f.plans[p.ID] = p
	return nil
}
func (f *fake) GetReplacePlan(_ context.Context, id string) (registry.ReplacePlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.plans[id]
	if !ok {
		return p, registry.ErrNotFound
	}
	return p, nil
}
func (f *fake) SaveReplacePlan(_ context.Context, p registry.ReplacePlan) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur := f.plans[p.ID]
	cur.State, cur.StepsDone, cur.Error, cur.StartedAt, cur.FinishedAt, cur.KeepUntil = p.State, p.StepsDone, p.Error, p.StartedAt, p.FinishedAt, p.KeepUntil
	f.plans[p.ID] = cur
	return nil
}
func (f *fake) MarkReplacePlanRolledBack(_ context.Context, id, _ string, _ time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.plans[id]
	if p.State != registry.PlanDone {
		return false, nil
	}
	p.State = registry.PlanRolledBack
	f.plans[id] = p
	return true, nil
}
func (f *fake) ActiveReplacePlanForImage(_ context.Context, id string) (registry.ReplacePlan, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.plans {
		if (p.NewImageID == id || p.OldImageID == id) && (p.State == registry.PlanDraft || p.State == registry.PlanRunning) {
			return p, true, nil
		}
	}
	return registry.ReplacePlan{}, false, nil
}
func (f *fake) ReplacePlansByState(_ context.Context, st registry.PlanState) ([]registry.ReplacePlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []registry.ReplacePlan
	for _, p := range f.plans {
		if p.State == st {
			out = append(out, p)
		}
	}
	return out, nil
}
func (f *fake) SupersedeRegistryImage(_ context.Context, oldID, newID string, until time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	img := f.images[oldID]
	if img.State != registry.StateCurrent && img.State != registry.StateVariant {
		return false, nil
	}
	img.State, img.ReplacedBy, img.KeepUntil = registry.StateSuperseded, newID, until
	f.images[oldID] = img
	return true, nil
}
func (f *fake) RestoreRegistryImage(_ context.Context, id string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	img := f.images[id]
	if img.State != registry.StateSuperseded {
		return false, nil
	}
	img.State, img.ReplacedBy, img.KeepUntil = registry.StateCurrent, "", time.Time{}
	f.images[id] = img
	return true, nil
}

type warmer struct {
	mu    sync.Mutex
	asked []string
	gate  chan struct{} // when set, each Warm waits for a token
	enter chan string
}

func (w *warmer) Warm(_ context.Context, m string, _ registry.Image) error {
	if w.enter != nil {
		w.enter <- m
	}
	if w.gate != nil {
		<-w.gate
	}
	w.mu.Lock()
	w.asked = append(w.asked, m)
	w.mu.Unlock()
	return nil
}

type holders []string

func (h holders) MachinesHolding(context.Context, string) ([]string, error) { return h, nil }

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

const repoA, repoB = "acme/app", "acme/other"

// world: old image O and new image N for repoA; repoB also points at O.
func world(t *testing.T, w *warmer) (*Service, *fake, *clock) {
	t.Helper()
	f := newFake()
	base := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	f.images["old"] = registry.Image{ID: "old", Kind: registry.KindCustom, Profile: "node-web", Arch: "amd64", State: registry.StateCurrent,
		SourceRepository: repoA, BaseDigest: "sha256:aa", Commit: "c1", SizeBytes: 300}
	f.images["new"] = registry.Image{ID: "new", Kind: registry.KindCustom, Profile: "node-web", Arch: "amd64", State: registry.StateCurrent,
		SourceRepository: repoA, BaseDigest: "sha256:bb", Commit: "c2", Replaces: "old", SizeBytes: 320}
	f.ptrs[pk(repoA, "node-web", "amd64")] = registry.Pointer{Repository: repoA, Profile: "node-web", Arch: "amd64", ImageID: "old"}
	f.ptrs[pk(repoB, "node-web", "amd64")] = registry.Pointer{Repository: repoB, Profile: "node-web", Arch: "amd64", ImageID: "old"}
	ck := &clock{t: base}
	cfg := Config{Images: f, Pointers: f, Plans: f, Holders: holders{"m2", "m1"}, Now: ck.now}
	if w != nil {
		cfg.Warmer = w
	}
	return New(cfg), f, ck
}

func waitDone(t *testing.T, s *Service, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.Wait(ctx, id)
	if ctx.Err() != nil {
		t.Fatal("plan run did not end")
	}
}

func pointerOf(t *testing.T, f *fake, repo string) string {
	t.Helper()
	p, err := f.Pointer(context.Background(), repo, "node-web", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	return p.ImageID
}

func TestPlanLifecycle(t *testing.T) {
	ctx := context.Background()
	w := &warmer{}
	s, f, ck := world(t, w)

	p, err := s.CreatePlan(ctx, "new", 7, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if p.State != registry.PlanDraft || p.StepsTotal != 3 || len(p.Repositories) != 1 || p.Repositories[0] != repoA {
		t.Fatalf("draft plan wrong: %+v", p)
	}
	if got := p.Machines; len(got) != 2 || got[0] != "m1" || got[1] != "m2" {
		t.Fatalf("machines = %v", got)
	}
	// R5.54: nothing changed yet.
	if pointerOf(t, f, repoA) != "old" || f.images["old"].State != registry.StateCurrent || len(f.moves) != 0 {
		t.Fatal("create changed something")
	}
	// A second plan for the same image is refused while the first is a draft.
	if _, err := s.CreatePlan(ctx, "new", 7, "owner"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second plan: %v", err)
	}

	if _, err := s.Start(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s, p.ID)

	got, _ := s.Plan(ctx, p.ID)
	if got.State != registry.PlanDone || got.StepsDone != 3 || got.FinishedAt.IsZero() {
		t.Fatalf("done plan wrong: %+v", got)
	}
	if pointerOf(t, f, repoA) != "new" {
		t.Fatal("pointer did not move")
	}
	old := f.images["old"]
	want := ck.t.Add(7 * 24 * time.Hour)
	if old.State != registry.StateSuperseded || !old.KeepUntil.Equal(want) || old.ReplacedBy != "new" || !got.KeepUntil.Equal(want) {
		t.Fatalf("old image wrong: %+v plan keep %v", old, got.KeepUntil)
	}
	sort.Strings(w.asked)
	if len(w.asked) != 2 || w.asked[0] != "m1" || w.asked[1] != "m2" {
		t.Fatalf("warmed %v", w.asked)
	}
	// A finished plan cannot start again.
	if _, err := s.Start(ctx, p.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("restart done plan: %v", err)
	}
}

func TestAnotherRepositoryKeepsTheOldImage(t *testing.T) {
	ctx := context.Background()
	s, f, _ := world(t, nil)

	c, err := s.Compare(ctx, "new")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Repositories) != 1 || c.Repositories[0] != repoA {
		t.Fatalf("repositories that move = %v", c.Repositories)
	}
	p, err := s.CreatePlan(ctx, "new", 7, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s, p.ID)

	if pointerOf(t, f, repoA) != "new" {
		t.Fatal("repoA pointer did not move")
	}
	if pointerOf(t, f, repoB) != "old" {
		t.Fatal("repoB pointer moved: the old image must stay for other repositories (D7)")
	}
	if len(f.moves) != 1 {
		t.Fatalf("moves = %v", f.moves)
	}
	if n, _ := f.PointersTo(ctx, "old"); len(n) != 1 {
		t.Fatalf("old image pointers = %d", len(n))
	}
}

func TestRollbackRestoresThePointer(t *testing.T) {
	ctx := context.Background()
	s, f, ck := world(t, nil)
	p, _ := s.CreatePlan(ctx, "new", 7, "owner")
	if _, err := s.Rollback(ctx, p.ID, "owner"); !errors.Is(err, ErrConflict) {
		t.Fatalf("rollback of a draft: %v", err)
	}
	_, _ = s.Start(ctx, p.ID)
	waitDone(t, s, p.ID)

	ck.t = ck.t.Add(3 * 24 * time.Hour) // inside the window
	rp, err := s.Rollback(ctx, p.ID, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if rp.State != registry.PlanRolledBack {
		t.Fatalf("state = %s", rp.State)
	}
	if pointerOf(t, f, repoA) != "old" {
		t.Fatal("pointer not restored")
	}
	if pointerOf(t, f, repoB) != "old" {
		t.Fatal("repoB changed")
	}
	if img := f.images["old"]; img.State != registry.StateCurrent || !img.KeepUntil.IsZero() {
		t.Fatalf("old image not restored: %+v", img)
	}
	if _, err := s.Rollback(ctx, p.ID, "owner"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second rollback: %v", err)
	}
}

func TestRollbackAfterKeepUntilIsRefused(t *testing.T) {
	ctx := context.Background()
	s, f, ck := world(t, nil)
	p, _ := s.CreatePlan(ctx, "new", 7, "owner")
	_, _ = s.Start(ctx, p.ID)
	waitDone(t, s, p.ID)

	ck.t = ck.t.Add(7*24*time.Hour + time.Second)
	_, err := s.Rollback(ctx, p.ID, "owner")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	if pointerOf(t, f, repoA) != "new" || f.images["old"].State != registry.StateSuperseded {
		t.Fatal("a refused rollback changed something")
	}
	if got, _ := s.Plan(ctx, p.ID); got.State != registry.PlanDone {
		t.Fatalf("plan state = %s", got.State)
	}
}

func TestStopAfterCurrentStepThenResume(t *testing.T) {
	ctx := context.Background()
	w := &warmer{gate: make(chan struct{}), enter: make(chan string, 4)}
	s, f, _ := world(t, w)
	p, _ := s.CreatePlan(ctx, "new", 7, "owner")
	if _, err := s.Stop(ctx, p.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("stop of a draft: %v", err)
	}
	_, _ = s.Start(ctx, p.ID)
	<-w.enter // first warm step is running; the pointer step is done
	if _, err := s.Stop(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	w.gate <- struct{}{} // the current step ends, the run stops before the next
	waitDone(t, s, p.ID)

	got, _ := s.Plan(ctx, p.ID)
	if got.State != registry.PlanStopped || got.StepsDone != 2 {
		t.Fatalf("stopped plan wrong: %+v", got)
	}
	if pointerOf(t, f, repoA) != "new" || f.images["old"].State != registry.StateCurrent {
		t.Fatal("a stopped plan keeps moved pointers and does not supersede yet")
	}
	// Start goes on from the saved step.
	close(w.gate)
	if _, err := s.Start(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s, p.ID)
	if got, _ := s.Plan(ctx, p.ID); got.State != registry.PlanDone || got.StepsDone != 3 {
		t.Fatalf("resumed plan wrong: %+v", got)
	}
	if len(f.moves) != 1 {
		t.Fatalf("pointer moved again on resume: %v", f.moves)
	}
}

func TestRecoverStopsOrphanedRunningPlans(t *testing.T) {
	ctx := context.Background()
	s, f, _ := world(t, nil)
	p, _ := s.CreatePlan(ctx, "new", 7, "owner")
	cur := f.plans[p.ID]
	cur.State, cur.StepsDone = registry.PlanRunning, 1
	f.plans[p.ID] = cur // as left by a controller that died mid-run
	if err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Plan(ctx, p.ID)
	if got.State != registry.PlanStopped || got.StepsDone != 1 || got.Error == "" {
		t.Fatalf("recovered plan wrong: %+v", got)
	}
}

func TestCreateValidation(t *testing.T) {
	ctx := context.Background()
	s, f, _ := world(t, nil)
	if _, err := s.CreatePlan(ctx, "new", 400, "o"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("keep days: %v", err)
	}
	if _, err := s.CreatePlan(ctx, "nope", 7, "o"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("unknown image: %v", err)
	}
	if _, err := s.CreatePlan(ctx, "old", 7, "o"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("image that replaces nothing: %v", err)
	}
	if _, err := s.Compare(ctx, "old"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("compare of an image that replaces nothing: %v", err)
	}
	img := f.images["new"]
	img.State = registry.StateVariant
	f.images["new"] = img
	if _, err := s.CreatePlan(ctx, "new", 7, "o"); !errors.Is(err, ErrConflict) {
		t.Fatalf("not current: %v", err)
	}
}

type stacks map[string][]registry.StackFile

func (s stacks) StackFiles(_ context.Context, repo, commit string) ([]registry.StackFile, error) {
	return s[repo+"@"+commit], nil
}

func TestCompare(t *testing.T) {
	ctx := context.Background()
	s, f, _ := world(t, nil)
	f.layers["old"] = []registry.Layer{
		{Digest: "L1", SizeBytes: 100, Images: []string{"old", "new"}},
		{Digest: "L2", SizeBytes: 200, Images: []string{"old"}},
		{Digest: "L3", SizeBytes: 50, Images: []string{"old", "other"}},
	}
	f.layers["new"] = []registry.Layer{{Digest: "L1", SizeBytes: 100, Images: []string{"old", "new"}}, {Digest: "L4", SizeBytes: 220, Images: []string{"new"}}}
	s.cfg.Stacks = stacks{
		repoA + "@c1": {{Path: "package.json", Content: []byte("a")}, {Path: ".nvmrc", Content: []byte("20")}, {Path: "gone.txt", Content: []byte("x")}},
		repoA + "@c2": {{Path: "package.json", Content: []byte("a")}, {Path: ".nvmrc", Content: []byte("22")}, {Path: "new.txt", Content: []byte("y")}},
	}
	c, err := s.Compare(ctx, "new")
	if err != nil {
		t.Fatal(err)
	}
	if !c.BaseChanged || c.SharedBytes != 100 {
		t.Fatalf("base %v shared %d", c.BaseChanged, c.SharedBytes)
	}
	// repoB still points at the old image, so nothing is freed yet.
	if c.FreesBytes != 0 {
		t.Fatalf("frees %d with another repository on the old image", c.FreesBytes)
	}
	var paths []string
	for _, sf := range c.StackFiles {
		paths = append(paths, sf.Path+":"+sf.Change)
	}
	want := []string{".nvmrc:changed", "gone.txt:removed", "new.txt:added"}
	if len(paths) != 3 || paths[0] != want[0] || paths[1] != want[1] || paths[2] != want[2] {
		t.Fatalf("stack changes = %v", paths)
	}
	// With repoB gone from the old image, L2 (held by old alone) is freed.
	delete(f.ptrs, pk(repoB, "node-web", "amd64"))
	c, _ = s.Compare(ctx, "new")
	if c.FreesBytes != 200 || c.KeepDaysDefault != registry.DefaultKeepOldDays {
		t.Fatalf("frees %d keep %d", c.FreesBytes, c.KeepDaysDefault)
	}
}
