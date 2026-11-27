package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func trialJSON(mode string) map[string]any {
	return map[string]any{"repository": "luis-mc/app", "mode": mode, "variables": []string{}, "total": 1,
		"hosted": map[string]any{"runs": 1, "succeeded": 1, "avg_wait_sec": 20, "avg_run_sec": 60},
		"crsp":   map[string]any{"runs": 0, "succeeded": 0, "avg_wait_sec": 0, "avg_run_sec": 0},
		"jobs": []map[string]any{{"job": "build", "would_take": 1,
			"hosted": map[string]any{"runs": 1, "succeeded": 1, "avg_wait_sec": 20, "avg_run_sec": 60},
			"crsp":   map[string]any{"runs": 0, "succeeded": 0, "avg_wait_sec": 0, "avg_run_sec": 0}}}}
}

func TestTrialShowAndSet(t *testing.T) { // github-integration R5.66
	f := newFixture(t)
	f.repos(repoJSON(10, "luis-mc/app", true))
	f.handle("GET /api/v1/repositories/10/trial", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, trialJSON("observe"))
	})
	var put map[string]any
	f.handle("PUT /api/v1/repositories/10/trial", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&put)
		writeJSON(w, 200, trialJSON("canary"))
	})
	if code, out, _ := f.run("trial", "luis-mc/app"); code != 0 || f.called("PUT") || !strings.Contains(out, "trial observe") || !strings.Contains(out, "build") {
		t.Fatalf("show: %d %q", code, out)
	}
	if code, out, _ := f.run("trial", "luis-mc/app", "canary", "--percent", "25", "--jobs", "build,lint"); code != 0 ||
		put["mode"] != "canary" || put["percent"] != float64(25) || len(put["jobs"].([]any)) != 2 || !strings.Contains(out, "trial canary") {
		t.Fatalf("set: %d %v %q", code, put, out)
	}
	if code, _, _ := f.run("trial", "luis-mc/app", "wild"); code != ExitUsage {
		t.Fatalf("bad mode exits %d", code)
	}
}
