package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func leftoversFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.machines(machineJSON("build-01", "active", true))
	finding := func(check, subject string, items []any) map[string]any {
		return map[string]any{"check": check, "checked_at": "2026-11-14T10:00:00Z", "fix": map[string]any{"text": "x"}, "milestone": 2, "needs_admin": true,
			"severity": "warn", "subject": map[string]any{"id": subject, "kind": "machine", "name": "n"}, "title": "t", "items": items}
	}
	f.reply("GET /api/v1/doctor/findings", 200, map[string]any{"items": []any{
		finding("machine.runner_leftovers", "m_build-01", []any{
			map[string]any{"kind": "service", "name": "actions.runner.old", "why": "no runner owns it"},
			map[string]any{"kind": "folder", "name": `C:\crsp\runners\old`, "why": "no runner owns it", "size_bytes": int64(5) << 20},
		}),
		finding("machine.runner_leftovers", "m_other", []any{map[string]any{"kind": "account", "name": "other", "why": "x"}}),
		finding("disk.space", "m_build-01", []any{map[string]any{"kind": "account", "name": "unrelated", "why": "x"}}),
	}})
	return f
}

// cli R5.63: the list shows the leftovers of the latest doctor finding of the machine only.
func TestMachineLeftoversLists(t *testing.T) {
	f := leftoversFixture(t)
	code, out, errOut := f.run("machine", "leftovers", "build-01")
	if code != 0 {
		t.Fatalf("%d %q", code, errOut)
	}
	if !strings.Contains(out, "actions.runner.old") || !strings.Contains(out, `C:\crsp\runners\old`) || !strings.Contains(out, "5 MiB") || strings.Contains(out, "other") || strings.Contains(out, "unrelated") {
		t.Fatalf("out %q", out)
	}
	if f.called("POST") {
		t.Fatalf("a list must change nothing: %v", f.calls)
	}
	if code, out, _ := f.run("machine", "leftovers", "build-01", "--json"); code != 0 || !strings.Contains(out, `"items"`) {
		t.Fatalf("json %d %q", code, out)
	}
}

func TestMachineLeftoversListEmpty(t *testing.T) {
	f := newFixture(t)
	f.machines(machineJSON("build-01", "active", true))
	f.reply("GET /api/v1/doctor/findings", 200, map[string]any{"items": []any{}})
	code, _, errOut := f.run("machine", "leftovers", "build-01")
	if code != 0 || !strings.Contains(errOut, "No runner leftovers on build-01") {
		t.Fatalf("%d %q", code, errOut)
	}
}

// Remove prints what goes, calls the operation with the kind of each name, and prints one result per item.
func TestMachineLeftoversRemove(t *testing.T) {
	f := leftoversFixture(t)
	var body map[string]any
	f.handle("POST /api/v1/machines/m_build-01/runner-leftovers/remove", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeJSON(w, 200, map[string]any{"results": []any{
			map[string]any{"kind": "service", "name": "actions.runner.old", "outcome": "removed", "steps": []string{"stopped"}},
			map[string]any{"kind": "folder", "name": `C:\crsp\runners\old`, "outcome": "already_gone", "steps": []string{}},
		}})
	})
	code, out, errOut := f.run("machine", "leftovers", "remove", "build-01", "actions.runner.old", `C:\crsp\runners\old`, "--yes")
	if code != 0 {
		t.Fatalf("%d %q", code, errOut)
	}
	items, _ := body["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["kind"] != "service" || items[1].(map[string]any)["kind"] != "folder" {
		t.Fatalf("body %v", body)
	}
	if !strings.Contains(errOut, "service actions.runner.old") || !strings.Contains(out, "removed service actions.runner.old") || !strings.Contains(out, "already gone folder") {
		t.Fatalf("out %q err %q", out, errOut)
	}
}

// Without --yes and without a terminal nothing is sent (cli R5.14).
func TestMachineLeftoversRemoveNeedsConfirmation(t *testing.T) {
	f := leftoversFixture(t)
	code, _, _ := f.run("machine", "leftovers", "remove", "build-01", "actions.runner.old")
	if code != ExitNotConfirmed || f.called("POST") {
		t.Fatalf("exit %d calls %v", code, f.calls)
	}
}

func TestMachineLeftoversRemoveRefusedAndUnknown(t *testing.T) {
	f := leftoversFixture(t)
	f.reply("POST /api/v1/machines/m_build-01/runner-leftovers/remove", 200, map[string]any{"results": []any{
		map[string]any{"kind": "service", "name": "actions.runner.old", "outcome": "refused", "error": "A runner owns it.", "steps": []string{}},
	}})
	code, out, errOut := f.run("machine", "leftovers", "remove", "build-01", "actions.runner.old", "--yes")
	if code != ExitFailed || !strings.Contains(out, "refused service actions.runner.old: A runner owns it.") || !strings.Contains(errOut, "1 of 1") {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	n := f.count("POST")
	code, _, errOut = f.run("machine", "leftovers", "remove", "build-01", "nothing", "--yes")
	if code != ExitNotFound || f.count("POST") != n || !strings.Contains(errOut, "not in the latest doctor scan") {
		t.Fatalf("%d %q", code, errOut)
	}
	if code, _, _ := f.run("machine", "leftovers", "remove", "build-01"); code != ExitUsage {
		t.Fatalf("usage %d", code)
	}
}
