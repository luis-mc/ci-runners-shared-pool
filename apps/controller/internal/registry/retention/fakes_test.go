package retention

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

var t0 = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC) // a Thursday

func day(n int) time.Time { return t0.Add(time.Duration(n) * 24 * time.Hour) }

// fakeRepo is an in-memory Repo.
type fakeRepo struct {
	mu        sync.Mutex
	settings  registry.Settings
	images    map[string]registry.Image
	staged    map[string]string // image id -> staged name
	pointers  map[string][]registry.Pointer
	running   map[string][]string
	leases    map[string]int
	recs      map[string]registry.Recommendation // open only
	closed    map[string]string
	layers    []registry.Layer
	deletedL  []string
	use       map[registry.Kind]KindUse
	readOnly  []bool
	cleanups  []Cleanup
	failStart error
}

func newRepo() *fakeRepo {
	return &fakeRepo{
		settings: registry.Settings{StandardBudget: registry.DefaultStandardBudget, CustomBudget: registry.DefaultCustomBudget,
			UnusedDays: 30, KeepOldDays: 7, VariantGoneDays: 7, VariantUnused: 14, CleanupWeekday: time.Sunday, CleanupHour: 3},
		images: map[string]registry.Image{}, staged: map[string]string{}, pointers: map[string][]registry.Pointer{},
		running: map[string][]string{}, leases: map[string]int{}, recs: map[string]registry.Recommendation{},
		closed: map[string]string{}, use: map[registry.Kind]KindUse{},
	}
}

func (r *fakeRepo) add(img registry.Image) registry.Image {
	if img.Manifest == "" {
		img.Manifest = "sha256:" + img.ID
	}
	if img.Name == "" {
		img.Name = "crsp/std/base"
	}
	if img.Kind == "" {
		img.Kind = registry.KindStandard
	}
	r.images[img.ID] = img
	return img
}

