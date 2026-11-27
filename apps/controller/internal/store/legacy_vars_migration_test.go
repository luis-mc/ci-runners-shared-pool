package store

import (
	"context"
	"encoding/json"
	"io/fs"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestIntegrationDropLegacyVarsMigration proves migration 43 removes the
// legacy_vars setting from every saved policy and keeps the rest of the
// repository state (CRSP229, github-integration R5.39 removed).
func TestIntegrationDropLegacyVarsMigration(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, s.db, sub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 42); err != nil {
		t.Fatalf("down to 42: %v", err)
	}
	const body = `{"installations":[],"repositories":[{"ID":100}],
		"policies":{"100":{"Priority":5,"Labels":["linux"],"LegacyVars":true},"101":{"Priority":0,"LegacyVars":false},"102":{"Priority":1}},
		"variables":[{"RepositoryID":100,"Name":"RUNS_ON_LINUX"}]}`
	if err := s.ReposState().SaveReposState(ctx, []byte(body)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 43); err != nil {
		t.Fatalf("up to 43: %v", err)
	}
	got, err := s.ReposState().LoadReposState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var sn struct {
		Repos    []map[string]any          `json:"repositories"`
		Policies map[string]map[string]any `json:"policies"`
		Vars     []map[string]any          `json:"variables"`
	}
	if err := json.Unmarshal(got, &sn); err != nil {
		t.Fatalf("%v: %s", err, got)
	}
	if len(sn.Policies) != 3 || len(sn.Repos) != 1 || len(sn.Vars) != 1 {
		t.Fatalf("state changed: %s", got)
	}
	for id, pol := range sn.Policies {
		if _, ok := pol["LegacyVars"]; ok {
			t.Fatalf("policy %s still has legacy_vars: %s", id, got)
		}
		if _, ok := pol["Priority"]; !ok {
			t.Fatalf("policy %s lost its fields: %s", id, got)
		}
	}
	if sn.Policies["100"]["Priority"] != float64(5) || len(sn.Policies["100"]["Labels"].([]any)) != 1 {
		t.Fatalf("policy 100: %v", sn.Policies["100"])
	}
	// A state with no policies, and down and up again, work too.
	if _, err := p.DownTo(ctx, 42); err != nil {
		t.Fatalf("down again: %v", err)
	}
	if err := s.ReposState().SaveReposState(ctx, []byte(`{"installations":[]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if got, _ := s.ReposState().LoadReposState(ctx); string(got) != `{"installations":[]}` {
		t.Fatalf("no policies: %s", got)
	}
}
