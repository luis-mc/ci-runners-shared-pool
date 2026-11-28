package repos

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/routing"
)

const routedWorkflow = `name: ci
on: push
jobs:
  unit:
    runs-on: ${{ fromJSON(vars.CRSP_RUNS_ON_LINUX) }}
    steps:
      - run: go test ./...
`

// fakePRs is GitHub as the routing service sees it: workflow files and pull requests.
type fakePRs struct {
	files   map[string][]byte
	opened  []github.PullRequestRequest
	created bool // the next pull request is new
	fail    error
}

func (f *fakePRs) WorkflowFiles(context.Context, routing.Repo) (map[string][]byte, error) {
	return f.files, nil
}
func (f *fakePRs) BranchHead(context.Context, int64, string, string, string) (string, error) {
	return "abc", nil
}
func (f *fakePRs) OpenPullRequest(_ context.Context, _ int64, _, _ string, pr github.PullRequestRequest) (github.PullRequest, error) {
	if f.fail != nil {
		return github.PullRequest{}, f.fail
	}
	f.opened = append(f.opened, pr)
	return github.PullRequest{Number: 7, URL: "https://example.test/pr/7", Created: f.created}, nil
}

func runsOnEnv(t *testing.T, files map[string][]byte) (*env, *fakePRs) {
	t.Helper()
	e := newEnv(t)
	e.world()
	f := &fakePRs{files: files, created: true}
	e.svc.SetRunsOnPullRequests(&routing.Service{Store: &routing.MemStore{}, Files: f, PRs: f, PROwners: []string{"alice"}})
	return e, f
}

func TestPreviewRunsOnPRShowsFilesAndDiff(t *testing.T) { // R5.113
	e, f := runsOnEnv(t, map[string][]byte{".github/workflows/ci.yml": []byte(routedWorkflow)})
	out, err := e.svc.PreviewRunsOnPR(context.Background(), adminP, RunsOnPRTarget{RepositoryID: 100})
	if err != nil || len(out) != 1 {
		t.Fatalf("%v %+v", err, out)
	}
	v := out[0]
	if v.State != PRPreviewChange || len(v.Files) != 1 || v.Files[0].Path != ".github/workflows/ci.yml" || v.Files[0].Changes != 1 {
		t.Fatalf("%+v", v)
	}
	if !strings.Contains(v.Diff, "+    runs-on: ${{ fromJSON(vars.CRSP_UNIT") {
		t.Errorf("diff:\n%s", v.Diff)
	}
	if len(f.opened) != 0 {
		t.Error("a preview opened a pull request")
	}
}

func TestPreviewRunsOnPRNothingToChange(t *testing.T) {
	e, _ := runsOnEnv(t, map[string][]byte{"ci.yml": []byte("jobs:\n  a:\n    runs-on: ubuntu-latest\n")})
	out, err := e.svc.PreviewRunsOnPR(context.Background(), adminP, RunsOnPRTarget{RepositoryID: 100})
	if err != nil || out[0].State != PRNothing {
		t.Fatalf("%v %+v", err, out)
	}
}

func TestOpenRunsOnPROpensAndAudits(t *testing.T) { // R5.113
	e, f := runsOnEnv(t, map[string][]byte{".github/workflows/ci.yml": []byte(routedWorkflow)})
	out, err := e.svc.OpenRunsOnPR(context.Background(), adminP, RunsOnPRTarget{RepositoryID: 100})
	if err != nil || len(out) != 1 || out[0].State != PROpened || out[0].Number != 7 || out[0].URL == "" {
		t.Fatalf("%v %+v", err, out)
	}
	if len(f.opened) != 1 || f.opened[0].Branch != routing.PRBranch {
		t.Fatalf("%+v", f.opened)
	}
	if e.audit.count(auditRunsOnPR) != 1 || !strings.Contains(e.audit.detail(auditRunsOnPR), "#7") {
		t.Errorf("audit: %v", e.audit.events)
	}
}

func TestOpenRunsOnPRSecondCallReturnsTheFirst(t *testing.T) { // R5.113
	e, f := runsOnEnv(t, map[string][]byte{".github/workflows/ci.yml": []byte(routedWorkflow)})
	ctx := context.Background()
	first, _ := e.svc.OpenRunsOnPR(ctx, adminP, RunsOnPRTarget{RepositoryID: 100})
	second, err := e.svc.OpenRunsOnPR(ctx, adminP, RunsOnPRTarget{RepositoryID: 100})
	if err != nil || second[0].State != PRExisting || second[0].URL != first[0].URL {
		t.Fatalf("%v %+v", err, second)
	}
	if len(f.opened) != 1 {
		t.Errorf("opened %d pull requests", len(f.opened))
	}
	if e.audit.count(auditRunsOnPRHad) != 1 {
		t.Errorf("audit: %v", e.audit.events)
	}
}

