package routing_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsize"
	"github.com/luis-mc/ci-runners-shared-pool/internal/routing"
)

var t0 = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

// fakeGH is GitHub as the service sees it: variables, runner labels, pull requests.
type fakeGH struct {
	vars       map[string]string
	sets       []string
	deletes    []string
	adds       []string // "runner:label"
	removes    []string
	prs        []github.PullRequestRequest
	setErr     error
	head       string
	open       []time.Time
	scanned    []string
	rows       []jobsize.ScanRow
	files      map[string][]byte
	existingPR bool
}

func (f *fakeGH) ListVariables(context.Context, int64, string, string) ([]github.Variable, error) {
	var out []github.Variable
	for n, v := range f.vars {
		out = append(out, github.Variable{Name: n, Value: v})
	}
	return out, nil
}
func (f *fakeGH) SetVariable(_ context.Context, _ int64, _, _ string, v github.Variable) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.vars[v.Name] = v.Value
	f.sets = append(f.sets, v.Name)
	return nil
}
func (f *fakeGH) DeleteVariable(_ context.Context, _ int64, _, _, n string) error {
	delete(f.vars, n)
	f.deletes = append(f.deletes, n)
	return nil
}
func (f *fakeGH) AddRunnerLabels(_ context.Context, _ int64, _ github.RunnerScope, id int64, l []string) error {
	f.adds = append(f.adds, fmt.Sprintf("%d:%s", id, strings.Join(l, ",")))
	return nil
}
func (f *fakeGH) RemoveRunnerLabel(_ context.Context, _ int64, _ github.RunnerScope, id int64, l string) error {
	f.removes = append(f.removes, fmt.Sprintf("%d:%s", id, l))
	return nil
}
func (f *fakeGH) BranchHead(context.Context, int64, string, string, string) (string, error) {
	return f.head, nil
}
func (f *fakeGH) OpenPullRequest(_ context.Context, _ int64, _, _ string, pr github.PullRequestRequest) (github.PullRequest, error) {
	f.prs = append(f.prs, pr)
	return github.PullRequest{Number: 7, URL: "https://example.test/pr/7", Created: !f.existingPR}, nil
}
func (f *fakeGH) ScanRepo(_ context.Context, _, sha string) error {
	f.scanned = append(f.scanned, sha)
	return nil
}
func (f *fakeGH) ScanRows(context.Context, string) ([]jobsize.ScanRow, error) { return f.rows, nil }
func (f *fakeGH) WorkflowFiles(context.Context, routing.Repo) (map[string][]byte, error) {
	return f.files, nil
}
func (f *fakeGH) OpenRunStarts(context.Context, routing.Repo) ([]time.Time, error) {
	return f.open, nil
}

const wf = `jobs:
  e2e:
    runs-on: ${{ fromJSON(vars.CRSP_RUNS_ON_LINUX) }}
  unit:
    runs-on: ${{ fromJSON(vars.CRSP_RUNS_ON_LINUX) }}
`

func newSvc(t *testing.T) (*routing.Service, *fakeGH, *time.Time) {
	t.Helper()
	now := t0
	f := &fakeGH{vars: map[string]string{"CRSP_RUNS_ON_LINUX": `["self-hosted","Linux"]`}, head: "abc123", files: map[string][]byte{".github/workflows/ci.yml": []byte(wf)},
		rows: []jobsize.ScanRow{{Job: "e2e", Class: jobsize.Heavy}, {Job: "unit", Class: jobsize.Light}}}
	s := &routing.Service{Store: &routing.MemStore{}, Scans: f, Vars: f, Files: f, Runs: f, Labeler: f, PRs: f,
		Throttle: &routing.Throttle{Now: func() time.Time { return now }, Sleep: func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }},
		Now:      func() time.Time { return now }}
	return s, f, &now
}

var repo = routing.Repo{Installation: 1, Owner: "luis-mc", Name: "app", DefaultBranch: "main"}

