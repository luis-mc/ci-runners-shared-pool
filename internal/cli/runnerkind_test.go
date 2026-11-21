package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestRepoPolicyRunnerKind(t *testing.T) { // github-integration R5.60
	f := newFixture(t)
	pub := repoJSON(10, "luis-mc/open", true)
	pub["visibility"] = "public"
	pub["policy"].(map[string]any)["runner_kinds"] = "ephemeral_only"
	pub["policy"].(map[string]any)["runner_kinds_set"] = false
	f.repos(pub)
	var body map[string]any
	f.handle("PATCH /api/v1/repositories/10/policy", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		p := repoJSON(10, "luis-mc/open", true)["policy"].(map[string]any)
		p["runner_kinds"], p["runner_kinds_set"] = "any_runner", true
		writeJSON(w, 200, p)
	})
	if code, out, _ := f.run("repo", "policy", "luis-mc/open"); code != 0 || !strings.Contains(out, "Runner type:     Ephemeral only (default)") {
		t.Fatalf("show: %d %q", code, out)
	}
	code, out, e := f.run("repo", "policy", "luis-mc/open", "--runner-kind", "any-runner")
	if code != 0 || body["runner_kinds"] != "any_runner" || !strings.Contains(out, "Any runner (set by you)") {
		t.Fatalf("set: %d %v %q", code, body, out)
	}
	if !strings.Contains(e, "public repository") || !strings.Contains(e, "Warning") {
		t.Fatalf("a public repository set to any runner is warned: %q", e)
	}
	if code, _, _ := f.run("repo", "policy", "luis-mc/open", "--runner-kind", "default"); code != 0 || body["runner_kinds"] != "default" {
		t.Fatalf("default: %v", body)
	}
	if code, _, _ := f.run("repo", "policy", "luis-mc/open", "--runner-kind", "nope"); code != ExitUsage {
		t.Fatal("an unknown kind is a usage error")
	}
}

func TestRunnerAddKindAndKeepDependencies(t *testing.T) { // runners R5.89
	f := newFixture(t)
	f.machines(machineJSON("build-01", "active", true))
	var req map[string]any
	f.handle("POST /api/v1/runner-plans", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&req)
		p := planJSON("planned", false)
		p["area"] = "runners"
		writeJSON(w, 201, p)
	})
	f.reply("POST /api/v1/runner-plans/p1/apply", 202, planJSON("done", false))
	base := []string{"runner", "add", "--machine", "build-01", "--os", "linux", "--backend", "docker"}
	run := func(extra ...string) (int, string) {
		req = nil
		code, _, e := f.run(append(append([]string{}, base...), extra...)...)
		return code, e
	}
	if code, e := run("--persistent", "--no-keep-dependencies"); code != 0 || req["kind"] != "persistent" || req["keep_dependencies"] != false {
		t.Fatalf("persistent off: %d %v %q", code, req, e)
	}
	if code, e := run("--persistent", "--keep-dependencies"); code != 0 || req["keep_dependencies"] != true {
		t.Fatalf("persistent on: %d %v %q", code, req, e)
	}
	if code, e := run("--persistent"); code != 0 {
		t.Fatalf("persistent default: %d %v %q", code, req, e)
	} else if _, has := req["keep_dependencies"]; has {
		t.Fatalf("the controller picks the default: %v", req)
	}
	if code, e := run("--ephemeral"); code != 0 || req["kind"] != "ephemeral" {
		t.Fatalf("ephemeral: %d %v %q", code, req, e)
	}
	if code, e := run("--ephemeral", "--keep-dependencies"); code != ExitUsage || !strings.Contains(e, "persistent") {
		t.Fatalf("keep with ephemeral is refused: %d %q", code, e)
	}
	if code, _ := run("--persistent", "--ephemeral"); code != ExitUsage {
		t.Fatal("both kinds")
	}
	if code, _ := run("--persistent", "--keep-dependencies", "--no-keep-dependencies"); code != ExitUsage {
		t.Fatal("both keep flags")
	}
}

func TestRunnerUpdateAndClearCache(t *testing.T) { // runners R5.89
	f := newFixture(t)
	f.machines(machineJSON("build-01", "active", true))
	pers := runnerJSON("pool-1", "m_build-01", nil)
	pers["kind"] = "persistent"
	pers["keep_dependencies"] = true
	pers["unclean_reason"] = "could not remove /tmp/x"
	eph := runnerJSON("eph-1", "m_build-01", nil)
	f.reply("GET /api/v1/runners", 200, map[string]any{"items": []any{pers, eph}})
	code, out, _ := f.run("runner", "list")
	if code != 0 || !strings.Contains(out, "unclean") {
		t.Fatalf("list: %d %q", code, out)
	}
	var body map[string]any
	f.handle("PATCH /api/v1/runners/r_pool-1/settings", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeJSON(w, 200, pers)
	})
	if code, _, e := f.run("runner", "update", "pool-1", "--no-keep-dependencies"); code != 0 || body["keep_dependencies"] != false || !strings.Contains(e, "always removed") && !strings.Contains(e, "removes downloaded") {
		t.Fatalf("update: %d %v %q", code, body, e)
	}
	if code, _, _ := f.run("runner", "update", "pool-1"); code != ExitUsage {
		t.Fatal("nothing to change")
	}
	if code, _, _ := f.run("runner", "update", "eph-1", "--keep-dependencies"); code != ExitUsage {
		t.Fatal("an ephemeral runner has nothing to keep")
	}
	f.reply("POST /api/v1/runners/r_pool-1/clear-cache", 202, pers)
	if code, _, _ := f.run("runner", "clear-cache", "pool-1"); code != ExitNotConfirmed || f.called("POST /api/v1/runners/r_pool-1/clear-cache") {
		t.Fatal("clear-cache asks first")
	}
	if code, _, e := f.run("runner", "clear-cache", "pool-1", "--yes"); code != 0 || !f.called("POST /api/v1/runners/r_pool-1/clear-cache") || !strings.Contains(e, "pool-1") {
		t.Fatalf("clear: %d %q", code, e)
	}
	if code, _, _ := f.run("runner", "clear-cache", "eph-1", "--yes"); code != ExitUsage {
		t.Fatal("an ephemeral runner has no cache")
	}
}
