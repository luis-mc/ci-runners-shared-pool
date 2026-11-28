package mirror

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/lancache"
)

func TestHubCardsBudgetAndWarmth(t *testing.T) {
	srv, st := actionsHolder(t)
	n, _ := lancache.Name(lancache.NSActions, "123", "k/v")
	_, _ = st.Put(n, "", strings.NewReader("actions-entry"))
	srv.Stats().Hit("123", time.Unix(10, 0))
	srv.Stats().Miss("123")
	p, _ := lancache.Name(lancache.NSPackages, "luis-mc__site", "npm/tgz/a/a-1.tgz")
	_, _ = st.Put(p, "", strings.NewReader("pkg"))
	proxy := &lancache.Proxy{Store: st}
	proxy.Stats().Hit("luis-mc__site", time.Unix(11, 0))
	proxy.Stats().Served("luis-mc__site", 3)
	g := &Git{Dir: t.TempDir()}
	fakeMirror(t, g, "luis-mc/site", 50, time.Unix(20, 0))
	now := time.Unix(100, 0)
	w := &Warmth{Now: func() time.Time { return now }}
	w.Record("m1", "luis-mc/site", []string{WarmGit, WarmTools})
	w.Record("m2", "luis-mc/other", []string{WarmImage})
	ms := &memSettings{v: Settings{ActionsCache: true, GitMirror: true, PackageProxy: true, BudgetGiB: 5}}
	s := &Service{Settings: ms, Actions: srv, ActStore: st, Git: g, Proxy: proxy, Warmth: w,
		Budget: &Budget{Store: st, Git: g}, RegistrySize: func() int64 { return 7 }}
	h, err := s.Hub(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if h.BudgetBytes != 5<<30 || h.UsedBytes != int64(len("actions-entry")+len("pkg")+50) {
		t.Fatalf("budget %d used %d", h.BudgetBytes, h.UsedBytes)
	}
	if len(h.Cards) != 4 {
		t.Fatalf("cards %+v", h.Cards)
	}
	by := map[string]HubCard{}
	for _, c := range h.Cards {
		by[c.Kind] = c
	}
	if a := by[CardActions]; !a.On || a.Entries != 1 || a.Hits != 1 || a.Misses != 1 || a.Status != StatusWaiting || a.WipeID != "mirror:actions_all" {
		t.Fatalf("actions %+v", a)
	}
	if g := by[CardGit]; !g.On || g.Entries != 1 || g.Size != 50 || g.WipeID != "mirror:git_all" {
		t.Fatalf("git %+v", g)
	}
	if r := by[CardRegistry]; r.On || r.Size != 7 {
		t.Fatalf("registry %+v", r)
	}
	if p := by[CardPackages]; !p.On || p.Entries != 1 || p.Hits != 1 || p.Served != 3 || p.WipeID != "mirror:packages" {
		t.Fatalf("packages %+v", p)
	}
	if h.Hits != 2 || h.Misses != 1 || h.WarmMachines != 2 {
		t.Fatalf("hits %d misses %d warm %d", h.Hits, h.Misses, h.WarmMachines)
	}
	rows := map[string]HubRepo{}
	for _, r := range h.Repos {
		rows[r.Repo] = r
	}
	site := rows["luis-mc/site"]
	if site.Warmth != 50 || site.Hits != 1 || site.Size != 50+3 {
		t.Fatalf("site row %+v (all %+v)", site, h.Repos)
	}
	if rows["luis-mc/other"].Warmth != 25 || rows["123"].Misses != 1 {
		t.Fatalf("rows %+v", h.Repos)
	}
}
