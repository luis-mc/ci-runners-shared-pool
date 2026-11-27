package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/records"
)

var regT0 = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

type fakeRegistryRead struct {
	err      error
	overview records.Overview
	images   []records.ImageSummary
	detail   map[string]records.ImageDetail
	pulls    []records.Pull
	gotF     records.ImageFilter
	gotM     string
}

func (f *fakeRegistryRead) Overview(context.Context) (records.Overview, error) {
	return f.overview, f.err
}

func (f *fakeRegistryRead) Images(_ context.Context, fl records.ImageFilter) ([]records.ImageSummary, error) {
	f.gotF = fl
	return f.images, f.err
}

func (f *fakeRegistryRead) ImageDetail(_ context.Context, id string) (records.ImageDetail, error) {
	if f.err != nil {
		return records.ImageDetail{}, f.err
	}
	d, ok := f.detail[id]
	if !ok {
		return records.ImageDetail{}, registry.ErrNotFound
	}
	return d, nil
}

func (f *fakeRegistryRead) Pulls(_ context.Context, m string) ([]records.Pull, error) {
	f.gotM = m
	return f.pulls, f.err
}

func registryReadMux(svc RegistryReadService) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		RegistryRead: svc, Authn: asRole(auth.RoleOwner)})
	return mux
}

func regImage(id, key string) records.ImageSummary {
	return records.ImageSummary{Image: registry.Image{ID: id, Kind: registry.KindCustom, Name: "crsp/custom/o-r/node", Key: registry.Key(key),
		Profile: "node", Arch: "amd64", State: registry.StateCurrent, Manifest: "sha256:" + strings.Repeat("a", 64),
		SourceRepository: "o/r", SizeBytes: 900, UniqueBytes: 100, PushedAt: regT0, Scan: registry.ScanNotYet}, Pulls7Days: 3}
}

func TestRegistryReadRoutesAnswer501WithoutService(t *testing.T) {
	mux := registryReadMux(nil)
	for _, p := range []string{"/api/v1/registry/overview", "/api/v1/registry/images", "/api/v1/registry/images/img_1", "/api/v1/registry/pulls"} {
		rec := do(mux, http.MethodGet, p, "")
		if rec.Code != http.StatusNotImplemented || !strings.Contains(rec.Body.String(), CodeNotImplemented) {
			t.Errorf("%s: %d %s", p, rec.Code, rec.Body)
		}
	}
}

