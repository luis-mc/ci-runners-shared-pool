package github

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
)

// A workflow_job body in the shape GitHub sends (trimmed to what matters).
const jobEventBody = `{
 "action": "in_progress",
 "workflow_job": {
  "id": 8801, "run_id": 5500, "run_attempt": 2,
  "run_url": "https://api.github.com/repos/luis-mc/app/actions/runs/5500",
  "workflow_name": "CI", "head_branch": "OPS34-jobs-section", "head_sha": "abc123",
  "name": "test (ubuntu, 22)", "status": "in_progress", "conclusion": null,
  "html_url": "https://github.com/luis-mc/app/actions/runs/5500/job/8801",
  "created_at": "2026-11-18T09:00:00Z", "started_at": "2026-11-18T09:01:00Z", "completed_at": null,
  "runner_name": "crsp-1", "labels": ["self-hosted", "linux"],
  "steps": [
   {"name": "Set up job", "status": "completed", "conclusion": "success", "number": 1,
    "started_at": "2026-11-18T09:01:00Z", "completed_at": "2026-11-18T09:01:02Z"},
   {"name": "Run tests", "status": "in_progress", "conclusion": null, "number": 2,
    "started_at": "2026-11-18T09:01:02Z", "completed_at": null},
   {"name": "Post", "status": "queued", "conclusion": null, "number": 3, "started_at": null, "completed_at": null}
  ]
 },
 "repository": {"full_name": "luis-mc/app"}
}`

func TestParseWorkflowJobDetail(t *testing.T) {
	ev, err := ParseWorkflowJobDetail([]byte(jobEventBody))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Action != "in_progress" || ev.Job.ID != 8801 || ev.Job.RunID != 5500 || ev.Job.RunAttempt != 2 ||
		ev.Job.HeadBranch != "OPS34-jobs-section" || ev.Job.HeadSHA != "abc123" || ev.Job.Workflow != "CI" || !ev.Job.Partial {
		t.Fatalf("job %+v", ev)
	}
	if ev.RunURL != "https://api.github.com/repos/luis-mc/app/actions/runs/5500" || ev.RunHTMLURL != "https://github.com/luis-mc/app/actions/runs/5500" {
		t.Fatalf("run urls %q %q", ev.RunURL, ev.RunHTMLURL)
	}
	if len(ev.Steps) != 3 || ev.Steps[0].Name != "Set up job" || ev.Steps[0].Conclusion != "success" || ev.Steps[0].CompletedAt.IsZero() ||
		ev.Steps[1].Status != "in_progress" || !ev.Steps[1].CompletedAt.IsZero() || !ev.Steps[2].StartedAt.IsZero() {
		t.Fatalf("steps %+v", ev.Steps)
	}
}

func TestParseWorkflowJobEventUnchanged(t *testing.T) {
	j, err := ParseWorkflowJobEvent([]byte(jobEventBody))
	d, _ := ParseWorkflowJobDetail([]byte(jobEventBody))
	if err != nil || !reflect.DeepEqual(j, d.Job) {
		t.Fatalf("%v %+v", err, j)
	}
	if _, err := ParseWorkflowJobEvent([]byte(`{"workflow_job":{"id":0}}`)); err == nil {
		t.Fatal("a job with no id was accepted")
	}
}

type detailEvents struct {
	recEvents
	details []JobEvent
}

func (d *detailEvents) JobDetail(e JobEvent) { d.details = append(d.details, e) }

func TestWebhookDeliversDetail(t *testing.T) {
	h, _, secret := newHook(t)
	ev := &detailEvents{}
	h.Events = ev
	if code := deliver(h, secret, []byte(jobEventBody), "", "d1", "workflow_job"); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if len(ev.jobs) != 1 || len(ev.details) != 1 || len(ev.details[0].Steps) != 3 {
		t.Fatalf("jobs %d details %+v", len(ev.jobs), ev.details)
	}
}

