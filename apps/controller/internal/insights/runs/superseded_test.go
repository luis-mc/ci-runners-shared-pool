package runs

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

func runJob(id, runID int64, wf, branch, head, event string, created, start, end float64, result string) insights.JobFact {
	j := job(id, "j", created, created, start, end)
	j.RunID, j.Workflow, j.Branch, j.HeadSHA, j.Event, j.Result = runID, wf, branch, head, event, result
	j.RepositoryID, j.Repository = 7, "o/r"
	return j
}

func TestSupersededMinutes(t *testing.T) {
	jobs := []insights.JobFact{
		// run 1 on commit a; run 2 on commit b is created at minute 2; run 1's job starts at 3 and runs 5 min
		runJob(1, 1, "ci.yml", "feat", "a", "push", 0, 3, 8, "success"),
		// a cancelled job of run 1 does not count
		runJob(2, 1, "ci.yml", "feat", "a", "push", 0, 4, 9, "cancelled"),
		// a job of run 1 that started before the newer run is real work
		runJob(3, 1, "ci.yml", "feat", "a", "push", 0, 1, 6, "success"),
		runJob(4, 2, "ci.yml", "feat", "b", "push", 2, 2, 4, "success"),
		// another branch is not superseded
		runJob(5, 3, "ci.yml", "other", "c", "push", 0, 3, 8, "success"),
	}
	got := SupersededMinutes(jobs)
	if len(got) != 1 || got[runKey{1, 1}] != 5 {
		t.Errorf("got %v, want run 1 = 5 min", got)
	}
}

func TestTwoEventsOneHead(t *testing.T) {
	jobs := []insights.JobFact{
		runJob(1, 1, "ci.yml", "feat", "a", "push", 0, 0, 10, "success"),
		runJob(2, 2, "ci.yml", "feat", "a", "pull_request", 1, 1, 8, "success"),
		runJob(3, 3, "ci.yml", "feat", "a", "pull_request", 2, 2, 5, "success"), // same event as run 2: not a duplicate pair with run 1's event
		runJob(4, 4, "lint.yml", "feat", "a", "push", 0, 0, 3, "success"),
	}
	got := TwoEventsOneHead(jobs)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Events != "pull_request+push" || got[0].Minutes != 7 || got[0].Run.ID != 2 {
		t.Errorf("first = %+v", got[0])
	}
}

type fakeTrees map[string]string

func (f fakeTrees) Tree(_ context.Context, _ int64, sha string) (string, bool, error) {
	t, ok := f[sha]
	return t, ok, nil
}

func TestRetestedTree(t *testing.T) {
	pr := runJob(1, 1, "ci.yml", "feat", "pr1", "pull_request", 0, 0, 10, "success")
	push := runJob(2, 2, "ci.yml", "main", "m1", "push", 20, 20, 32, "success")
	push.DefaultBranch = true
	other := runJob(3, 3, "ci.yml", "main", "m2", "push", 40, 40, 50, "success")
	other.DefaultBranch = true
	failed := runJob(4, 4, "ci.yml", "feat2", "pr2", "pull_request", 0, 0, 10, "failure")
	jobs := []insights.JobFact{pr, push, other, failed}
	got, err := FindRetestedTrees(context.Background(), fakeTrees{"pr1": "T1", "m1": "T1", "m2": "T2", "pr2": "T2"}, jobs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[runKey{2, 1}] != 12 {
		t.Errorf("got %v, want only run 2 = 12 min (the failed pull request run does not count)", got)
	}
	// tree unknown while the GitHub share is spent: nothing is claimed
	got, _ = FindRetestedTrees(context.Background(), fakeTrees{}, jobs)
	if len(got) != 0 {
		t.Errorf("got %v with no trees", got)
	}
}

type fakeJobs []insights.JobFact

func (f fakeJobs) JobsBetween(context.Context, insights.Window, int64) ([]insights.JobFact, error) {
	return f, nil
}

func TestAnalyserThresholdAndKinds(t *testing.T) {
	jobs := fakeJobs{
		runJob(1, 1, "ci.yml", "feat", "a", "push", 0, 0, 10, "success"),
		runJob(2, 2, "ci.yml", "feat", "a", "pull_request", 1, 1, 13, "success"),
	}
	w := insights.Window{From: t0, To: t0.Add(7 * 24 * 60 * 60 * 1e9)}
	a := Analyser{Jobs: jobs}
	fs, err := a.Analyse(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].Kind != insights.KindRunDuplicateEvent || fs[0].MinutesSavedWeek != 12 {
		t.Fatalf("got %+v", fs)
	}
	var ev map[string]any
	if err := json.Unmarshal(fs[0].Evidence, &ev); err != nil {
		t.Fatal(err)
	}
	// under the 10 min threshold: no finding
	fs, _ = Analyser{Jobs: jobs[:1]}.Analyse(context.Background(), w)
	if len(fs) != 0 {
		t.Errorf("got %+v", fs)
	}
	if a.Name() != "runs" || len(a.Kinds()) != 5 {
		t.Errorf("name/kinds = %s %v", a.Name(), a.Kinds())
	}
}
