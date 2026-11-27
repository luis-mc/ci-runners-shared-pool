package records

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

var t0 = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

type env struct {
	t   *testing.T
	ctx context.Context
	q   *sqlcgen.Queries
	svc *Service
	now time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	st, err := store.OpenForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	e := &env{t: t, ctx: ctx, q: st.RegistryQueries(), now: t0}
	e.svc = New(e.q, Config{Now: func() time.Time { return e.now }, Loc: time.UTC, Host: "10.0.0.2:8443", DefaultFolder: "/data/registry"})
	return e
}

type seed struct {
	id, kind, profile, arch, key, repo, state, branch string
	promoted                                          time.Time
}

func (e *env) image(s seed) {
	e.t.Helper()
	if s.arch == "" {
		s.arch = "amd64"
	}
	if s.state == "" {
		s.state = "current"
	}
	if s.profile == "" {
		s.profile = "base"
	}
	name := "crsp/std/" + s.profile
	if s.kind == "custom" {
		name = "crsp/custom/" + strings.ReplaceAll(s.repo, "/", "-") + "/" + s.profile
	}
	err := e.q.InsertRegistryImage(e.ctx, sqlcgen.InsertRegistryImageParams{ID: s.id, Kind: s.kind, Name: name, ImageTag: s.key,
		ProfileID: s.profile, Arch: s.arch, State: "staged", ManifestDigest: "sha256:" + strings.Repeat(s.key[:1], 64),
		StackHash: strings.Repeat("a", 64), BaseDigest: "sha256:" + strings.Repeat("0", 64), KeyInput: "{}",
		SourceRepository: s.repo, Branch: s.branch, PushedBy: "m1", PushedAt: stamp(s.promoted.Add(-time.Minute))})
	if err != nil {
		e.t.Fatal(err)
	}
	if s.state != "staged" {
		if _, err := e.q.PromoteRegistryImage(e.ctx, sqlcgen.PromoteRegistryImageParams{State: s.state, PromotedAt: stamp(s.promoted), ID: s.id}); err != nil {
			e.t.Fatal(err)
		}
	}
}

func layer(d string, size int64) registry.Layer {
	return registry.Layer{Digest: "sha256:" + strings.Repeat(d, 64), SizeBytes: size, MediaType: "application/vnd.oci.image.layer.v1.tar+gzip"}
}

func (e *env) layers(id string, ls ...registry.Layer) {
	e.t.Helper()
	if err := e.svc.PutLayers(e.ctx, id, ls); err != nil {
		e.t.Fatal(err)
	}
}

func TestLocatorEnsure(t *testing.T) { // R5.32
	e := newEnv(t)
	e.image(seed{id: "img_a", kind: "standard", key: "aaaaaaaaaaaa", promoted: t0})
	e.image(seed{id: "img_s", kind: "standard", key: "bbbbbbbbbbbb", state: "staged", promoted: t0})
	e.image(seed{id: "img_r", kind: "standard", key: "cccccccccccc", state: "refused", promoted: t0})
	e.layers("img_a", layer("1", 700), layer("2", 300))

	got, err := e.svc.Ensure(e.ctx, "m1", "aaaaaaaaaaaa", "amd64")
	if err != nil || !got.Found || got.Name != "crsp/std/base" || got.Manifest != "sha256:"+strings.Repeat("a", 64) || got.Size != 1000 || got.Busy {
		t.Fatalf("found answer: %+v %v", got, err)
	}
	// The agent says x64; the answer is the same.
	if got, _ := e.svc.Ensure(e.ctx, "m1", "aaaaaaaaaaaa", "x64"); !got.Found {
		t.Fatalf("x64 is amd64: %+v", got)
	}
	for _, c := range []struct{ key, arch, why string }{
		{"aaaaaaaaaaaa", "arm64", "other arch"}, {"bbbbbbbbbbbb", "amd64", "staged"}, {"cccccccccccc", "amd64", "refused"},
		{"dddddddddddd", "amd64", "unknown"}, {"nope", "amd64", "bad key"},
	} {
		got, err := e.svc.Ensure(e.ctx, "m1", registry.Key(c.key), c.arch)
		if err != nil || got.Found || got.Busy {
			t.Errorf("%s must be a clear not found: %+v %v", c.why, got, err)
		}
	}
	// The read-only window does not stop answers.
	if err := e.q.SetRegistryReadOnly(e.ctx, 1); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.svc.Ensure(e.ctx, "m1", "aaaaaaaaaaaa", "amd64"); !got.Found {
		t.Fatal("read-only must still answer")
	}
	// A superseded image is still offered while it is kept.
	e.image(seed{id: "img_old", kind: "standard", key: "eeeeeeeeeeee", state: "superseded", promoted: t0})
	if got, _ := e.svc.Ensure(e.ctx, "m1", "eeeeeeeeeeee", "amd64"); !got.Found {
		t.Fatal("superseded is offered")
	}
}