func TestGetRegistryOverview(t *testing.T) { // R5.33
	f := &fakeRegistryRead{overview: records.Overview{
		Where: records.Where{Host: "10.0.0.2:8443", Folder: "/data/registry", Running: true},
		Budgets: []records.Budget{
			{Budget: registry.Budget{Kind: registry.KindStandard, LimitBytes: 40, UsedBytes: 50, SharedBytes: 5}, Over: true},
			{Budget: registry.Budget{Kind: registry.KindCustom, LimitBytes: 30, UsedBytes: 1}}},
		Week:     records.Week{Pulls: 12, BuildsAvoid: 11, SavedSeconds: 3600, BytesPulled: 9_000},
		Standard: 2, Custom: 3, Builds: 1, Pulls: 2, Pushes: 3,
		Recommendations: []registry.Recommendation{
			{ID: "r1", Rule: registry.RuleReplace, ImageID: "img_n", OtherID: "img_o", Decider: registry.DecidedByOwner, FreesBytes: 7, CreatedAt: regT0},
			{ID: "r2", Rule: registry.RuleOverBudget, Kind: registry.KindStandard, Decider: registry.DecidedByOwner, CreatedAt: regT0},
			{ID: "r3", Rule: registry.RuleVariant, ImageID: "img_v", Decider: registry.DecidedByCrsp, DueAt: regT0.Add(48 * time.Hour), CreatedAt: regT0}},
		LastCleanup:   &records.Cleanup{ID: 4, Trigger: "weekly", State: "done", FreedBytes: 99, StartedAt: regT0, FinishedAt: regT0.Add(time.Minute)},
		NextCleanupAt: regT0.Add(72 * time.Hour),
	}}
	rec := do(registryReadMux(f), http.MethodGet, "/api/v1/registry/overview", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var ov gen.RegistryOverview
	if err := json.Unmarshal(rec.Body.Bytes(), &ov); err != nil {
		t.Fatal(err)
	}
	if ov.Where.Host != "10.0.0.2:8443" || !ov.Where.Running || ov.Where.ReadOnly || ov.Where.WhyNot != nil {
		t.Errorf("where: %+v", ov.Where)
	}
	if len(ov.Budgets) != 2 || !ov.Budgets[0].Over || ov.Budgets[0].SharedBytes != 5 || ov.Budgets[1].Over || ov.Budgets[1].Kind != gen.RegistryImageKind("custom") {
		t.Errorf("budgets: %+v", ov.Budgets)
	}
	if ov.Last7Days.Pulls != 12 || ov.Last7Days.BuildsAvoided != 11 || ov.Last7Days.SavedSeconds != 3600 || ov.Last7Days.BytesPulled != 9000 {
		t.Errorf("week: %+v", ov.Last7Days)
	}
	if ov.Images.Standard != 2 || ov.Images.Custom != 3 || ov.InFlight.Builds != 1 || ov.InFlight.Pulls != 2 || ov.InFlight.Pushes != 3 {
		t.Errorf("counts: %+v %+v", ov.Images, ov.InFlight)
	}
	if len(ov.Recommendations) != 3 || ov.Recommendations[0].OtherImageId == nil || *ov.Recommendations[0].OtherImageId != "img_o" ||
		ov.Recommendations[0].DueAt != nil || ov.Recommendations[1].Kind == nil || ov.Recommendations[2].DueAt == nil {
		t.Errorf("recommendations: %+v", ov.Recommendations)
	}
	if ov.LastCleanup == nil || ov.LastCleanup.FreedBytes != 99 || ov.LastCleanup.FinishedAt == nil || ov.NextCleanupAt == nil {
		t.Errorf("cleanup: %+v %v", ov.LastCleanup, ov.NextCleanupAt)
	}

	// Not running: the reason shows; empty lists are arrays, never null; no cleanup yet.
	f.overview = records.Overview{Where: records.Where{WhyNot: "No address."}}
	body := do(registryReadMux(f), http.MethodGet, "/api/v1/registry/overview", "").Body.String()
	for _, want := range []string{`"why_not":"No address."`, `"budgets":[]`, `"recommendations":[]`} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %s: %s", want, body)
		}
	}
	if strings.Contains(body, "last_cleanup") || strings.Contains(body, "next_cleanup_at") {
		t.Errorf("absent fields are omitted: %s", body)
	}
}