func (r *fakeRepo) Settings(context.Context) (registry.Settings, error) { return r.settings, nil }
func (r *fakeRepo) PutSettings(_ context.Context, s registry.Settings, _ string) error {
	r.settings = s
	return nil
}
func (r *fakeRepo) Images(context.Context) ([]registry.Image, error) {
	var out []registry.Image
	for _, i := range r.images {
		if i.State != registry.StateDeleted {
			out = append(out, i)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (r *fakeRepo) Image(_ context.Context, id string) (registry.Image, error) {
	i, ok := r.images[id]
	if !ok {
		return i, registry.ErrNotFound
	}
	return i, nil
}
func (r *fakeRepo) PointersTo(_ context.Context, id string) ([]registry.Pointer, error) {
	return r.pointers[id], nil
}
func (r *fakeRepo) RunningJobs(_ context.Context, id string) ([]string, error) {
	return r.running[id], nil
}
func (r *fakeRepo) LiveLeases(_ context.Context, _ time.Time, name string) (int, error) {
	return r.leases[name], nil
}
func (r *fakeRepo) MarkDeleted(_ context.Context, id string, _ time.Time, _ string) error {
	i := r.images[id]
	i.State = registry.StateDeleted
	r.images[id] = i
	return nil
}
func (r *fakeRepo) OldStaged(_ context.Context, before time.Time) ([]StagedImage, error) {
	var out []StagedImage
	for id, i := range r.images {
		if (i.State == registry.StateStaged || i.State == registry.StateRefused) && i.PushedAt.Before(before) {
			out = append(out, StagedImage{Image: i, StagedName: r.staged[id]})
		}
	}
	return out, nil
}
func (r *fakeRepo) Recommendations(context.Context) ([]registry.Recommendation, error) {
	var out []registry.Recommendation
	for _, x := range r.recs {
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (r *fakeRepo) Upsert(_ context.Context, x registry.Recommendation) error {
	for id, o := range r.recs {
		if keyOf(o) == keyOf(x) {
			x.ID = id
		}
	}
	r.recs[x.ID] = x
	return nil
}
func (r *fakeRepo) Close(_ context.Context, id, why string) error {
	delete(r.recs, id)
	r.closed[id] = why
	return nil
}
func (r *fakeRepo) DanglingLayers(context.Context) ([]registry.Layer, error) {
	var out []registry.Layer
	for _, l := range r.layers {
		gone := false
		for _, d := range r.deletedL {
			gone = gone || d == l.Digest
		}
		if !gone {
			out = append(out, l)
		}
	}
	return out, nil
}
func (r *fakeRepo) DeleteLayer(_ context.Context, d string) error {
	r.deletedL = append(r.deletedL, d)
	return nil
}
func (r *fakeRepo) Use(context.Context) (map[registry.Kind]KindUse, error) { return r.use, nil }
func (r *fakeRepo) SetReadOnly(_ context.Context, on bool) error {
	r.readOnly = append(r.readOnly, on)
	return nil
}
func (r *fakeRepo) StartCleanup(_ context.Context, trigger, by string) (int64, error) {
	if r.failStart != nil {
		return 0, r.failStart
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanups = append(r.cleanups, Cleanup{ID: int64(len(r.cleanups) + 1), Trigger: trigger, State: "running", StartedBy: by, StartedAt: t0})
	return int64(len(r.cleanups)), nil
}
func (r *fakeRepo) FinishCleanup(_ context.Context, id int64, state string, freed int64, e string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := &r.cleanups[id-1]
	c.State, c.FreedBytes, c.Error = state, freed, e
	return nil
}
func (r *fakeRepo) Cleanup(_ context.Context, id int64) (Cleanup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cleanups[id-1], nil
}
func (r *fakeRepo) LastCleanup(context.Context) (Cleanup, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.cleanups) == 0 {
		return Cleanup{}, registry.ErrNotFound
	}
	return r.cleanups[len(r.cleanups)-1], nil
}

// fakeRegistry records the calls and the read-only state at each.
type fakeRegistry struct {
	readOnly     bool
	deleted      []string
	roAtGC       []bool
	gcDry        []bool
	gcFreed      int64
	gcErr        error
	dryErr       error
	deleteErr    error
	roAtDelete   []bool
	readOnlyLogs []bool
}

func (f *fakeRegistry) SetReadOnly(on bool) {
	f.readOnly = on
	f.readOnlyLogs = append(f.readOnlyLogs, on)
}
func (f *fakeRegistry) DeleteManifest(_ context.Context, name, digest string) error {
	f.deleted = append(f.deleted, name+"@"+digest)
	f.roAtDelete = append(f.roAtDelete, f.readOnly)
	return f.deleteErr
}
func (f *fakeRegistry) GC(_ context.Context, dry bool) (int64, error) {
	f.roAtGC = append(f.roAtGC, f.readOnly)
	f.gcDry = append(f.gcDry, dry)
	if dry {
		return 0, f.dryErr
	}
	return f.gcFreed, f.gcErr
}

type fakeDisk struct{ free int64 }

func (d fakeDisk) Free(string) (int64, error) { return d.free, nil }

type auditEntry struct {
	Actor  Actor
	Action string
	ID     string
}

type fakeAudit struct {
	entries []auditEntry
	err     error
}

func (a *fakeAudit) Record(_ context.Context, actor Actor, action, _ string, id string, _ map[string]any) error {
	if a.err != nil {
		return a.err
	}
	a.entries = append(a.entries, auditEntry{actor, action, id})
	return nil
}
func (a *fakeAudit) has(action string) bool {
	for _, e := range a.entries {
		if e.Action == action {
			return true
		}
	}
	return false
}

type env struct {
	repo *fakeRepo
	reg  *fakeRegistry
	aud  *fakeAudit
	svc  *Service
	now  time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{repo: newRepo(), reg: &fakeRegistry{}, aud: &fakeAudit{}, now: t0}
	e.svc = New(Options{Repo: e.repo, Registry: e.reg, Disk: fakeDisk{free: 1_000_000_000_000}, Audit: e.aud,
		DataDir: absPath("/data"), Now: func() time.Time { return e.now }})
	return e
}

func (e *env) recFor(rule registry.Rule, image string) (registry.Recommendation, bool) {
	for _, r := range e.repo.recs {
		if r.Rule == rule && r.ImageID == image {
			return r, true
		}
	}
	return registry.Recommendation{}, false
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func isErr(err, target error) bool { return errors.Is(err, target) }
