package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func updateJSON(available bool, jobs ...map[string]any) map[string]any {
	rj := []any{}
	for _, j := range jobs {
		rj = append(rj, j)
	}
	return map[string]any{
		"current_version": "1.4.0", "latest_version": "1.5.0", "update_available": available,
		"state": "idle", "auto_update": true, "running_jobs": rj,
	}
}

func TestUpdateStatusPrints(t *testing.T) {
	f := newFixture(t)
	f.reply("GET /api/v1/update", 200, updateJSON(true))
	code, out, _ := f.run("update", "status")
	if code != 0 || !strings.Contains(out, "1.4.0") || !strings.Contains(out, "1.5.0") || !strings.Contains(out, "Automatic updates") {
		t.Fatalf("%d %q", code, out)
	}
}

func TestUpdateApplyNamesJobsAndNeedsYes(t *testing.T) {
	f := newFixture(t)
	f.reply("GET /api/v1/update", 200, updateJSON(true, map[string]any{"repository": "luis-mc/app", "workflow": "ci", "run_id": 7, "job": "test"}))
	f.reply("POST /api/v1/update/apply", 202, updateJSON(true))
	// No terminal and no --yes: exit 5, the job is named, nothing is sent.
	code, _, errOut := f.run("update", "apply")
	if code != ExitNotConfirmed || f.called("POST") || !strings.Contains(errOut, "luis-mc/app ci, run 7, job test") || !strings.Contains(errOut, "No running job is stopped") {
		t.Fatalf("%d %v %q", code, f.calls, errOut)
	}
	if code, _, e := f.run("update", "apply", "--yes"); code != 0 || !f.called("POST /api/v1/update/apply?confirmed=true") {
		t.Fatalf("%d %v %q", code, f.calls, e)
	}
}

func TestUpdateApplyWhenCurrentDoesNothing(t *testing.T) {
	f := newFixture(t)
	f.reply("GET /api/v1/update", 200, updateJSON(false))
	code, _, errOut := f.run("update", "apply", "--yes")
	if code != 0 || f.called("POST") || !strings.Contains(errOut, "Nothing to update") {
		t.Fatalf("%d %v %q", code, f.calls, errOut)
	}
}

func TestRestartAsksThenSendsConfirmed(t *testing.T) {
	f := newFixture(t)
	f.reply("GET /api/v1/update", 200, updateJSON(false))
	f.reply("POST /api/v1/service/restart", 202, updateJSON(false))
	if code, _, _ := f.run("restart"); code != ExitNotConfirmed || f.called("POST") {
		t.Fatalf("%d %v", code, f.calls)
	}
	if code, _, _ := f.run("restart", "--yes"); code != 0 || !f.called("POST /api/v1/service/restart?confirmed=true") {
		t.Fatalf("%d %v", code, f.calls)
	}
}

func TestUpdateAutoTogglesTheSetting(t *testing.T) {
	f := newFixture(t)
	var body map[string]any
	f.handle("PATCH /api/v1/settings", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeJSON(w, 200, body)
	})
	if code, _, e := f.run("update", "auto", "off"); code != 0 || body["auto_update"] != false || !strings.Contains(e, "are off") {
		t.Fatalf("%d %v %q", code, body, e)
	}
	if code, _, _ := f.run("update", "auto", "on"); code != 0 || body["auto_update"] != true {
		t.Fatalf("%d %v", code, body)
	}
	if code, _, _ := f.run("update", "auto", "maybe"); code != ExitUsage {
		t.Fatalf("%d", code)
	}
}
