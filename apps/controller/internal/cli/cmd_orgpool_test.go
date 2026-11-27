package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func orgPoolJSON(id int64, login string, org, enabled bool) map[string]any {
	return map[string]any{"installation_id": id, "organization": login, "is_organization": org, "eligible": org, "enabled": enabled,
		"group_name": "crsp-home", "repositories": []string{"acme/api"}}
}

func TestOrgPoolListOnOff(t *testing.T) { // github-integration R5.21
	f := newFixture(t)
	f.handle("GET /api/v1/github/org-pools", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"items": []map[string]any{orgPoolJSON(10, "alice", false, false), orgPoolJSON(20, "acme", true, false)}})
	})
	var put map[string]any
	f.handle("PUT /api/v1/github/installations/20/org-pool", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&put)
		writeJSON(w, 200, orgPoolJSON(20, "acme", put["enabled"] == true, put["enabled"] == true))
	})
	if code, out, _ := f.run("org-pool", "list"); code != 0 || !strings.Contains(out, "acme") || !strings.Contains(out, "personal account") {
		t.Fatalf("list: %d %q", code, out)
	}
	if code, out, _ := f.run("org-pool", "on", "ACME"); code != 0 || put["enabled"] != true || !strings.Contains(out, "crsp-home") {
		t.Fatalf("on: %d %v %q", code, put, out)
	}
	if code, out, _ := f.run("org-pool", "off", "acme"); code != 0 || put["enabled"] != false || !strings.Contains(out, "repository runners again") {
		t.Fatalf("off: %d %v %q", code, put, out)
	}
	// A personal account is refused before any change; an unknown name is not found.
	if code, _, _ := f.run("org-pool", "on", "alice"); code != ExitUsage {
		t.Fatalf("personal account exits %d", code)
	}
	if code, _, _ := f.run("org-pool", "on", "nobody"); code != ExitNotFound {
		t.Fatalf("unknown exits %d", code)
	}
	if code, _, _ := f.run("org-pool", "on"); code != ExitUsage {
		t.Fatalf("no name exits %d", code)
	}
}
