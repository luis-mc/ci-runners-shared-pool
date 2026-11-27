package runners

import (
	"context"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// orgResolver knows one repository (1) of an organization whose pool is on and
// one (2) of a personal account.
type orgResolver struct{ on bool }

func (orgResolver) Resolve(_ context.Context, s Scope) (Target, error) {
	if s.Kind == "organization" {
		return Target{Scope: github.RunnerScope{Owner: "acme", Organization: "acme", GroupID: 77}, InstallationID: 20,
			URL: "https://github.com/acme", Group: s.Group}, nil
	}
	return Target{Scope: github.RunnerScope{Owner: "alice", Repo: "demo"}, InstallationID: 10, URL: "https://github.com/alice/demo"}, nil
}

func (o orgResolver) OrgScope(_ context.Context, s Scope) (Scope, bool, error) {
	if !o.on || s.RepositoryID != 1 {
		return s, false, nil
	}
	return Scope{Kind: "organization", RepositoryID: 1, Organization: "acme", Group: "pool-a"}, true, nil
}

func TestJitRunnerOfOrganizationRegistersAtOrgLevel(t *testing.T) { // github-integration R5.21
	r := newRig(machine("linux", "x64", true))
	r.svc.Resolver = orgResolver{on: true}
	rec, err := r.svc.StartJobRunner(context.Background(), jobRunner(), "luis-mc")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Scope.Kind != "organization" || rec.Scope.Organization != "acme" || rec.Scope.Group != "pool-a" || rec.Scope.RepositoryID != 1 {
		t.Fatalf("the record keeps the organization scope and the repository: %+v", rec.Scope)
	}
	if len(r.gh.jitCalls) != 1 || r.gh.jitCalls[0].GroupID != 77 {
		t.Fatalf("the JIT config is asked for in the pool's group: %+v", r.gh.jitCalls)
	}
	stored, _ := r.store.ListRunners(context.Background(), Filter{})
	if len(stored) != 1 || stored[0].Scope.Kind != "organization" {
		t.Fatalf("%+v", stored)
	}
}

func TestPersistentRunnerOfOrganizationRegistersAtOrgLevel(t *testing.T) { // github-integration R5.21
	r := newRig(machine("linux", "x64", true))
	r.svc.Resolver = orgResolver{on: true}
	rec := r.add(t)
	if rec.Scope.Kind != "organization" || rec.Scope.Organization != "acme" {
		t.Fatalf("%+v", rec.Scope)
	}
	if len(r.ag.added) != 1 || r.ag.added[0].GitHubURL != "https://github.com/acme" || r.ag.added[0].Group != "pool-a" {
		t.Fatalf("the agent registers at the organization, in the group: %+v", r.ag.added)
	}
}

func TestRunnerStaysRepositoryLevelWhenModeIsOff(t *testing.T) { // github-integration R5.21
	r := newRig(machine("linux", "x64", true))
	r.svc.Resolver = orgResolver{on: false}
	rec, err := r.svc.StartJobRunner(context.Background(), jobRunner(), "luis-mc")
	if err != nil || rec.Scope.Kind != "repository" || rec.Scope.RepositoryID != 1 {
		t.Fatalf("%+v %v", rec.Scope, err)
	}
	// A personal account's repository is never moved.
	j := jobRunner()
	j.Name, j.Scope = "crsp-job-2", Scope{Kind: "repository", RepositoryID: 2}
	r.svc.Resolver = orgResolver{on: true}
	rec, err = r.svc.StartJobRunner(context.Background(), j, "luis-mc")
	if err != nil || rec.Scope.Kind != "repository" || r.gh.jitCalls[1].GroupID != 0 {
		t.Fatalf("%+v %v %+v", rec.Scope, err, r.gh.jitCalls)
	}
}

func TestScaleSetJitKeepsItsScope(t *testing.T) { // github-integration R5.21, R5.40
	r := newRig(machine("linux", "x64", true))
	r.svc.Resolver = orgResolver{on: true}
	j := jobRunner()
	j.JIT = func(context.Context) (github.JITConfig, error) { return github.NewJITConfig(5, "SS-JIT"), nil }
	rec, err := r.svc.StartJobRunner(context.Background(), j, "luis-mc")
	if err != nil || rec.Scope.Kind != "repository" || len(r.gh.jitCalls) != 0 {
		t.Fatalf("a scale-set runner registers where its scale set lives: %+v %v", rec.Scope, err)
	}
}
