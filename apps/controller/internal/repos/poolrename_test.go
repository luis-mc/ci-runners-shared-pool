package repos

import (
	"context"
	"strings"
	"testing"
)

// CRSP400: the pool rename writes the variables with the same writer as the
// restore and Repair, so each repository carries the label of the new name.
func TestSyncPoolVariablesWritesTheNewPoolLabel(t *testing.T) {
	e := newEnv(t)
	e.world()
	e.turnOn(101)
	ctx := context.Background()
	v, _ := e.fake.Variable("alice/secret", "CRSP_RUNS_ON_LINUX")
	if !strings.Contains(v, "crsp-home") {
		t.Fatalf("starts with the old label: %s", v)
	}
	repos, err := e.svc.PoolOnRepositories(ctx)
	if err != nil || len(repos) != 1 || repos[0].ID != 101 {
		t.Fatalf("pool-on repositories %v %v", repos, err)
	}
	e.cap.m = map[string][]string{"Linux": {"X64", "docker", "crsp-lab"}} // the name in force changed
	if err := e.svc.SyncPoolVariables(ctx, 101, "luis"); err != nil {
		t.Fatal(err)
	}
	v, _ = e.fake.Variable("alice/secret", "CRSP_RUNS_ON_LINUX")
	if !strings.Contains(v, "crsp-lab") || strings.Contains(v, "crsp-home") {
		t.Fatalf("the variable names the new label only: %s", v)
	}
	// Again changes nothing and fails nothing.
	if err := e.svc.SyncPoolVariables(ctx, 101, "luis"); err != nil {
		t.Fatal(err)
	}
	// A repository that left the pool is left alone.
	e.fake.SetVariable("alice/secret", "CRSP_RUNS_ON_LINUX", `["ubuntu-latest"]`)
	_ = e.store.SetRepositoryPool(ctx, 101, false, "luis", e.clock())
	if err := e.svc.SyncPoolVariables(ctx, 101, "luis"); err != nil {
		t.Fatal(err)
	}
	if v, _ := e.fake.Variable("alice/secret", "CRSP_RUNS_ON_LINUX"); !strings.Contains(v, "ubuntu-latest") {
		t.Fatalf("not touched: %s", v)
	}
}
