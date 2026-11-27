package api

import (
	"context"
	"encoding/json"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/caches"
)

type fakeCaches struct {
	list    []caches.Cache
	err     error
	actor   audit.Actor
	confirm bool
	machine string
	kind    string
}

func (f *fakeCaches) List(_ context.Context, machineID, kind string) ([]caches.Cache, error) {
	f.machine, f.kind = machineID, kind
	return f.list, f.err
}

func (f *fakeCaches) BeginWipe(_ context.Context, actor audit.Actor, id string, confirm bool) (caches.Cache, error) {
	f.actor, f.confirm = actor, confirm
	if f.err != nil {
		return caches.Cache{}, f.err
	}
	for _, c := range f.list {
		if c.ID == id {
			if !confirm {
				return caches.Cache{}, &caches.NeedsConfirmation{Items: []caches.ConfirmItem{
					{Kind: "cache", Text: "the tool cache of runner r1 on build-1, 3.0 GiB"}, {Kind: "job", Text: "waits for o/r, run 9, job test"}}}
			}
			c.State = caches.StateWiping
			return c, nil
		}
	}
	return caches.Cache{}, caches.ErrNotFound
}

func cachesMux(svc CachesService) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Caches: svc, Authn: asRole(auth.RoleOwner)})
	return mux
}

func oneCache() *fakeCaches {
	return &fakeCaches{list: []caches.Cache{{
		ID: "cch_m1_r1_tool_cache", Kind: caches.KindTool, MachineID: "m1", RunnerName: "r1", Size: 3 << 30, Cap: 20 << 30, Entries: 4,
		State: caches.StateReady, LastWipedAt: time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC),
	}, {
		ID: "cch_m1_r1_rust_cache", Kind: caches.KindRust, MachineID: "m1", RunnerName: "r1", Size: 10, State: caches.StateWiping, WaitingFor: []string{"o/r, run 9, job test"},
	}, {
		ID: "cch_m1_r2_rust_cache", Kind: caches.KindRust, MachineID: "m1", RunnerName: "r2", State: caches.StateReady, Error: "the folder could not be emptied",
	}}}
}

// Web-console R5.25: the list gives each cache with its size against its cap.
func TestListCachesGivesSizeAgainstCap(t *testing.T) {
	f := oneCache()
	rec := do(cachesMux(f), http.MethodGet, "/api/v1/caches?machine_id=m1&kind=tool_cache", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if f.machine != "m1" || f.kind != "tool_cache" {
		t.Fatalf("filters not passed: %q %q", f.machine, f.kind)
	}
	var out struct {
		Items []struct {
			ID          string   `json:"id"`
			Kind        string   `json:"kind"`
			RunnerName  string   `json:"runner_name"`
			SizeBytes   int64    `json:"size_bytes"`
			CapBytes    *int64   `json:"cap_bytes"`
			EntryCount  int      `json:"entry_count"`
			LastWipedAt string   `json:"last_wiped_at"`
			State       string   `json:"state"`
			WaitingFor  []string `json:"waiting_for"`
			Error       string   `json:"error"`
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 3 {
		t.Fatalf("%s", rec.Body)
	}
	a, b, c := out.Items[0], out.Items[1], out.Items[2]
	if a.SizeBytes != 3<<30 || a.CapBytes == nil || *a.CapBytes != 20<<30 || a.EntryCount != 4 || a.RunnerName != "r1" || a.LastWipedAt == "" {
		t.Errorf("tool cache: %+v", a)
	}
	if b.CapBytes != nil || b.State != "wiping" || len(b.WaitingFor) != 1 {
		t.Errorf("a cache with no cap omits it and a wiping one names its jobs: %+v", b)
	}
	if c.Error == "" {
		t.Errorf("the error of the last wipe is shown: %+v", c)
	}
}

func TestListCachesEmptyIsAnEmptyListNotNull(t *testing.T) {
	rec := do(cachesMux(&fakeCaches{}), http.MethodGet, "/api/v1/caches", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestCachesAnswer501WithoutAService(t *testing.T) {
	for _, r := range [][2]string{{http.MethodGet, "/api/v1/caches"}, {http.MethodPost, "/api/v1/caches/x/wipe"}, {http.MethodGet, "/api/v1/images"}} {
		if rec := do(cachesMux(nil), r[0], r[1], ""); rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s: %d", r[0], r[1], rec.Code)
		}
	}
}

// R5.30: without confirm the wipe answers 409 needs_confirmation and names the
// cache with its size and the jobs it waits for; with it the answer is 202.
func TestWipeNeedsConfirmationThenAccepts(t *testing.T) {
	f := oneCache()
	rec := do(cachesMux(f), http.MethodPost, "/api/v1/caches/cch_m1_r1_tool_cache/wipe", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Details []struct{ Kind, Text string }
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != CodeNeedsConfirmation || len(env.Error.Details) != 2 || env.Error.Details[0].Kind != "cache" ||
		!strings.Contains(env.Error.Details[0].Text, "3.0 GiB") || env.Error.Details[1].Kind != "job" {
		t.Fatalf("%s", rec.Body)
	}
	if f.confirm {
		t.Fatal("no yes was given")
	}
	rec = do(cachesMux(f), http.MethodPost, "/api/v1/caches/cch_m1_r1_tool_cache/wipe?confirm=true", "")
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"state":"wiping"`) || !f.confirm {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if f.actor.Login == "" {
		t.Fatal("the actor reaches the audit")
	}
}

func TestWipeErrorsMapToTheEnvelope(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{caches.ErrNotFound, 404, CodeNotFound},
		{&caches.RefusalError{Reason: "build-1 is offline; the wipe needs its agent"}, 409, CodeConflict},
		{context.DeadlineExceeded, 500, CodeInternal},
	}
	for _, c := range cases {
		rec := do(cachesMux(&fakeCaches{err: c.err}), http.MethodPost, "/api/v1/caches/x/wipe?confirm=true", "")
		if rec.Code != c.status || !strings.Contains(rec.Body.String(), `"code":"`+c.code+`"`) {
			t.Errorf("%v: %d %s", c.err, rec.Code, rec.Body)
		}
	}
	if rec := do(cachesMux(&fakeCaches{err: context.Canceled}), http.MethodGet, "/api/v1/caches", ""); rec.Code != 500 {
		t.Errorf("list failure: %d", rec.Code)
	}
}

// Runners R5.32: with no machine that can run container jobs the list is
// empty, and an image id names nothing.
func TestImagesAreEmptyWithoutMachines(t *testing.T) {
	mux := cachesMux(&fakeCaches{})
	rec := do(mux, http.MethodGet, "/api/v1/images?os=linux", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	for _, r := range [][2]string{
		{http.MethodGet, "/api/v1/images/img_1"},
		{http.MethodPost, "/api/v1/images/img_1/rebuild?confirm=true"},
	} {
		rec := do(mux, r[0], r[1], `{"enabled_layers":["kvm"]}`)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"code":"not_found"`) {
			t.Errorf("%s %s: %d %s", r[0], r[1], rec.Code, rec.Body)
		}
	}
}
