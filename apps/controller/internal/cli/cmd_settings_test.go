package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestSettingsPublicRepositories(t *testing.T) { // github-integration R5.54, R5.55
	f := newFixture(t)
	enabled, onPool := false, 2
	var puts []map[string]any
	f.handle("GET /api/v1/settings/public-repositories", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"enabled": enabled, "public_on_pool": onPool})
	})
	f.handle("PUT /api/v1/settings/public-repositories", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		puts = append(puts, b)
		stopped := 0
		if b["enabled"] == false {
			stopped, onPool = onPool, 0
		}
		enabled = b["enabled"] == true
		writeJSON(w, 200, map[string]any{"enabled": enabled, "public_on_pool": onPool, "stopped": stopped})
	})
	if code, out, _ := f.run("settings", "public-repositories"); code != 0 || !strings.Contains(out, "off") || !strings.Contains(out, "2 public repositories are on the pool") {
		t.Fatalf("show: %d %q", code, out)
	}
	if code, _, _ := f.run("settings", "public-repositories", "--on", "--off"); code != ExitUsage {
		t.Fatalf("both flags exit %d", code)
	}
	if code, out, _ := f.run("settings", "public-repositories", "--on"); code != 0 || len(puts) != 1 || puts[0]["enabled"] != true || !strings.Contains(out, "on") {
		t.Fatalf("on: %d %v %q", code, puts, out)
	}
	// Off names how many lose the pool and needs a yes without a terminal.
	if code, _, _ := f.run("settings", "public-repositories", "--off"); code != ExitNotConfirmed || len(puts) != 1 {
		t.Fatalf("off without yes: %d %v", code, puts)
	}
	code, out, _ := f.run("settings", "public-repositories", "--off", "--yes")
	if code != 0 || len(puts) != 2 || puts[1]["enabled"] != false || !strings.Contains(out, "2 repositories stopped") {
		t.Fatalf("off: %d %v %q", code, puts, out)
	}
}

func TestRepoEnablePublicRefusalPrintsTheReason(t *testing.T) { // github-integration R5.55
	f := newFixture(t)
	f.repos(repoJSON(10, "luis-mc/open", false))
	reason := "luis-mc/open is a public repository, and the setting Public repositories is off, so crsp does not serve it. Turn the setting on first."
	f.reply("POST /api/v1/repository-plans", 409, apiErr("public_repositories_off", reason))
	code, _, errOut := f.run("repo", "enable", "luis-mc/open")
	if code != ExitConflict || !strings.Contains(errOut, reason) {
		t.Fatalf("%d %q", code, errOut)
	}
}
