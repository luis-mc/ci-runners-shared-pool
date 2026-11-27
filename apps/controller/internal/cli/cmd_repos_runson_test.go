package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestReposRunsOnPR(t *testing.T) { // github-integration R5.113
	f := newFixture(t)
	f.repos(repoJSON(10, "luis-mc/app", true))
	f.handle("GET /api/v1/runs-on-pr", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"items": []map[string]any{{"repository_id": 10, "full_name": "luis-mc/app", "state": "change",
			"files": []map[string]any{{"path": "ci.yml", "changes": 1, "diff": "d"}}, "diff": "-old\n+new\n", "skipped": []string{}}}})
	})
	var body map[string]any
	f.handle("POST /api/v1/runs-on-pr", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeJSON(w, 200, map[string]any{"items": []map[string]any{{"repository_id": 10, "full_name": "luis-mc/app", "state": "opened", "number": 3, "url": "https://example.test/pr/3"}}})
	})
	if code, out, _ := f.run("repos", "runs-on-pr", "--repo", "luis-mc/app", "--preview"); code != 0 || f.called("POST") || !strings.Contains(out, "+new") {
		t.Fatalf("preview: %d %q", code, out)
	}
	if code, out, _ := f.run("repos", "runs-on-pr", "--repo", "luis-mc/app"); code != 0 || body["repository_id"] != float64(10) || !strings.Contains(out, "https://example.test/pr/3") {
		t.Fatalf("open: %d %v %q", code, body, out)
	}
	if code, _, _ := f.run("repos", "runs-on-pr", "--all"); code != 0 || body["all"] != true {
		t.Fatalf("all: %d %v", code, body)
	}
	if code, _, _ := f.run("repos", "runs-on-pr"); code != ExitUsage {
		t.Fatalf("no target exits %d", code)
	}
}