func TestOpenRunsOnPRRefused(t *testing.T) { // R5.113
	e, f := runsOnEnv(t, map[string][]byte{".github/workflows/ci.yml": []byte(routedWorkflow)})
	ctx := context.Background()
	if _, err := e.svc.OpenRunsOnPR(ctx, viewerP, RunsOnPRTarget{RepositoryID: 100}); !errors.Is(err, auth.ErrForbidden) {
		t.Errorf("viewer: %v", err)
	}
	if _, err := e.svc.PreviewRunsOnPR(ctx, viewerP, RunsOnPRTarget{RepositoryID: 100}); !errors.Is(err, auth.ErrForbidden) {
		t.Errorf("viewer preview: %v", err)
	}
	// alice is no GitHub admin of repository 102.
	if _, err := e.svc.OpenRunsOnPR(ctx, adminP, RunsOnPRTarget{RepositoryID: 102}); !errors.Is(err, auth.ErrForbidden) {
		t.Errorf("not GitHub admin: %v", err)
	}
	if len(f.opened) != 0 || e.audit.count(auditRunsOnPR) != 0 {
		t.Error("a refused call opened a pull request")
	}
}

func TestOpenRunsOnPRAllCoversOptedInOnly(t *testing.T) { // R5.113
	e, f := runsOnEnv(t, map[string][]byte{"ci.yml": []byte(routedWorkflow)})
	e.turnOn(100)
	out, err := e.svc.OpenRunsOnPR(context.Background(), adminP, RunsOnPRTarget{All: true})
	if err != nil || len(out) != 1 || out[0].RepositoryID != 100 || out[0].State != PROpened {
		t.Fatalf("%v %+v", err, out)
	}
	if len(f.opened) != 1 {
		t.Errorf("opened %d", len(f.opened))
	}
}

func TestOpenRunsOnPRAllKeepsGoingAfterAFailure(t *testing.T) {
	e, f := runsOnEnv(t, map[string][]byte{"ci.yml": []byte(routedWorkflow)})
	e.turnOn(100, 101)
	f.fail = errors.New("GitHub said no")
	out, err := e.svc.OpenRunsOnPR(context.Background(), adminP, RunsOnPRTarget{All: true})
	if err != nil || len(out) != 2 || out[0].State != PRError || out[1].State != PRError || out[0].Message == "" {
		t.Fatalf("%v %+v", err, out)
	}
}

func TestRunsOnPRNotOwnedAndBadTarget(t *testing.T) {
	e, f := runsOnEnv(t, map[string][]byte{"ci.yml": []byte(routedWorkflow)})
	e.svc.SetRunsOnPullRequests(&routing.Service{Store: &routing.MemStore{}, Files: f, PRs: f, PROwners: []string{"someone-else"}})
	ctx := context.Background()
	out, err := e.svc.OpenRunsOnPR(ctx, adminP, RunsOnPRTarget{RepositoryID: 100})
	if err != nil || out[0].State != PRNotOwned {
		t.Fatalf("%v %+v", err, out)
	}
	pv, _ := e.svc.PreviewRunsOnPR(ctx, adminP, RunsOnPRTarget{RepositoryID: 100})
	if pv[0].State != PRNotOwned {
		t.Errorf("%+v", pv)
	}
	if _, err := e.svc.OpenRunsOnPR(ctx, adminP, RunsOnPRTarget{}); !errors.As(err, new(*auth.InvalidError)) {
		t.Errorf("no target: %v", err)
	}
	e.svc.SetRunsOnPullRequests(nil)
	if _, err := e.svc.OpenRunsOnPR(ctx, adminP, RunsOnPRTarget{RepositoryID: 100}); !errors.Is(err, ErrRunsOnPRUnavailable) {
		t.Errorf("off: %v", err)
	}
}

// CRSP402: a repository whose workflows read the dev-standards CI_RUNS_ON names
// has a preview with files and a diff, and the old variable stays a fallback.
func TestPreviewRunsOnPRRoutesCIRunsOn(t *testing.T) {
	wf := "jobs:\n  unit:\n    runs-on: ${{ fromJSON(vars.CI_RUNS_ON || '\"ubuntu-latest\"') }}\n  win:\n    runs-on: ${{ fromJSON(vars.CI_RUNS_ON_WINDOWS) }}\n"
	e, f := runsOnEnv(t, map[string][]byte{".github/workflows/ci.yml": []byte(wf)})
	out, err := e.svc.PreviewRunsOnPR(context.Background(), adminP, RunsOnPRTarget{RepositoryID: 100})
	if err != nil || len(out) != 1 {
		t.Fatalf("%v %+v", err, out)
	}
	v := out[0]
	if v.State != PRPreviewChange || len(v.Files) != 1 || v.Files[0].Changes != 2 {
		t.Fatalf("%+v", v)
	}
	for _, want := range []string{
		"+    runs-on: ${{ fromJSON(vars.CRSP_UNIT || vars.CRSP_RUNS_ON_LINUX || vars.CI_RUNS_ON || '\"ubuntu-latest\"') }}",
		"+    runs-on: ${{ fromJSON(vars.CRSP_WIN || vars.CRSP_RUNS_ON_WINDOWS || vars.CI_RUNS_ON_WINDOWS) }}",
	} {
		if !strings.Contains(v.Diff, want) {
			t.Errorf("diff misses %q:\n%s", want, v.Diff)
		}
	}
	if len(f.opened) != 0 {
		t.Error("a preview opened a pull request")
	}
}