func TestListRegistryImages(t *testing.T) { // R5.34
	upd := regImage("img_2", "222222222222")
	upd.UpdateFound, upd.KeepUntil = true, regT0.Add(time.Hour)
	f := &fakeRegistryRead{images: []records.ImageSummary{regImage("img_1", "111111111111"), upd}}
	rec := do(registryReadMux(f), http.MethodGet, "/api/v1/registry/images?kind=custom&state=current&repository=o/r&arch=amd64&profile=node", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	want := records.ImageFilter{Kind: registry.KindCustom, States: []registry.State{registry.StateCurrent}, Repository: "o/r", Arch: "amd64", Profile: "node"}
	if f.gotF.Kind != want.Kind || len(f.gotF.States) != 1 || f.gotF.States[0] != want.States[0] || f.gotF.Repository != "o/r" || f.gotF.Arch != "amd64" || f.gotF.Profile != "node" {
		t.Errorf("filter: %+v", f.gotF)
	}
	var list gen.RegistryImageList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 || list.Items[0].Key != "111111111111" || list.Items[0].Pulls7Days != 3 || list.Items[0].UpdateFound != nil ||
		list.Items[1].UpdateFound == nil || !*list.Items[1].UpdateFound || list.Items[1].KeepUntil == nil || list.Items[0].SizeBytes != 900 {
		t.Errorf("items: %+v", list.Items)
	}

	// No filter and no images: an empty array.
	f2 := &fakeRegistryRead{}
	rec = do(registryReadMux(f2), http.MethodGet, "/api/v1/registry/images", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"items":[]`) || len(f2.gotF.States) != 0 || f2.gotF.Kind != "" {
		t.Errorf("empty: %d %s %+v", rec.Code, rec.Body, f2.gotF)
	}
	// An unknown kind is refused by the contract.
	if rec := do(registryReadMux(f), http.MethodGet, "/api/v1/registry/images?kind=weird", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("bad kind: %d", rec.Code)
	}
}

func TestGetRegistryImage(t *testing.T) { // R5.35
	d := records.ImageDetail{ImageSummary: regImage("img_1", "111111111111")}
	d.StackHash, d.BaseName, d.Commit, d.PushedBy, d.Replaces = strings.Repeat("a", 64), "ubuntu:24.04", "abc123", "m1", "img_0"
	d.Lineage = []records.LineageEntry{{ImageID: "img_1", Key: "111111111111", State: registry.StateCurrent, PushedAt: regT0},
		{ImageID: "img_0", Key: "000000000000", State: registry.StateSuperseded, Branch: "fix", PushedAt: regT0.Add(-time.Hour)}}
	d.Layers = []records.Layer{{Layer: registry.Layer{Digest: "sha256:" + strings.Repeat("1", 64), SizeBytes: 800, MediaType: "x/y"}, SharedWith: 2}}
	d.PullsByDay = []records.PullDay{{Day: "2026-11-18", Pulls: 0}, {Day: "2026-11-19", Pulls: 4}}
	d.PullsText, d.SavedSeconds = "Pulled 4 times in the last 14 days; most on Thu 19 Nov (4).", 240
	d.Machines = []records.Machine{{ID: "m1", Name: "Build box", Holds: true, InUseByRunner: true, LastUsedAt: regT0}, {ID: "m2", Name: "m2"}}
	d.Pointers = []registry.Pointer{{Repository: "o/r", Profile: "node", Arch: "amd64", ImageID: "img_1", Previous: "img_0", MovedAt: regT0, MovedBy: "luis"}}
	d.Scan = records.Scan{State: registry.ScanDone, Packages: 300, Critical: 1, High: 2, ScannedAt: regT0}
	d.Similar = []records.Similar{{ImageID: "img_0", SharedPercent: 88.5}}
	f := &fakeRegistryRead{detail: map[string]records.ImageDetail{"img_1": d}}
	mux := registryReadMux(f)

	rec := do(mux, http.MethodGet, "/api/v1/registry/images/img_1", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var im gen.RegistryImage
	if err := json.Unmarshal(rec.Body.Bytes(), &im); err != nil {
		t.Fatal(err)
	}
	if im.Id != "img_1" || im.Name != "crsp/custom/o-r/node" || im.Pulls7Days != 3 || im.StackHash == nil || *im.Commit != "abc123" || *im.Replaces != "img_0" || *im.BaseName != "ubuntu:24.04" {
		t.Errorf("facts: %+v", im)
	}
	if len(im.Lineage) != 2 || *im.Lineage[1].Branch != "fix" || im.Lineage[1].State != gen.RegistryImageStateSuperseded {
		t.Errorf("lineage: %+v", im.Lineage)
	}
	if len(im.Layers) != 1 || im.Layers[0].SharedWith != 2 || *im.Layers[0].MediaType != "x/y" {
		t.Errorf("layers: %+v", im.Layers)
	}
	if len(im.PullsByDay) != 2 || im.PullsByDay[1].Day.String() != "2026-11-19" || im.PullsByDay[1].Pulls != 4 || im.PullsText == nil || !strings.Contains(*im.PullsText, "Pulled 4 times") || *im.SavedSeconds != 240 {
		t.Errorf("pulls: %+v", im.PullsByDay)
	}
	if len(im.Machines) != 2 || !im.Machines[0].Holds || im.Machines[0].InUseByRunner == nil || im.Machines[1].InUseByRunner != nil || im.Machines[0].MachineName != "Build box" {
		t.Errorf("machines: %+v", im.Machines)
	}
	if len(im.Pointers) != 1 || *im.Pointers[0].PreviousImageId != "img_0" || im.Pointers[0].MovedBy != "luis" {
		t.Errorf("pointers: %+v", im.Pointers)
	}
	if im.Scan == nil || im.Scan.State != gen.RegistryScanStateDone || *im.Scan.Critical != 1 || *im.Scan.Packages != 300 {
		t.Errorf("scan: %+v", im.Scan)
	}
	if im.Similar == nil || len(*im.Similar) != 1 || (*im.Similar)[0].SharedPercent != 88.5 {
		t.Errorf("similar: %+v", im.Similar)
	}

	// Not scanned: a state and no counts. Empty lists stay arrays.
	bare := records.ImageDetail{ImageSummary: regImage("img_b", "bbbbbbbbbbbb"), Scan: records.Scan{State: registry.ScanNotYet}}
	f.detail["img_b"] = bare
	body := do(mux, http.MethodGet, "/api/v1/registry/images/img_b", "").Body.String()
	for _, want := range []string{`"lineage":[]`, `"layers":[]`, `"pulls_by_day":[]`, `"machines":[]`, `"pointers":[]`, `"scan":{"state":"not_scanned"}`} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %s: %s", want, body)
		}
	}

	rec = do(mux, http.MethodGet, "/api/v1/registry/images/img_none", "")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), CodeNotFound) {
		t.Errorf("missing: %d %s", rec.Code, rec.Body)
	}
}

func TestRegistryReadErrorsDoNotLeak(t *testing.T) {
	f := &fakeRegistryRead{err: errors.New("disk /secret/path failed")}
	mux := registryReadMux(f)
	for _, p := range []string{"/api/v1/registry/overview", "/api/v1/registry/images", "/api/v1/registry/images/img_1", "/api/v1/registry/pulls"} {
		rec := do(mux, http.MethodGet, p, "")
		if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "secret") || !strings.Contains(rec.Body.String(), CodeInternal) {
			t.Errorf("%s: %d %s", p, rec.Code, rec.Body)
		}
	}
}

func TestListRegistryPulls(t *testing.T) { // R5.36
	left := 42.0
	f := &fakeRegistryRead{pulls: []records.Pull{
		{ID: "p1", MachineID: "m1", MachineName: "Build box", Key: "aaaaaaaaaaaa", ImageID: "img_a", Repository: "o/r", Source: registry.SourcePulled,
			State: "pulling", BytesDone: 10, BytesTotal: 100, SecondsLeft: &left, StartedAt: regT0},
		{ID: "p2", MachineID: "m2", Key: "aaaaaaaaaaaa", Source: registry.SourcePulled, State: "waiting_controller", StartedAt: regT0}}}
	mux := registryReadMux(f)
	rec := do(mux, http.MethodGet, "/api/v1/registry/pulls?machine_id=m1", "")
	if rec.Code != 200 || f.gotM != "m1" {
		t.Fatalf("%d %s machine %q", rec.Code, rec.Body, f.gotM)
	}
	var list gen.RegistryPullList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("items: %+v", list.Items)
	}
	if p := list.Items[0]; p.Id != "p1" || p.SecondsLeft == nil || *p.SecondsLeft != 42 || p.State != gen.RegistryPullStatePulling || *p.MachineName != "Build box" || *p.ImageId != "img_a" || p.BytesTotal != 100 {
		t.Errorf("p1: %+v", p)
	}
	// No history: the time left is absent, not zero (UX-29).
	if p := list.Items[1]; p.SecondsLeft != nil || p.State != gen.RegistryPullStateWaitingController || p.MachineName != nil {
		t.Errorf("p2: %+v", p)
	}
	if body := do(registryReadMux(&fakeRegistryRead{}), http.MethodGet, "/api/v1/registry/pulls", "").Body.String(); !strings.Contains(body, `"items":[]`) {
		t.Errorf("empty: %s", body)
	}
}