func TestWorkflowFilePath(t *testing.T) {
	for in, want := range map[string]string{".github/workflows/ci.yml@refs/heads/main": ".github/workflows/ci.yml", ".github/workflows/ci.yml": ".github/workflows/ci.yml", "": ""} {
		if got := workflowFilePath(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

func TestParseWorkflowGraph(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		needs   map[string][]string
		dynamic []string
	}{
		{"string and list", "on: push\njobs:\n  a:\n    runs-on: x\n  b:\n    needs: a\n  c:\n    needs: [a, b]\n",
			map[string][]string{"a": {}, "b": {"a"}, "c": {"a", "b"}}, nil},
		{"names join by display name", "jobs:\n  build:\n    name: Build app\n  test:\n    name: Unit tests\n    needs: build\n",
			map[string][]string{"Build app": {}, "Unit tests": {"Build app"}}, nil},
		{"matrix is dynamic", "jobs:\n  t:\n    strategy:\n      matrix:\n        os: [a, b]\n    runs-on: ${{ matrix.os }}\n  done:\n    needs: t\n",
			map[string][]string{"t": {}, "done": {"t"}}, []string{"t"}},
		{"matrix from json", "jobs:\n  t:\n    strategy:\n      matrix: ${{ fromJson(needs.x.outputs.m) }}\n    needs: x\n  x: {}\n",
			map[string][]string{"t": {"x"}, "x": {}}, []string{"t"}},
		{"reusable workflow", "jobs:\n  call:\n    uses: ./.github/workflows/r.yml\n  after:\n    needs: call\n",
			map[string][]string{"call": {}, "after": {"call"}}, []string{"call"}},
		{"expression name falls back to the id", "jobs:\n  t:\n    name: test ${{ matrix.v }}\n    strategy:\n      matrix: {v: [1]}\n",
			map[string][]string{"t": {}}, []string{"t"}},
		{"quoted keys and comments", "# c\njobs:\n  'a-b':   # first\n    runs-on: x\n  \"c\":\n    needs: 'a-b'\n",
			map[string][]string{"a-b": {}, "c": {"a-b"}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, err := ParseWorkflowGraph([]byte(c.yaml))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(g.Needs, c.needs) {
				t.Fatalf("needs %v want %v", g.Needs, c.needs)
			}
			if len(g.Dynamic) != len(c.dynamic) {
				t.Fatalf("dynamic %v want %v", g.Dynamic, c.dynamic)
			}
			for _, d := range c.dynamic {
				if !g.Dynamic[d] {
					t.Fatalf("%s not dynamic: %v", d, g.Dynamic)
				}
			}
		})
	}
	for _, bad := range []string{"", "jobs: [", "name: x\n", "jobs: {}\n"} {
		if _, err := ParseWorkflowGraph([]byte(bad)); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestRESTWorkflowFetch(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.fake.AddInstall(10, "alice", "User")
	e.fake.AddRepo(100, 10, "alice", "demo", true)
	e.fake.SetWorkflow("alice/demo", ".github/workflows/ci.yml", "name: ci\njobs: {}\n")
	p, err := e.client.GetRunWorkflowPath(ctx, 10, "alice", "demo", 77)
	if err != nil || p != ".github/workflows/ci.yml" {
		t.Fatalf("path %q %v", p, err)
	}
	b, err := e.client.GetFileAt(ctx, 10, "alice", "demo", p, "0123456789abcdef")
	if err != nil || string(b) != "name: ci\njobs: {}\n" {
		t.Fatalf("file %q %v", b, err)
	}
	if _, err := e.client.GetFileAt(ctx, 10, "alice", "demo", ".github/workflows/none.yml", "abc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing file: %v", err)
	}
}

func TestRESTListBlobsAt(t *testing.T) { // IMG2, registry R5.73
	e := newEnv(t)
	ctx := context.Background()
	e.fake.AddInstall(10, "alice", "User")
	e.fake.AddRepo(100, 10, "alice", "demo", true)
	e.fake.SetWorkflow("alice/demo", "go.mod", "module x\n")
	e.fake.SetWorkflow("alice/demo", "sub/package.json", "{}")
	got, err := e.client.ListBlobsAt(ctx, 10, "alice", "demo", "0123456789abcdef0123456789abcdef01234567")
	if err != nil || len(got) != 2 || got[0].Path != "go.mod" || got[0].Size != 9 || len(got[0].SHA) != 40 || got[1].Path != "sub/package.json" {
		t.Fatalf("%+v %v", got, err)
	}
	e.fake.TreeTruncated = true
	if _, err := e.client.ListBlobsAt(ctx, 10, "alice", "demo", "abc"); !errors.Is(err, ErrTreeTruncated) {
		t.Fatalf("truncated: %v", err)
	}
}

func TestRESTListBlobsAtHeldBackOnLowBudget(t *testing.T) { // IMG2, registry R5.75, R6.6
	e := newEnv(t)
	e.fake.AddInstall(10, "alice", "User")
	e.fake.AddRepo(100, 10, "alice", "demo", true)
	ctx := context.Background()
	if _, err := e.client.ListBlobsAt(ctx, 10, "alice", "demo", "abc"); err != nil {
		t.Fatal(err)
	}
	e.fake.Lock(func() { e.fake.RateRemaining = 2000 })
	if _, err := e.client.ListBlobsAt(ctx, 10, "alice", "demo", "abc"); err != nil {
		t.Fatal(err) // records the budget
	}
	var rl *RateLimitError
	if _, err := e.client.ListBlobsAt(Background(ctx), 10, "alice", "demo", "abc"); !errors.As(err, &rl) {
		t.Fatalf("want RateLimitError: %v", err)
	}
}
