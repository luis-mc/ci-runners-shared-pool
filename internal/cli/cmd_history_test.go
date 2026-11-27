package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func lastJobsCall(f *fixture) string {
	q := ""
	for _, c := range f.calls {
		if strings.HasPrefix(c, "GET /api/v1/jobs") {
			q = c
		}
	}
	return q
}

// R5.11: history asks only for finished jobs and passes the four filters of the console.
func TestHistoryFilters(t *testing.T) {
	f := newFixture(t)
	f.repos(repoJSON(10, "luis-mc/app", true))
	f.machines(machineJSON("build-01", "active", true))
	f.reply("GET /api/v1/jobs", 200, map[string]any{"items": []any{jobJSON(1, "luis-mc/app", "test", "done")}, "stale": map[string]any{"stale": false}})
	code, out, _ := f.run("history", "--repo", "luis-mc/app", "--machine", "build-01", "--backend", "docker", "--result", "failure", "--since", "24h", "--limit", "20")
	if code != 0 || !strings.Contains(out, "luis-mc/app") {
		t.Fatalf("%d %q", code, out)
	}
	q := lastJobsCall(f)
	for _, want := range []string{"state=done", "repository_id=10", "machine_id=m_build-01", "backend=docker", "result=failure", "limit=20", "from=2026-"} {
		if !strings.Contains(q, want) {
			t.Errorf("query %q lacks %s", q, want)
		}
	}
	// history has no --state: it is always the finished jobs.
	if code, _, _ := f.run("history", "--state", "queued"); code != ExitUsage {
		t.Errorf("--state accepted: exit %d", code)
	}
	if code, _, _ := f.run("history", "--limit", "0"); code != ExitUsage {
		t.Errorf("--limit 0 accepted: exit %d", code)
	}
}

// R5.11: --json prints the rows of the API, unchanged and in the order the API sent them (newest first).
func TestHistoryJSON(t *testing.T) {
	f := newFixture(t)
	rows := []any{jobJSON(3, "luis-mc/app", "newest", "done"), jobJSON(2, "luis-mc/app", "middle", "done"), jobJSON(1, "luis-mc/app", "oldest", "done")}
	f.reply("GET /api/v1/jobs", 200, map[string]any{"items": rows, "stale": map[string]any{"stale": false}})
	code, out, _ := f.run("history", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var page struct {
		Items []struct {
			ID      int    `json:"id"`
			JobName string `json:"job_name"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("not json: %v\n%s", err, out)
	}
	if len(page.Items) != 3 || page.Items[0].JobName != "newest" || page.Items[2].JobName != "oldest" {
		t.Fatalf("rows %+v", page.Items)
	}
	if !strings.Contains(lastJobsCall(f), "state=done") {
		t.Errorf("history asked for %q", lastJobsCall(f))
	}
}
