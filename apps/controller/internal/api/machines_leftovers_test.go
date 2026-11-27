package api

import (
	"context"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/leftovers"
)

type apiLeftoverSource struct{ results []leftovers.Result }

func (apiLeftoverSource) Scan(context.Context, string, []leftovers.Owned) (leftovers.Scan, error) {
	return leftovers.Scan{Admin: true, Items: []leftovers.Item{
		{Kind: leftovers.KindService, Name: "actions.runner.old"},
		{Kind: leftovers.KindFolder, Name: `C:\crsp\runners\old`},
	}}, nil
}

func (s apiLeftoverSource) Remove(context.Context, string, []leftovers.Owned, []leftovers.Ref) ([]leftovers.Result, error) {
	return s.results, nil
}

func TestRemoveRunnerLeftoversRoute(t *testing.T) {
	mux, repo, id, svc := machinesMuxSvc(t)
	ctx := context.Background()
	path := "/api/v1/machines/" + id + "/runner-leftovers/remove"
	item := func(kind, name string) map[string]any { return map[string]any{"kind": kind, "name": name} }
	body := func(items ...map[string]any) map[string]any { return map[string]any{"items": items} }

	// Offline: 409. Online without a source: 501. Empty list: 400.
	if rec := doM(t, mux, "POST", path, body(item("service", "x"))); rec.Code != 409 {
		t.Fatalf("offline: %d %s", rec.Code, rec.Body)
	}
	m, _ := repo.GetMachine(ctx, id)
	m.Online = true
	_ = repo.UpdateMachine(ctx, m)
	if rec := doM(t, mux, "POST", path, body(item("service", "x"))); rec.Code != 501 {
		t.Fatalf("no source: %d %s", rec.Code, rec.Body)
	}
	src := apiLeftoverSource{results: []leftovers.Result{
		{Kind: leftovers.KindService, Name: "actions.runner.old", Outcome: leftovers.Removed, Steps: []string{"stopped the service"}},
		{Kind: leftovers.KindFolder, Name: `C:\crsp\runners\old`, Outcome: leftovers.Failed, Error: "a file is in use"},
	}}
	svc.SetLeftoverSources(nil, src)
	svc.SetOwnedRunners(func(context.Context, string) ([]leftovers.Owned, error) { return nil, nil })
	if rec := doM(t, mux, "POST", path, body()); rec.Code != 400 {
		t.Fatalf("empty: %d %s", rec.Code, rec.Body)
	}
	// An item the last scan never showed is refused (409), nothing runs.
	if rec := doM(t, mux, "POST", path, body(item("service", "actions.runner.old"))); rec.Code != 409 || !strings.Contains(rec.Body.String(), "scan again") {
		t.Fatalf("not scanned: %d %s", rec.Code, rec.Body)
	}
	if _, err := svc.RunnerLeftovers(ctx, id); err != nil {
		t.Fatal(err)
	}
	if rec := doM(t, mux, "POST", path, body(item("service", "actions.runner.old"), item("folder", `C:\Windows`))); rec.Code != 409 || !strings.Contains(rec.Body.String(), `C:\\Windows`) {
		t.Fatalf("one item not in scan: %d %s", rec.Code, rec.Body)
	}
	// 200 with one removed and one failed item.
	rec := doM(t, mux, "POST", path, body(item("service", "actions.runner.old"), item("folder", `C:\crsp\runners\old`)))
	got := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(got, `"outcome":"removed"`) || !strings.Contains(got, `"outcome":"failed"`) ||
		!strings.Contains(got, `"error":"a file is in use"`) || !strings.Contains(got, `"steps":["stopped the service"]`) || !strings.Contains(got, `"steps":[]`) {
		t.Fatalf("results: %d %s", rec.Code, got)
	}
	// Unknown machine: 404.
	if rec := doM(t, mux, "POST", "/api/v1/machines/m_nope/runner-leftovers/remove", body(item("service", "x"))); rec.Code != 404 {
		t.Fatalf("unknown machine: %d %s", rec.Code, rec.Body)
	}
}