// R5.102, R5.106: opt-in scans the default branch and writes one variable per
// job; a second sync with nothing changed writes nothing.
func TestOptInWritesVariablesOnlyOnChange(t *testing.T) {
	s, f, _ := newSvc(t)
	res, err := s.OptIn(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(f.sets)
	if len(f.scanned) != 1 || f.scanned[0] != "abc123" || strings.Join(f.sets, ",") != "CRSP_E2E,CRSP_UNIT" || len(res.Written) != 2 {
		t.Fatalf("scanned %v sets %v res %+v", f.scanned, f.sets, res)
	}
	var list []string
	if err := json.Unmarshal([]byte(f.vars["CRSP_E2E"]), &list); err != nil || len(list) != 3 || list[0] != "self-hosted" || list[2] != routing.JobLabel("luis-mc/app", "e2e") {
		t.Fatalf("value %q", f.vars["CRSP_E2E"])
	}
	if f.vars["CRSP_RUNS_ON_LINUX"] != `["self-hosted","Linux"]` {
		t.Fatal("CRSP_RUNS_ON_LINUX must stay as it was")
	}
	f.sets = nil
	res, _ = s.Sync(context.Background(), repo, "abc124")
	if len(f.sets) != 0 || res.Unchanged != 2 {
		t.Fatalf("rewrote unchanged variables: %v %+v", f.sets, res)
	}
}

// R5.106: a push to another branch does nothing; a push to the default branch scans again.
func TestOnPush(t *testing.T) {
	s, f, _ := newSvc(t)
	if _, err := s.OnPush(context.Background(), repo, "refs/heads/feature", "x1"); err != nil || len(f.scanned) != 0 {
		t.Fatalf("feature push scanned: %v %v", f.scanned, err)
	}
	if _, err := s.OnPush(context.Background(), repo, "refs/heads/main", "x2"); err != nil || len(f.scanned) != 1 {
		t.Fatalf("main push: %v %v", f.scanned, err)
	}
}

// R5.105: a job with no scan row, or on an OS the pool does not serve, keeps
// the fallback and is never blocked; a hosted CRSP_RUNS_ON_<OS> is left alone.
func TestFirstRunJobIsNeverBlocked(t *testing.T) {
	s, f, _ := newSvc(t)
	f.rows = f.rows[:1] // unit has no scan row yet
	res, err := s.Sync(context.Background(), repo, "a")
	if err != nil || len(res.Skipped) != 1 || !strings.HasPrefix(res.Skipped[0], "unit:") || f.vars["CRSP_UNIT"] != "" {
		t.Fatalf("res %+v err %v", res, err)
	}
	if _, ok, _ := s.Label(context.Background(), "luis-mc/app", "unit"); ok {
		t.Fatal("unit must have no label yet")
	}
	f.vars["CRSP_RUNS_ON_LINUX"] = `["ubuntu-latest"]` // hosted fallback is on
	f.rows = append(f.rows, jobsize.ScanRow{Job: "unit", Class: jobsize.Light})
	res, _ = s.Sync(context.Background(), repo, "b")
	if len(res.Skipped) != 2 {
		t.Fatalf("hosted base must skip every job: %+v", res)
	}
}

// R5.105: a changed label goes in the variable; the old label is served until
// no run created before the change is open, then removed from the runner.
func TestChangedLabelRetiresOnlyWhenOldRunsAreDone(t *testing.T) {
	s, f, now := newSvc(t)
	ctx := context.Background()
	f.files[".github/workflows/ci.yml"] = []byte(wf + "  lint:\n    runs-on: ${{ fromJSON(vars.CRSP_RUNS_ON_LINUX) }}\n")
	f.rows = append(f.rows, jobsize.ScanRow{Job: "lint", Class: jobsize.Light})
	if _, err := s.Sync(ctx, repo, "a"); err != nil {
		t.Fatal(err)
	}
	old := routing.JobLabel("luis-mc/app", "e2e")
	if err := s.Assign(ctx, repo, old, []routing.RunnerRef{{ID: "r1", GitHubID: 11, Persistent: true, ScopeOwner: "luis-mc", ScopeRepo: "app"}, {ID: "r2", Persistent: false}}); err != nil {
		t.Fatal(err)
	}
	if len(f.adds) != 1 || f.adds[0] != "11:"+old {
		t.Fatalf("adds %v (an ephemeral runner gets its label at creation)", f.adds)
	}
	_ = s.Assign(ctx, repo, old, []routing.RunnerRef{{ID: "r1", GitHubID: 11, Persistent: true}})
	if len(f.adds) != 1 {
		t.Fatalf("label added twice: %v", f.adds)
	}
	// Too many jobs for the cap: the jobs now share class labels.
	s.Cap, s.Reserved = 100, 98 // room for 2 labels, 3 jobs, 2 size classes
	*now = now.Add(time.Hour)
	f.open = []time.Time{t0.Add(30 * time.Minute)} // a run created before the change is open
	res, err := s.Sync(ctx, repo, "b")
	if err != nil || !res.ByClass || len(res.Retired) != 0 || len(f.removes) != 0 {
		t.Fatalf("res %+v err %v removes %v", res, err, f.removes)
	}
	served, _ := s.ServedLabels(ctx, "luis-mc/app", "e2e")
	if len(served) != 2 {
		t.Fatalf("both labels are served while the old run is open: %v", served)
	}
	*now = now.Add(time.Hour)
	f.open = []time.Time{*now} // only a run created after the change
	got, err := s.Sweep(ctx, repo)
	if err != nil || len(got) != 3 || got[0] != old || len(f.removes) != 1 || f.removes[0] != "11:"+old {
		t.Fatalf("sweep %v %v removes %v", got, err, f.removes)
	}
	if served, _ = s.ServedLabels(ctx, "luis-mc/app", "e2e"); len(served) != 1 {
		t.Fatalf("served after sweep %v", served)
	}
}

// R5.103: a persistent runner never gets a label past the cap.
func TestAssignRefusesPastCap(t *testing.T) {
	s, f, _ := newSvc(t)
	s.Cap, s.Reserved = 11, 10
	rr := []routing.RunnerRef{{ID: "r1", GitHubID: 1, Persistent: true}}
	if err := s.Assign(context.Background(), repo, "crsp-a-0001", rr); err != nil {
		t.Fatal(err)
	}
	err := s.Assign(context.Background(), repo, "crsp-b-0002", rr)
	if !errors.Is(err, routing.ErrLabelCap) || len(f.adds) != 1 {
		t.Fatalf("err %v adds %v", err, f.adds)
	}
}

// R5.108: a rate limit stops the bulk write, keeps what was written, holds the
// throttle, and the next sync finishes the rest.
func TestRateLimitStopsBulkWrite(t *testing.T) {
	s, f, now := newSvc(t)
	f.setErr = &github.RateLimitError{ResetAt: t0.Add(5 * time.Minute), Secondary: true}
	_, err := s.Sync(context.Background(), repo, "a")
	if !errors.Is(err, github.ErrRateLimited) || len(f.sets) != 0 {
		t.Fatalf("err %v sets %v", err, f.sets)
	}
	f.setErr = nil
	before := *now
	if _, err := s.Sync(context.Background(), repo, "a"); err != nil || len(f.sets) != 2 {
		t.Fatalf("err %v sets %v", err, f.sets)
	}
	if now.Sub(before) < 4*time.Minute {
		t.Fatalf("throttle did not hold until the reset: %v", now.Sub(before))
	}
}

// R5.101: a job that left the workflows loses its variable and retires its label.
func TestGoneJobLosesVariable(t *testing.T) {
	s, f, _ := newSvc(t)
	ctx := context.Background()
	_, _ = s.Sync(ctx, repo, "a")
	f.files[".github/workflows/ci.yml"] = []byte("jobs:\n  unit:\n    runs-on: ${{ fromJSON(vars.CRSP_RUNS_ON_LINUX) }}\n")
	f.rows = f.rows[1:]
	res, err := s.Sync(ctx, repo, "b")
	if err != nil || len(res.Deleted) != 1 || res.Deleted[0] != "CRSP_E2E" {
		t.Fatalf("res %+v err %v", res, err)
	}
	if _, ok := f.vars["CRSP_E2E"]; ok {
		t.Fatal("variable must be gone")
	}
}

// R5.107: the pull request opens once, on the owner's repositories only.
func TestOpenPROnceAndOnlyForOwner(t *testing.T) {
	s, f, _ := newSvc(t)
	ctx := context.Background()
	res, err := s.OpenPR(ctx, repo)
	if err != nil || !res.Created || res.Number != 7 || len(f.prs) != 1 {
		t.Fatalf("res %+v err %v", res, err)
	}
	pr := f.prs[0]
	if pr.Branch != routing.PRBranch || len(pr.Files) != 1 || !strings.Contains(string(pr.Files[0].Content), "CRSP_E2E") || !strings.Contains(pr.Body, "e2e") {
		t.Fatalf("request %+v", pr)
	}
	if res, _ = s.OpenPR(ctx, repo); res.Number != 7 || len(f.prs) != 1 {
		t.Fatalf("second call opened another: %d", len(f.prs))
	}
	other := routing.Repo{Installation: 2, Owner: "someone", Name: "app", DefaultBranch: "main"}
	if _, err := s.OpenPR(ctx, other); !errors.Is(err, routing.ErrNotOwned) || len(f.prs) != 1 {
		t.Fatalf("err %v", err)
	}
	if p, err := s.PatchFor(ctx, other); err != nil || p.Empty() || p.Diff() == "" {
		t.Fatalf("doctor patch %v %v", p, err)
	}
	f.files = map[string][]byte{"x.yml": []byte("jobs:\n  a:\n    runs-on: ubuntu-latest\n")}
	if res, err := s.OpenPR(ctx, routing.Repo{Installation: 3, Owner: "luis-mc", Name: "plain", DefaultBranch: "main"}); err != nil || !res.Empty || len(f.prs) != 1 {
		t.Fatalf("nothing to change must open nothing: %+v %v", res, err)
	}
}

// CRSP229: the sync never deletes a pool variable as a stale per-job one,
// such as CRSP_RUNS_ON_LINUX that an older version recorded as the variable of
// a job named runs-on-linux.
func TestSyncNeverDeletesAPoolVariable(t *testing.T) {
	s, f, _ := newSvc(t)
	ctx := context.Background()
	f.vars["CRSP_RUNS_ON_CANARY_BUILD"] = `["self-hosted","linux"]`
	if err := s.Store.Save(ctx, repo.Full(), routing.State{Jobs: []routing.JobState{
		{Key: "runs-on-linux", Var: "CRSP_RUNS_ON_LINUX"}, {Key: "build", Var: "CRSP_RUNS_ON_CANARY_BUILD"}}}); err != nil {
		t.Fatal(err)
	}
	res, err := s.Sync(ctx, repo, "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 0 || len(f.deletes) != 0 {
		t.Fatalf("deleted %v %v", res.Deleted, f.deletes)
	}
	if _, ok := f.vars["CRSP_RUNS_ON_LINUX"]; !ok {
		t.Fatal("CRSP_RUNS_ON_LINUX must stay")
	}
}
