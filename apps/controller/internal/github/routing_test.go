package github

import (
	"context"
	"fmt"
	"testing"
)

// R5.104: custom labels go on and off a persistent runner through the labels API.
func TestRunnerLabelsAddAndRemove(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.fake.AddInstall(10, "alice", "User")
	e.fake.AddRepo(100, 10, "alice", "demo", true)
	scope := RunnerScope{Owner: "alice", Repo: "demo"}
	if err := e.client.AddRunnerLabels(ctx, 10, scope, 5, []string{"crsp-e2e-7f3a"}); err != nil {
		t.Fatal(err)
	}
	if got := e.fake.RunnerCustomLabels("alice/demo", 5); len(got) != 1 || got[0] != "crsp-e2e-7f3a" {
		t.Fatalf("labels %v", got)
	}
	if err := e.client.RemoveRunnerLabel(ctx, 10, scope, 5, "crsp-e2e-7f3a"); err != nil {
		t.Fatal(err)
	}
	if err := e.client.RemoveRunnerLabel(ctx, 10, scope, 5, "crsp-e2e-7f3a"); err != nil {
		t.Fatalf("a label that is gone is not an error: %v", err)
	}
	if got := e.fake.RunnerCustomLabels("alice/demo", 5); len(got) != 0 {
		t.Fatalf("labels after remove %v", got)
	}
}

// R5.103: GitHub fails the add past 100 labels with HTTP 500, the reason crsp
// counts labels before it adds.
func TestRunnerLabelCapIsAnError(t *testing.T) {
	e := newEnv(t)
	e.fake.AddInstall(10, "alice", "User")
	e.fake.AddRepo(100, 10, "alice", "demo", true)
	var many []string
	for i := 0; i < 100; i++ {
		many = append(many, fmt.Sprintf("l%d", i))
	}
	scope := RunnerScope{Owner: "alice", Repo: "demo"}
	if err := e.client.AddRunnerLabels(context.Background(), 10, scope, 5, many); err != nil {
		t.Fatal(err)
	}
	if err := e.client.AddRunnerLabels(context.Background(), 10, scope, 5, []string{"one-too-many"}); err == nil {
		t.Fatal("the 101st label must fail")
	}
}

// R5.107: one pull request carries the patch; a second call adds none.
func TestOpenPullRequestOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.fake.AddInstall(10, "alice", "User")
	e.fake.AddRepo(100, 10, "alice", "demo", true)
	e.fake.SetBranch("alice/demo", "main", "sha-main")
	req := PullRequestRequest{Branch: "crsp/per-job-runs-on", Base: "main", Title: "t", Body: "b", Message: "m",
		Files: []PullRequestFile{{Path: ".github/workflows/ci.yml", Content: []byte("jobs: {}\n")}}}
	pr, err := e.client.OpenPullRequest(ctx, 10, "alice", "demo", req)
	if err != nil || !pr.Created || pr.Number != 1 {
		t.Fatalf("pr %+v err %v", pr, err)
	}
	if c, ok := e.fake.BranchFile("alice/demo", "crsp/per-job-runs-on", ".github/workflows/ci.yml"); !ok || c != "jobs: {}\n" {
		t.Fatalf("file on branch %q %v", c, ok)
	}
	req.Files[0].Content = []byte("jobs: {a: 1}\n")
	pr, err = e.client.OpenPullRequest(ctx, 10, "alice", "demo", req)
	if err != nil || pr.Created || pr.Number != 1 || len(e.fake.PullRequestsOf("alice/demo")) != 1 {
		t.Fatalf("second call: %+v %v prs %v", pr, err, e.fake.PullRequestsOf("alice/demo"))
	}
	if c, _ := e.fake.BranchFile("alice/demo", "crsp/per-job-runs-on", ".github/workflows/ci.yml"); c != "jobs: {a: 1}\n" {
		t.Fatalf("branch keeps the newest patch: %q", c)
	}
	if h, err := e.client.BranchHead(ctx, 10, "alice", "demo", "main"); err != nil || h != "sha-main" {
		t.Fatalf("head %q %v", h, err)
	}
}