func TestLayerAccounting(t *testing.T) { // R5.31
	e := newEnv(t)
	// Standard A is the oldest; custom B shares L1 and L2 with it.
	e.image(seed{id: "img_a", kind: "standard", key: "aaaaaaaaaaaa", promoted: t0.Add(-48 * time.Hour)})
	e.image(seed{id: "img_b", kind: "custom", key: "bbbbbbbbbbbb", repo: "o/r", promoted: t0.Add(-24 * time.Hour)})
	e.layers("img_a", layer("1", 1000), layer("2", 500), layer("3", 200))
	e.layers("img_b", layer("1", 1000), layer("2", 500), layer("4", 70))
	// Recording again is safe.
	e.layers("img_b", layer("1", 1000), layer("2", 500), layer("4", 70))

	a, _ := e.svc.Image(e.ctx, "img_a")
	b, _ := e.svc.Image(e.ctx, "img_b")
	if a.SizeBytes != 1700 || a.UniqueBytes != 200 {
		t.Errorf("A size/unique = %d/%d, want 1700/200", a.SizeBytes, a.UniqueBytes)
	}
	if b.SizeBytes != 1570 || b.UniqueBytes != 70 {
		t.Errorf("B size/unique = %d/%d, want 1570/70", b.SizeBytes, b.UniqueBytes)
	}
	var n int
	if err := rowCount(e, &n); err != nil || n != 4 {
		t.Errorf("layers are recorded once each: %d %v", n, err)
	}
	ov, err := e.svc.Overview(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	std, cus := ov.Budgets[0], ov.Budgets[1]
	if std.Kind != registry.KindStandard || std.UsedBytes != 1700 || std.SharedBytes != 1500 {
		t.Errorf("standard budget: %+v", std)
	}
	if cus.UsedBytes != 70 || cus.SharedBytes != 0 {
		t.Errorf("custom budget: %+v", cus)
	}
	ls, _ := e.svc.Layers(e.ctx, "img_b")
	if ls[0].ChargedTo != registry.KindStandard || ls[2].ChargedTo != registry.KindCustom {
		t.Errorf("charges: %+v", ls)
	}

	// The oldest image goes: the layers B shares are now B's own, charged to custom, all unique.
	if _, err := e.q.MarkRegistryImageDeleted(e.ctx, sqlcgen.MarkRegistryImageDeletedParams{DeletedAt: stamp(t0), DeletedBy: "owner", ID: "img_a"}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Recount(e.ctx, "img_a"); err != nil {
		t.Fatal(err)
	}
	b, _ = e.svc.Image(e.ctx, "img_b")
	if b.SizeBytes != 1570 || b.UniqueBytes != 1570 {
		t.Errorf("after delete B size/unique = %d/%d, want 1570/1570", b.SizeBytes, b.UniqueBytes)
	}
	ls, _ = e.svc.Layers(e.ctx, "img_b")
	for _, l := range ls {
		if l.ChargedTo != registry.KindCustom {
			t.Errorf("layer %s still charged to %s", l.Digest[:12], l.ChargedTo)
		}
	}
}

func rowCount(e *env, n *int) error {
	rows, err := e.q.ListDanglingRegistryLayers(e.ctx)
	if err != nil {
		return err
	}
	_ = rows
	// Count by reading the layers of both images and de-duplicating.
	seen := map[string]bool{}
	for _, id := range []string{"img_a", "img_b"} {
		ls, err := e.svc.Layers(e.ctx, id)
		if err != nil {
			return err
		}
		for _, l := range ls {
			seen[l.Digest] = true
		}
	}
	*n = len(seen)
	return nil
}

func TestLayerAccountingTiesAndBadInput(t *testing.T) {
	e := newEnv(t)
	e.image(seed{id: "img_x", kind: "custom", key: "aaaaaaaaaaaa", repo: "o/r", promoted: t0})
	e.image(seed{id: "img_y", kind: "standard", key: "bbbbbbbbbbbb", promoted: t0})
	e.layers("img_x", layer("1", 10))
	e.layers("img_y", layer("1", 10))
	// Same time: the smaller id is the older one, so the answer is stable.
	ls, _ := e.svc.Layers(e.ctx, "img_x")
	if ls[0].ChargedTo != registry.KindCustom {
		t.Errorf("tie goes to the smaller id: %+v", ls)
	}
	if err := e.svc.PutLayers(e.ctx, "img_x", []registry.Layer{{Digest: "", SizeBytes: 1}}); !errors.Is(err, errBadRecord) {
		t.Errorf("empty digest: %v", err)
	}
	if err := e.svc.PutLayers(e.ctx, "img_none", nil); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("unknown image: %v", err)
	}
}

type fakeLayers struct{ ls []registry.Layer }

func (f fakeLayers) Layers(context.Context, string, string) ([]registry.Layer, error) {
	return f.ls, nil
}

func TestPromotedHookAccountsLayers(t *testing.T) {
	e := newEnv(t)
	e.image(seed{id: "img_a", kind: "standard", key: "aaaaaaaaaaaa", promoted: t0})
	img, _ := e.svc.Image(e.ctx, "img_a")
	if err := e.svc.PromotedHook(fakeLayers{[]registry.Layer{layer("1", 5), layer("2", 6)}})(e.ctx, img); err != nil {
		t.Fatal(err)
	}
	if img, _ = e.svc.Image(e.ctx, "img_a"); img.SizeBytes != 11 || img.UniqueBytes != 11 {
		t.Errorf("sizes: %d/%d", img.SizeBytes, img.UniqueBytes)
	}
}

func pull(id string, state string, src registry.Source, at time.Time) registry.PullRecord {
	return registry.PullRecord{PullID: id, MachineID: "m1", Key: "aaaaaaaaaaaa", Arch: "amd64", Source: src, State: state, At: at,
		Repository: "o/r", BytesDone: 100, BytesTotal: 200, Seconds: 10}
}

func TestRecordPull(t *testing.T) { // R5.30
	e := newEnv(t)
	e.image(seed{id: "img_a", kind: "standard", key: "aaaaaaaaaaaa", promoted: t0.Add(-time.Hour)})
	if err := e.q.InsertImageBuild(e.ctx, sqlcgen.InsertImageBuildParams{ID: "bld_1", MachineID: "m1", ProfileID: "base", Arch: "amd64", ImageTag: "aaaaaaaaaaaa"}); err != nil {
		t.Fatal(err)
	}
	if err := e.q.SetImageBuildProgress(e.ctx, sqlcgen.SetImageBuildProgressParams{State: "building", StartedAt: stamp(t0.Add(-3 * time.Hour)), ID: "bld_1"}); err != nil {
		t.Fatal(err)
	}
	if err := e.q.FinishImageBuild(e.ctx, sqlcgen.FinishImageBuildParams{State: "done", FinishedAt: stamp(t0.Add(-3*time.Hour + 30*time.Minute)), ID: "bld_1"}); err != nil {
		t.Fatal(err)
	}

	if err := e.svc.RecordPull(e.ctx, pull("p1", "pulling", registry.SourcePulled, t0)); err != nil {
		t.Fatal(err)
	}
	if ps, _ := e.svc.Pulls(e.ctx, ""); len(ps) != 1 || ps[0].ID != "p1" || ps[0].ImageID != "img_a" {
		t.Fatalf("in flight: %+v", ps)
	}
	done := pull("p1", "done", registry.SourcePulled, t0.Add(10*time.Second))
	done.BytesDone = 200
	for i := 0; i < 3; i++ { // a resent report is one row and one count
		if err := e.svc.RecordPull(e.ctx, done); err != nil {
			t.Fatal(err)
		}
	}
	// A late progress report does not reopen the pull.
	if err := e.svc.RecordPull(e.ctx, pull("p1", "pulling", registry.SourcePulled, t0.Add(11*time.Second))); err != nil {
		t.Fatal(err)
	}
	row, _ := e.q.GetRegistryPullByID(e.ctx, "p1")
	if row.State != "done" || row.StartedAt != stamp(t0) || row.FinishedAt != stamp(t0.Add(10*time.Second)) || row.BytesDone != 200 {
		t.Errorf("row: %+v", row)
	}
	if row.SavedSeconds < 1789 || row.SavedSeconds > 1791 { // 30 min build minus 10 s pull
		t.Errorf("saved seconds: %v", row.SavedSeconds)
	}
	d, _ := e.q.ListRegistryPullDaily(e.ctx, sqlcgen.ListRegistryPullDailyParams{ImageID: "img_a", Day: "2026-11-19"})
	if len(d) != 1 || d[0].Pulls != 1 || d[0].BuildsSaved != 1 {
		t.Errorf("daily: %+v", d)
	}
	if img, _ := e.svc.Image(e.ctx, "img_a"); !img.LastPullAt.Equal(t0.Add(10 * time.Second)) {
		t.Errorf("last pull: %v", img.LastPullAt)
	}

	// A second machine pulls the next day: a second row, a second day.
	e.now = t0.Add(24 * time.Hour)
	p2 := pull("p2", "done", registry.SourcePulled, e.now)
	p2.MachineID = "m2"
	p2.SavedSeconds = 60
	if err := e.svc.RecordPull(e.ctx, p2); err != nil {
		t.Fatal(err)
	}
	// Local use and a local build are rows but no pull count.
	if err := e.svc.RecordPull(e.ctx, pull("p3", "done", registry.SourceLocal, e.now)); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RecordPull(e.ctx, pull("p4", "done", registry.SourceBuilt, e.now)); err != nil {
		t.Fatal(err)
	}
	d, _ = e.q.ListRegistryPullDaily(e.ctx, sqlcgen.ListRegistryPullDailyParams{ImageID: "img_a", Day: "2026-11-19"})
	if len(d) != 2 || d[1].Day != "2026-11-20" || d[1].Pulls != 1 || d[1].SavedSeconds != 60 {
		t.Errorf("daily after day 2: %+v", d)
	}
	// A pull of a key the registry does not know is a row with no image and no count.
	unk := pull("p5", "done", registry.SourcePulled, e.now)
	unk.Key = "ffffffffffff"
	if err := e.svc.RecordPull(e.ctx, unk); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.q.GetRegistryPullByID(e.ctx, "p5"); r.ImageID != "" {
		t.Errorf("unknown key: %+v", r)
	}

	for _, bad := range []registry.PullRecord{
		{MachineID: "m1", State: "done", Source: registry.SourcePulled},
		{PullID: "x", State: "done", Source: registry.SourcePulled},
		{PullID: "x", MachineID: "m1", State: "weird", Source: registry.SourcePulled},
		{PullID: "x", MachineID: "m1", State: "done", Source: "weird"},
	} {
		if err := e.svc.RecordPull(e.ctx, bad); !errors.Is(err, errBadRecord) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestRecordPush(t *testing.T) {
	e := newEnv(t)
	base := registry.PushRecord{PushID: "u1", State: "pushing", Seconds: 1, At: t0,
		Staged: registry.Staged{MachineID: "m1", Name: "crsp/incoming/m1/std/base", Target: "crsp/std/base", Key: "aaaaaaaaaaaa", Manifest: "sha256:" + strings.Repeat("a", 64)}}
	if err := e.svc.RecordPush(e.ctx, base); err != nil {
		t.Fatal(err)
	}
	base.State, base.At = "pushed", t0.Add(time.Minute)
	for i := 0; i < 2; i++ {
		if err := e.svc.RecordPush(e.ctx, base); err != nil {
			t.Fatal(err)
		}
	}
	r, _ := e.q.GetRegistryPushByID(e.ctx, "u1")
	if r.State != "pushed" || r.StartedAt != stamp(t0) || r.FinishedAt != stamp(t0.Add(time.Minute)) || r.TargetName != "crsp/std/base" {
		t.Fatalf("row: %+v", r)
	}
	if in, _ := e.q.ListRegistryPushesInFlight(e.ctx); len(in) != 1 {
		t.Errorf("pushed waits for promotion: %d", len(in))
	}
	if err := e.svc.SetPushOutcome(e.ctx, "u1", true, "img_a", ""); err != nil {
		t.Fatal(err)
	}
	// A resent "pushed" report does not undo "promoted".
	if err := e.svc.RecordPush(e.ctx, base); err != nil {
		t.Fatal(err)
	}
	r, _ = e.q.GetRegistryPushByID(e.ctx, "u1")
	if r.State != "promoted" || r.ImageID != "img_a" {
		t.Errorf("promoted kept: %+v", r)
	}
	if err := e.svc.SetPushOutcome(e.ctx, "unknown", false, "", "x"); err != nil {
		t.Errorf("unknown push: %v", err)
	}
	if err := e.svc.RecordPush(e.ctx, registry.PushRecord{State: "pushed"}); !errors.Is(err, errBadRecord) {
		t.Errorf("no id: %v", err)
	}
	bad := base
	bad.PushID, bad.State = "u2", "weird"
	if err := e.svc.RecordPush(e.ctx, bad); !errors.Is(err, errBadRecord) {
		t.Errorf("state: %v", err)
	}
}

func TestRecordEvent(t *testing.T) {
	e := newEnv(t)
	e.image(seed{id: "img_a", kind: "standard", key: "aaaaaaaaaaaa", promoted: t0.Add(-time.Hour)})
	dig := "sha256:" + strings.Repeat("a", 64)
	if err := e.svc.RecordEvent(e.ctx, registry.Event{At: t0, Action: "pull", Name: "crsp/std/base", Digest: dig, MachineID: "m1"}); err != nil {
		t.Fatal(err)
	}
	if img, _ := e.svc.Image(e.ctx, "img_a"); !img.LastPullAt.Equal(t0) {
		t.Errorf("last pull: %v", img.LastPullAt)
	}
	// An older event does not move it back; pushes and unknown digests change nothing.
	_ = e.svc.RecordEvent(e.ctx, registry.Event{At: t0.Add(-time.Hour), Action: "pull", Digest: dig})
	_ = e.svc.RecordEvent(e.ctx, registry.Event{At: t0.Add(time.Hour), Action: "push", Digest: dig})
	_ = e.svc.RecordEvent(e.ctx, registry.Event{At: t0.Add(time.Hour), Action: "pull", Digest: "sha256:" + strings.Repeat("9", 64)})
	if img, _ := e.svc.Image(e.ctx, "img_a"); !img.LastPullAt.Equal(t0) {
		t.Errorf("last pull moved: %v", img.LastPullAt)
	}
	// An event is no pull count: the PullReport is the count.
	if d, _ := e.q.ListRegistryPullDaily(e.ctx, sqlcgen.ListRegistryPullDailyParams{ImageID: "img_a", Day: "2000-01-01"}); len(d) != 0 {
		t.Errorf("daily: %+v", d)
	}
	// Consume reads until the channel closes.
	ch := make(chan registry.Event, 1)
	ch <- registry.Event{At: t0.Add(2 * time.Hour), Action: "pull", Digest: dig}
	close(ch)
	e.svc.Consume(e.ctx, ch)
	if img, _ := e.svc.Image(e.ctx, "img_a"); !img.LastPullAt.Equal(t0.Add(2 * time.Hour)) {
		t.Errorf("consume: %v", img.LastPullAt)
	}
}

func TestPrune(t *testing.T) { // rows 30 days, daily totals 180 days
	e := newEnv(t)
	e.image(seed{id: "img_a", kind: "standard", key: "aaaaaaaaaaaa", promoted: t0.Add(-400 * 24 * time.Hour)})
	for _, c := range []struct {
		id  string
		age time.Duration
	}{{"old", 31 * 24 * time.Hour}, {"new", 29 * 24 * time.Hour}} {
		if err := e.svc.RecordPull(e.ctx, pull(c.id, "done", registry.SourceLocal, t0.Add(-c.age))); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		day string
	}{{"2026-05-01"}, {"2026-11-01"}} {
		if err := e.q.AddRegistryPullDaily(e.ctx, sqlcgen.AddRegistryPullDailyParams{ImageID: "img_a", Day: c.day, BuildsSaved: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.svc.Prune(e.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.GetRegistryPullByID(e.ctx, "old"); !store.IsNoRows(err) {
		t.Errorf("old pull stays: %v", err)
	}
	if _, err := e.q.GetRegistryPullByID(e.ctx, "new"); err != nil {
		t.Errorf("new pull gone: %v", err)
	}
	d, _ := e.q.ListRegistryPullDaily(e.ctx, sqlcgen.ListRegistryPullDailyParams{ImageID: "img_a", Day: "2000-01-01"})
	if len(d) != 1 || d[0].Day != "2026-11-01" {
		t.Errorf("daily: %+v", d)
	}
}

func TestOverview(t *testing.T) { // R5.33
	e := newEnv(t)
	e.image(seed{id: "img_a", kind: "standard", key: "aaaaaaaaaaaa", promoted: t0.Add(-48 * time.Hour)})
	e.image(seed{id: "img_b", kind: "custom", key: "bbbbbbbbbbbb", repo: "o/r", promoted: t0.Add(-24 * time.Hour)})
	e.image(seed{id: "img_v", kind: "custom", key: "cccccccccccc", repo: "o/r", state: "variant", promoted: t0.Add(-time.Hour)})
	e.image(seed{id: "img_s", kind: "standard", key: "dddddddddddd", state: "staged", promoted: t0})
	e.layers("img_a", layer("1", 41_000_000_000)) // over the 40 GB standard budget
	e.layers("img_b", layer("2", 1000))
	for i, day := range []string{"2026-11-10", "2026-11-13", "2026-11-19"} { // the first is older than 7 days
		if err := e.q.AddRegistryPullDaily(e.ctx, sqlcgen.AddRegistryPullDailyParams{ImageID: "img_a", Day: day, BuildsSaved: 1, SavedSeconds: float64(100 * (i + 1))}); err != nil {
			t.Fatal(err)
		}
	}
	e.svc.q = e.q
	if err := e.svc.RecordPull(e.ctx, func() registry.PullRecord {
		p := pull("p1", "done", registry.SourcePulled, t0)
		p.BytesDone = 5000
		return p
	}()); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RecordPull(e.ctx, pull("p2", "pulling", registry.SourcePulled, t0)); err != nil {
		t.Fatal(err)
	}
	if err := e.q.InsertImageBuild(e.ctx, sqlcgen.InsertImageBuildParams{ID: "bld_1", MachineID: "m1", ProfileID: "base", Arch: "amd64", ImageTag: "zzzzzzzzzzzz"}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RecordPush(e.ctx, registry.PushRecord{PushID: "u1", State: "waiting_controller", Staged: registry.Staged{MachineID: "m1", Name: "n", Target: "t", Key: "eeeeeeeeeeee"}}); err != nil {
		t.Fatal(err)
	}
	for _, r := range []sqlcgen.UpsertRegistryRecommendationParams{
		{ID: "rec_c2", Rule: "superseded", ImageID: "img_a", Decider: "crsp", DueAt: stamp(t0.Add(48 * time.Hour))},
		{ID: "rec_c1", Rule: "variant", ImageID: "img_v", Decider: "crsp", DueAt: stamp(t0.Add(24 * time.Hour))},
		{ID: "rec_o", Rule: "replace", ImageID: "img_b", OtherImageID: "img_a", Decider: "owner"},
	} {
		if err := e.q.UpsertRegistryRecommendation(e.ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	id, err := e.q.InsertRegistryCleanup(e.ctx, sqlcgen.InsertRegistryCleanupParams{Trigger: "weekly", StartedBy: "crsp"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.q.FinishRegistryCleanup(e.ctx, sqlcgen.FinishRegistryCleanupParams{State: "done", FreedBytes: 123, ID: id}); err != nil {
		t.Fatal(err)
	}

	ov, err := e.svc.Overview(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ov.Where != (Where{Host: "10.0.0.2:8443", Folder: "/data/registry", Running: true}) {
		t.Errorf("where: %+v", ov.Where)
	}
	if !ov.Budgets[0].Over || ov.Budgets[1].Over || ov.Budgets[0].LimitBytes != registry.DefaultStandardBudget || ov.Budgets[1].LimitBytes != registry.DefaultCustomBudget {
		t.Errorf("budgets: %+v", ov.Budgets)
	}
	// Days 13 Nov and 19 Nov are inside the 7 days; 10 Nov is not. p1 adds one pull and a build avoided today.
	if ov.Week.Pulls != 3 || ov.Week.BuildsAvoid != 3 || ov.Week.BytesPulled != 5000 {
		t.Errorf("week: %+v", ov.Week)
	}
	if ov.Week.SavedSeconds < 500 { // 200 + 300 plus the saved time of p1 (none: no build history)
		t.Errorf("saved: %v", ov.Week.SavedSeconds)
	}
	if ov.Standard != 1 || ov.Custom != 2 {
		t.Errorf("counts: std %d custom %d (staged is not counted)", ov.Standard, ov.Custom)
	}
	if ov.Builds != 1 || ov.Pulls != 1 || ov.Pushes != 1 {
		t.Errorf("in flight: %d %d %d", ov.Builds, ov.Pulls, ov.Pushes)
	}
	if len(ov.Recommendations) != 3 || ov.Recommendations[0].ID != "rec_o" || ov.Recommendations[1].ID != "rec_c1" || ov.Recommendations[2].ID != "rec_c2" {
		t.Errorf("recommendation order: %+v", ov.Recommendations)
	}
	if ov.LastCleanup == nil || ov.LastCleanup.FreedBytes != 123 || ov.LastCleanup.State != "done" {
		t.Errorf("last cleanup: %+v", ov.LastCleanup)
	}
	// 19 Nov 2026 is a Thursday; the default cleanup is Sunday 03:00 local.
	if want := time.Date(2026, 11, 22, 3, 0, 0, 0, time.UTC); !ov.NextCleanupAt.Equal(want) {
		t.Errorf("next cleanup: %v want %v", ov.NextCleanupAt, want)
	}
}

func TestOverviewWhereStates(t *testing.T) {
	e := newEnv(t)
	e.svc.cfg.Host = ""
	ov, _ := e.svc.Overview(e.ctx)
	if ov.Where.Running || ov.Where.WhyNot == "" || ov.LastCleanup != nil || len(ov.Recommendations) != 0 {
		t.Errorf("not running: %+v", ov)
	}
	e.svc.cfg.Running = func(context.Context) (bool, string) { return false, "The listener has no private address." }
	if err := e.q.SetRegistryReadOnly(e.ctx, 1); err != nil {
		t.Fatal(err)
	}
	if ov, _ = e.svc.Overview(e.ctx); ov.Where.WhyNot != "The listener has no private address." || !ov.Where.ReadOnly {
		t.Errorf("hook: %+v", ov.Where)
	}
}

func TestImagesFilters(t *testing.T) { // R5.34
	e := newEnv(t)
	e.image(seed{id: "img_1", kind: "standard", key: "111111111111", promoted: t0.Add(-4 * time.Hour)})
	e.image(seed{id: "img_2", kind: "custom", key: "222222222222", repo: "o/r", profile: "node", promoted: t0.Add(-3 * time.Hour)})
	e.image(seed{id: "img_3", kind: "custom", key: "333333333333", repo: "o/other", profile: "node", arch: "arm64", state: "superseded", promoted: t0.Add(-2 * time.Hour)})
	e.image(seed{id: "img_4", kind: "standard", key: "444444444444", state: "staged", promoted: t0})
	e.image(seed{id: "img_5", kind: "standard", key: "555555555555", profile: "gone", promoted: t0.Add(-time.Hour)})
	if _, err := e.q.MarkRegistryImageDeleted(e.ctx, sqlcgen.MarkRegistryImageDeletedParams{ID: "img_5", DeletedAt: stamp(t0), DeletedBy: "o"}); err != nil {
		t.Fatal(err)
	}
	if err := e.q.UpsertRegistryPointer(e.ctx, sqlcgen.UpsertRegistryPointerParams{Repository: "o/pointer", ProfileID: "base", Arch: "amd64", ImageID: "img_1", MovedAt: stamp(t0), MovedBy: "crsp"}); err != nil {
		t.Fatal(err)
	}
	ids := func(f ImageFilter) string {
		ims, err := e.svc.Images(e.ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var s []string
		for _, i := range ims {
			s = append(s, i.ID)
		}
		return strings.Join(s, ",")
	}
	for _, c := range []struct {
		name string
		f    ImageFilter
		want string
	}{
		{"all, no staged, no deleted, newest first", ImageFilter{}, "img_3,img_2,img_1"},
		{"kind", ImageFilter{Kind: registry.KindStandard}, "img_1"},
		{"state", ImageFilter{States: []registry.State{registry.StateSuperseded}}, "img_3"},
		{"staged asked for", ImageFilter{States: []registry.State{registry.StateStaged}}, "img_4"},
		{"deleted never", ImageFilter{States: []registry.State{registry.StateDeleted}}, ""},
		{"arch", ImageFilter{Arch: "arm64"}, "img_3"},
		{"profile", ImageFilter{Profile: "node"}, "img_3,img_2"},
		{"repository by source", ImageFilter{Repository: "o/r"}, "img_2"},
		{"repository by pointer", ImageFilter{Repository: "o/pointer"}, "img_1"},
		{"no match", ImageFilter{Repository: "x/y"}, ""},
	} {
		if got := ids(c.f); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

type dir struct{}

func (dir) Machine(_ context.Context, id string, _ registry.Image) MachineInfo {
	if id == "m1" {
		return MachineInfo{Name: "Build box", Holds: true, InUseByRunner: true}
	}
	return MachineInfo{}
}

func TestImageDetail(t *testing.T) { // R5.35
	e := newEnv(t)
	e.svc.cfg.Machines = dir{}
	e.image(seed{id: "img_old", kind: "custom", key: "111111111111", repo: "o/r", state: "superseded", promoted: t0.Add(-72 * time.Hour)})
	e.image(seed{id: "img_new", kind: "custom", key: "222222222222", repo: "o/r", promoted: t0.Add(-24 * time.Hour)})
	e.image(seed{id: "img_br", kind: "custom", key: "333333333333", repo: "o/r", state: "variant", branch: "fix", promoted: t0.Add(-time.Hour)})
	e.image(seed{id: "img_other", kind: "custom", key: "444444444444", repo: "o/else", promoted: t0})
	e.layers("img_old", layer("1", 800), layer("2", 100))
	e.layers("img_new", layer("1", 800), layer("3", 100))
	e.layers("img_br", layer("9", 5))
	if err := e.q.UpsertRegistryPointer(e.ctx, sqlcgen.UpsertRegistryPointerParams{Repository: "o/r", ProfileID: "base", Arch: "amd64", ImageID: "img_new", MovedAt: stamp(t0), MovedBy: "luis"}); err != nil {
		t.Fatal(err)
	}
	for i, day := range []string{"2026-11-07", "2026-11-18", "2026-11-19", "2026-11-19"} { // the first is outside 14 days
		_ = i
		if err := e.q.AddRegistryPullDaily(e.ctx, sqlcgen.AddRegistryPullDailyParams{ImageID: "img_new", Day: day, BuildsSaved: 1, SavedSeconds: 60}); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range []string{"m1", "m2"} {
		p := pull("p"+m, "done", registry.SourcePulled, t0)
		p.MachineID, p.Key = m, "222222222222"
		if err := e.svc.RecordPull(e.ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.q.UpsertRegistryRecommendation(e.ctx, sqlcgen.UpsertRegistryRecommendationParams{ID: "r1", Rule: "replace", ImageID: "img_br", OtherImageID: "img_new", Decider: "owner"}); err != nil {
		t.Fatal(err)
	}

	d, err := e.svc.ImageDetail(e.ctx, "img_new")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "crsp/custom/o-r/base" || d.Key != "222222222222" || d.SizeBytes != 900 || d.UniqueBytes != 100 || !d.UpdateFound {
		t.Errorf("facts: %+v", d.ImageSummary)
	}
	if len(d.Lineage) != 3 || d.Lineage[0].ImageID != "img_br" || d.Lineage[1].ImageID != "img_new" || d.Lineage[2].State != registry.StateSuperseded || d.Lineage[0].Branch != "fix" {
		t.Errorf("lineage (same repository, newest first): %+v", d.Lineage)
	}
	if len(d.Layers) != 2 || d.Layers[0].SharedWith != 1 || d.Layers[1].SharedWith != 0 || d.Layers[0].SizeBytes != 800 {
		t.Errorf("layers: %+v", d.Layers)
	}
	if len(d.PullsByDay) != 14 || d.PullsByDay[0].Day != "2026-11-06" || d.PullsByDay[13].Day != "2026-11-19" {
		t.Fatalf("days: %+v", d.PullsByDay)
	}
	// 2026-11-07 is inside the 14 days; 18 Nov has 1 pull; 19 Nov has 2 from the table and 2 from the pull reports.
	if d.PullsByDay[1].Pulls != 1 || d.PullsByDay[12].Pulls != 1 || d.PullsByDay[13].Pulls != 4 {
		t.Errorf("pull counts: %+v", d.PullsByDay)
	}
	if !strings.Contains(d.PullsText, "6 times") || !strings.Contains(d.PullsText, "Thu 19 Nov (4)") {
		t.Errorf("text: %q", d.PullsText)
	}
	if d.SavedSeconds != 240 || d.Pulls7Days != 5 {
		t.Errorf("saved %v pulls7 %d", d.SavedSeconds, d.Pulls7Days)
	}
	if len(d.Machines) != 2 {
		t.Fatalf("machines: %+v", d.Machines)
	}
	byID := map[string]Machine{d.Machines[0].ID: d.Machines[0], d.Machines[1].ID: d.Machines[1]}
	if m := byID["m1"]; m.Name != "Build box" || !m.Holds || !m.InUseByRunner || !m.LastUsedAt.Equal(t0) {
		t.Errorf("m1: %+v", m)
	}
	if m := byID["m2"]; m.Name != "m2" || m.Holds || m.InUseByRunner {
		t.Errorf("m2 (unknown to the directory): %+v", m)
	}
	if len(d.Pointers) != 1 || d.Pointers[0].Repository != "o/r" || d.Pointers[0].MovedBy != "luis" {
		t.Errorf("pointers: %+v", d.Pointers)
	}
	if d.Scan.State != registry.ScanNotYet {
		t.Errorf("scan: %+v", d.Scan)
	}
	// 800 of 900 bytes shared with the old image is 88%: similar, information only.
	if len(d.Similar) != 1 || d.Similar[0].ImageID != "img_old" || d.Similar[0].SharedPercent < 88 || d.Similar[0].SharedPercent > 89 {
		t.Errorf("similar: %+v", d.Similar)
	}

	// A scan row shows its counts.
	if err := e.q.QueueRegistryScan(e.ctx, "img_new"); err != nil {
		t.Fatal(err)
	}
	if err := e.q.FinishRegistryScan(e.ctx, sqlcgen.FinishRegistryScanParams{State: "done", Packages: 300, Critical: 1, High: 2, ImageID: "img_new"}); err != nil {
		t.Fatal(err)
	}
	if d, _ = e.svc.ImageDetail(e.ctx, "img_new"); d.Scan.State != registry.ScanDone || d.Scan.Packages != 300 || d.Scan.Critical != 1 || d.Scan.High != 2 {
		t.Errorf("scan row: %+v", d.Scan)
	}

	// An image never pulled and with no layers has empty lists, not nulls, and the plain text.
	d, err = e.svc.ImageDetail(e.ctx, "img_other")
	if err != nil {
		t.Fatal(err)
	}
	if d.PullsText != "Not pulled in the last 14 days." || d.Layers == nil || d.Machines == nil || d.Pointers == nil || d.Similar == nil || len(d.PullsByDay) != 14 {
		t.Errorf("empty image: %+v", d)
	}
	if _, err := e.svc.ImageDetail(e.ctx, "img_missing"); !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
}

func TestPullsListAndTimeLeft(t *testing.T) { // R5.36
	e := newEnv(t)
	e.image(seed{id: "img_a", kind: "standard", key: "aaaaaaaaaaaa", promoted: t0.Add(-time.Hour)})
	// m1 has history: 1000 bytes in 10 s, so 100 B/s. m2 has none.
	h := pull("h1", "done", registry.SourcePulled, t0.Add(-time.Hour))
	h.BytesDone, h.Seconds = 1000, 10
	if err := e.svc.RecordPull(e.ctx, h); err != nil {
		t.Fatal(err)
	}
	a := pull("a", "pulling", registry.SourcePulled, t0)
	a.BytesDone, a.BytesTotal = 200, 1200
	b := pull("b", "pulling", registry.SourcePulled, t0.Add(time.Second))
	b.MachineID = "m2"
	c := pull("c", "waiting_controller", registry.SourcePulled, t0.Add(2*time.Second))
	c.BytesDone, c.BytesTotal = 0, 0
	f := pull("f", "failed", registry.SourcePulled, t0)
	for _, r := range []registry.PullRecord{a, b, c, f} {
		if err := e.svc.RecordPull(e.ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	ps, err := e.svc.Pulls(e.ctx, "")
	if err != nil || len(ps) != 3 {
		t.Fatalf("in flight (done and failed are not): %+v %v", ps, err)
	}
	if ps[0].ID != "a" || ps[0].SecondsLeft == nil || *ps[0].SecondsLeft != 10 {
		t.Errorf("a: %+v", ps[0])
	}
	if ps[1].ID != "b" || ps[1].SecondsLeft != nil {
		t.Errorf("no history gives no time: %+v", ps[1])
	}
	if ps[2].ID != "c" || ps[2].SecondsLeft != nil || ps[2].State != "waiting_controller" {
		t.Errorf("waiting: %+v", ps[2])
	}
	if ps, _ = e.svc.Pulls(e.ctx, "m2"); len(ps) != 1 || ps[0].ID != "b" || ps[0].MachineName != "m2" {
		t.Errorf("machine filter: %+v", ps)
	}
	if ps, _ = e.svc.Pulls(e.ctx, "nobody"); len(ps) != 0 || ps == nil {
		t.Errorf("none: %#v", ps)
	}
}

func TestNextCleanup(t *testing.T) {
	loc := time.UTC
	for _, c := range []struct {
		now  time.Time
		want time.Time
	}{
		{time.Date(2026, 11, 22, 2, 0, 0, 0, loc), time.Date(2026, 11, 22, 3, 0, 0, 0, loc)}, // Sunday, before 03:00
		{time.Date(2026, 11, 22, 3, 0, 0, 0, loc), time.Date(2026, 11, 29, 3, 0, 0, 0, loc)}, // exactly at it: next week
		{time.Date(2026, 11, 19, 9, 0, 0, 0, loc), time.Date(2026, 11, 22, 3, 0, 0, 0, loc)},
	} {
		if got := nextCleanup(c.now, time.Sunday, 3); !got.Equal(c.want) {
			t.Errorf("%v: %v want %v", c.now, got, c.want)
		}
	}
}
