package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/mirror"
)

type fakeHub struct {
	cur   mirror.Settings
	put   *mirror.Settings
	actor audit.Actor
}

func (f *fakeHub) Hub(context.Context) (mirror.Hub, error) {
	return mirror.Hub{Settings: f.cur, BudgetBytes: 60 << 30, UsedBytes: 5 << 30, Hits: 9, Misses: 3, Served: 100, WarmMachines: 2,
		Cards: []mirror.HubCard{
			{Kind: mirror.CardActions, On: true, Status: mirror.StatusWaiting, Note: "the receiver does not listen on the LAN yet", WipeID: "mirror:actions_all"},
			{Kind: mirror.CardPackages, On: true, Size: 7, Entries: 2, Hits: 9, LastUsed: time.Unix(100, 0), Status: mirror.StatusOK, WipeID: "mirror:packages"},
		},
		Repos: []mirror.HubRepo{{Repo: "o/r", Hits: 9, Misses: 3, Size: 7, Warmth: 50}},
	}, nil
}

func (f *fakeHub) Get(context.Context) (mirror.Settings, error) { return f.cur, nil }

func (f *fakeHub) Put(_ context.Context, actor audit.Actor, _ audit.Recorder, v mirror.Settings) (mirror.Settings, error) {
	f.put, f.actor = &v, actor
	f.cur = v
	return v, nil
}

func hubMux(svc CacheHubService, role auth.Role) *http.ServeMux {
	mux := http.NewServeMux()
	o := Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Authn: asRole(role)}
	if svc != nil {
		o.CacheHub = svc
	}
	Register(mux, o)
	return mux
}

// Caches R5.77: the hub route gives the budget, one card per kind with its
// wipe id, and warmth by repository; nil answers 501.
func TestCacheHubRoute(t *testing.T) {
	f := &fakeHub{cur: mirror.Settings{ActionsCache: true, PackageProxy: true, BudgetGiB: 60, GitRefreshMin: 15, WarmthHours: 24}}
	rec := do(hubMux(f, auth.RoleViewer), http.MethodGet, "/api/v1/caches/hub", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var out struct {
		Settings struct {
			BudgetGib    int  `json:"budget_gib"`
			PackageProxy bool `json:"package_proxy"`
		}
		BudgetBytes int64 `json:"budget_bytes"`
		UsedBytes   int64 `json:"used_bytes"`
		Cards       []struct {
			Kind       string  `json:"kind"`
			On         bool    `json:"on"`
			Status     string  `json:"status"`
			Note       *string `json:"note"`
			WipeID     string  `json:"wipe_id"`
			LastUsedAt *string `json:"last_used_at"`
		}
		Repos []struct {
			Repo   string `json:"repo"`
			Warmth int    `json:"warmth"`
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.BudgetBytes != 60<<30 || out.UsedBytes != 5<<30 || out.Settings.BudgetGib != 60 || !out.Settings.PackageProxy {
		t.Fatalf("%s", rec.Body)
	}
	if len(out.Cards) != 2 || out.Cards[0].Status != "waiting" || out.Cards[0].Note == nil || out.Cards[0].LastUsedAt != nil ||
		out.Cards[1].WipeID != "mirror:packages" || out.Cards[1].LastUsedAt == nil {
		t.Fatalf("cards %s", rec.Body)
	}
	if len(out.Repos) != 1 || out.Repos[0].Repo != "o/r" || out.Repos[0].Warmth != 50 {
		t.Fatalf("repos %s", rec.Body)
	}
	for _, r := range [][2]string{{http.MethodGet, "/api/v1/caches/hub"}, {http.MethodPut, "/api/v1/caches/settings"}} {
		if rec := do(hubMux(nil, auth.RoleOwner), r[0], r[1], `{"actions_cache":true,"git_mirror":false,"registry_mirror":false,"package_proxy":false,"budget_gib":60}`); rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s: %d", r[0], r[1], rec.Code)
		}
	}
}

// Caches R5.73, R5.77: an admin changes the switches and the budget; numbers
// left out keep what is stored; a bad budget is 400 and a viewer is refused.
func TestPutCacheSettingsValidatesAndAudits(t *testing.T) {
	f := &fakeHub{cur: mirror.Settings{GitRefreshMin: 30, WarmthHours: 48, BudgetGiB: 60}}
	body := `{"actions_cache":true,"git_mirror":true,"registry_mirror":false,"package_proxy":true,"budget_gib":40}`
	rec := do(hubMux(f, auth.RoleAdmin), http.MethodPut, "/api/v1/caches/settings", body)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"budget_gib":40`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if f.put == nil || !f.put.PackageProxy || f.put.BudgetGiB != 40 || f.put.GitRefreshMin != 30 || f.put.WarmthHours != 48 || f.actor.Login == "" {
		t.Fatalf("put %+v actor %+v", f.put, f.actor)
	}
	for _, bad := range []string{
		`{"actions_cache":true,"git_mirror":true,"registry_mirror":false,"package_proxy":true,"budget_gib":0}`,
		`{"actions_cache":true,"git_mirror":true,"registry_mirror":false,"package_proxy":true,"budget_gib":5000}`,
		`{"actions_cache":true,"git_mirror":true,"registry_mirror":false,"package_proxy":true,"budget_gib":10,"warmth_hours":9999}`,
	} {
		f.put = nil
		if rec := do(hubMux(f, auth.RoleAdmin), http.MethodPut, "/api/v1/caches/settings", bad); rec.Code != http.StatusBadRequest || f.put != nil {
			t.Errorf("%s: %d %s", bad, rec.Code, rec.Body)
		}
	}
	if rec := do(hubMux(f, auth.RoleViewer), http.MethodPut, "/api/v1/caches/settings", body); rec.Code != http.StatusForbidden {
		t.Errorf("a viewer changed the settings: %d", rec.Code)
	}
}
