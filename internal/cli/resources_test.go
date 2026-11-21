package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func machineJSON(name, state string, online bool, jobs ...map[string]any) map[string]any {
	rj := []any{}
	for _, j := range jobs {
		rj = append(rj, j)
	}
	return map[string]any{
		"id": "m_" + name, "name": name, "os": "linux", "arch": "x64", "state": state, "online": online, "is_controller": false,
		"backends":   []any{map[string]any{"name": "docker", "present": true, "usable": true, "allowed": true}},
		"settings":   map[string]any{"budget_cpu_pct": 60, "budget_memory_pct": 85, "drain_timeout_minutes": 2, "heavy_slots": 1, "job_slots": 2, "light_slots": 1, "windows_symlink_right": false, "allowed_backends": []string{"docker"}},
		"load":       map[string]any{"memory_pct": 41.5, "cpu_pct": 12.0, "memory_known": true, "cpu_known": true, "sampled_at": "2026-11-14T10:00:00Z", "stale": false},
		"slots_used": 1, "running_jobs": rj, "last_seen_at": "2026-11-14T10:00:00Z",
	}
}

func jobJSON(id int, repo, name, state string) map[string]any {
	return map[string]any{"id": id, "repository": repo, "run_id": 1, "run_attempt": 1, "github_job_id": id, "job_name": name, "state": state, "labels": []string{"self-hosted"}, "workflow": "ci.yml", "queued_at": "2026-11-14T10:00:00Z"}
}

func repoJSON(id int, full string, on bool) map[string]any {
	owner, name, _ := strings.Cut(full, "/")
	return map[string]any{"id": id, "full_name": full, "owner": owner, "name": name, "installation_id": 1, "visibility": "private", "pool_on": on, "user_is_github_admin": true, "user_admin_known": true,
		"variables": []any{map[string]any{"name": "RUNS_ON_LINUX", "status": "ok"}},
		"policy":    map[string]any{"priority": 0, "max_parallel_jobs": 2, "allow_fork_pull_requests": false, "labels": []string{"self-hosted", "linux"}, "runner_groups": []string{}}}
}

func (f *fixture) machines(ms ...map[string]any) {
	f.reply("GET /api/v1/machines", 200, map[string]any{"items": ms})
}

func (f *fixture) repos(rs ...map[string]any) {
	f.reply("GET /api/v1/github/repositories", 200, map[string]any{"items": rs, "stale": map[string]any{"is_stale": false}})
}

func TestMachineListAndAdd(t *testing.T) {
	f := newFixture(t)
	f.machines(machineJSON("build-01", "active", true), machineJSON("build-02", "active", false))
	code, out, _ := f.run("machine", "list")
	if code != 0 || !strings.Contains(out, "build-01") || !strings.Contains(out, "offline") || !strings.Contains(out, "42%") {
		t.Fatalf("%d %q", code, out)
	}
	if code, out, _ := f.run("machine", "list", "--json"); code != 0 || !strings.Contains(out, `"items"`) {
		t.Fatalf("%q", out)
	}
	f.machines()
	if _, _, e := f.run("machine", "list"); !strings.Contains(e, "crsp machine add") {
		t.Fatalf("empty state: %q", e)
	}
	var ttl float64
	f.handle("POST /api/v1/join-codes", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		ttl, _ = b["ttl_minutes"].(float64)
		writeJSON(w, 201, map[string]any{"code": "JOINCODE-1", "join_code": map[string]any{"id": "j1", "state": "waiting", "created_at": "2026-11-14T10:00:00Z", "expires_at": "2026-11-14T10:20:00Z"},
			"commands": map[string]any{"controller_address": "pool.lan", "agent_port": 8461, "cert_sha256": "ab", "items": []any{map[string]any{"os": "linux", "command": "crsp agent join pool.lan JOINCODE-1"}}}})
	})
	code, out, errOut := f.run("machine", "add", "--expires", "20m", "--name", "box")
	if code != 0 || ttl != 20 || !strings.Contains(out, "crsp agent join pool.lan JOINCODE-1") || !strings.Contains(errOut, "works once") || !strings.Contains(errOut, "2026-11-14 10:20") {
		t.Fatalf("%d ttl=%v %q %q", code, ttl, out, errOut)
	}
	if code, _, _ := f.run("machine", "add", "--expires", "48h"); code != ExitUsage {
		t.Fatalf("expires: %d", code)
	}
}

func TestMachineNotFoundExits4(t *testing.T) {
	f := newFixture(t)
	f.machines(machineJSON("build-01", "active", true))
	if code, _, _ := f.run("machine", "drain", "nope", "--yes"); code != ExitNotFound {
		t.Fatalf("%d", code)
	}
}

func TestMachineResume(t *testing.T) {
	f := newFixture(t)
	f.machines(machineJSON("build-01", "drained", true))
	f.reply("POST /api/v1/machines/m_build-01/resume", 200, machineJSON("build-01", "active", true))
	if code, _, e := f.run("machine", "resume", "build-01"); code != 0 || !strings.Contains(e, "takes jobs again") {
		t.Fatalf("%d %q", code, e)
	}
}

func change(state string, jobs ...map[string]any) map[string]any {
	w := []any{}
	for _, j := range jobs {
		w = append(w, j)
	}
	return map[string]any{"id": "c1", "machine_id": "m_build-01", "kind": "drain", "state": state, "started_at": "2026-11-14T10:00:00Z", "waiting_for": w}
}

func TestDestructiveListsWhatGoes(t *testing.T) {
	f := newFixture(t)
	f.machines(machineJSON("build-01", "active", true, jobJSON(7, "luis-mc/app", "test", "running")))
	f.reply("POST /api/v1/machines/m_build-01/drain", 202, change("done"))
	code, _, errOut := f.run("machine", "drain", "build-01", "--yes")
	if code != 0 || !strings.Contains(errOut, "machine build-01") || !strings.Contains(errOut, "waits for running job: luis-mc/app ci.yml test") {
		t.Fatalf("%d %q", code, errOut)
	}
}

func TestYesFlagAndNoTerminal(t *testing.T) {
	f := newFixture(t)
	f.machines(machineJSON("build-01", "active", true))
	f.reply("DELETE /api/v1/machines/m_build-01", 202, change("done"))
	// No terminal, no --yes: exit 5, nothing sent.
	code, _, errOut := f.run("machine", "remove", "build-01")
	if code != ExitNotConfirmed || f.called("DELETE") || !strings.Contains(errOut, "machine build-01") {
		t.Fatalf("%d %v %q", code, f.calls, errOut)
	}
	// Terminal: a wrong answer exits 5; yes goes on.
	f.terminal, f.stdin = true, "no\n"
	if code, _, e := f.run("machine", "remove", "build-01"); code != ExitNotConfirmed || f.called("DELETE") || !strings.Contains(e, "Type yes to go on:") {
		t.Fatalf("%d %q", code, e)
	}
	f.stdin = "yes\n"
	if code, _, _ := f.run("machine", "remove", "build-01"); code != 0 || !f.called("DELETE /api/v1/machines/m_build-01?confirm=true") {
		t.Fatalf("%d %v", code, f.calls)
	}
}

func TestWaitsForJobsAndTimeout(t *testing.T) {
	f := newFixture(t)
	job := jobJSON(7, "luis-mc/app", "test", "running")
	f.machines(machineJSON("build-01", "active", true, job))
	f.reply("POST /api/v1/machines/m_build-01/drain", 202, change("waiting", job))
	var polls atomic.Int32
	f.handle("GET /api/v1/machines/m_build-01/changes/c1", func(w http.ResponseWriter, r *http.Request) {
		if polls.Add(1) >= 2 {
			writeJSON(w, 200, change("done"))
			return
		}
		writeJSON(w, 200, change("waiting", job))
	})
	code, _, errOut := f.run("machine", "drain", "build-01", "--yes")
	if code != 0 || !strings.Contains(errOut, "Waiting for 1 jobs to end on build-01") || !strings.Contains(errOut, "drained") {
		t.Fatalf("%d %q", code, errOut)
	}
}

func TestViewerChangeExits3(t *testing.T) {
	f := newFixture(t)
	f.machines(machineJSON("build-01", "active", true))
	f.reply("POST /api/v1/machines/m_build-01/drain", 403, apiErr("forbidden", "Draining a machine needs the admin role."))
	code, _, errOut := f.run("machine", "drain", "build-01", "--yes")
	if code != ExitAuth || !strings.Contains(errOut, "needs the admin role") {
		t.Fatalf("%d %q", code, errOut)
	}
}

func planJSON(state string, needsYes bool) map[string]any {
	return map[string]any{"id": "p1", "area": "repositories", "state": state, "created_at": "2026-11-14T10:00:00Z", "created_by": "luis-mc",
		"steps": []any{map[string]any{"id": "s1", "action": "pool_off", "text": "Delete variable RUNS_ON_LINUX", "needs_yes": needsYes, "state": "planned"}}}
}

func TestRepoCommands(t *testing.T) {
	f := newFixture(t)
	f.repos(repoJSON(10, "luis-mc/app", false), repoJSON(11, "luis-mc/web", true))
	code, out, _ := f.run("repo", "list")
	if code != 0 || !strings.Contains(out, "luis-mc/app") || !strings.Contains(out, "self-hosted,linux") {
		t.Fatalf("%d %q", code, out)
	}
	if code, out, _ := f.run("repo", "list", "--pool", "on", "--q", "web", "--json"); code != 0 || !f.called("GET /api/v1/github/repositories?pool=on&q=web") || !strings.Contains(out, "luis-mc/app") {
		t.Fatalf("%d %v", code, f.calls)
	}
	if code, _, _ := f.run("repo", "list", "--pool", "maybe"); code != ExitUsage {
		t.Fatal("bad --pool")
	}

	var planBody map[string]any
	f.handle("POST /api/v1/repository-plans", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&planBody)
		writeJSON(w, 201, planJSON("planned", false))
	})
	f.reply("POST /api/v1/repository-plans/p1/apply", 202, planJSON("done", false))
	code, _, errOut := f.run("repo", "enable", "luis-mc/app")
	ch := planBody["changes"].([]any)[0].(map[string]any)
	if code != 0 || ch["pool"] != "on" || ch["repository_id"] != float64(10) || !strings.Contains(errOut, "Pool on for luis-mc/app") {
		t.Fatalf("%d %v %q", code, planBody, errOut)
	}
	// Disable asks first.
	if code, _, _ := f.run("repo", "disable", "luis-mc/web"); code != ExitNotConfirmed {
		t.Fatalf("disable without yes: %d", code)
	}
	n := f.count("POST /api/v1/repository-plans/p1/apply")
	if code, _, _ := f.run("repo", "disable", "luis-mc/web", "--yes"); code != 0 || f.count("POST /api/v1/repository-plans/p1/apply") != n+1 {
		t.Fatal("disable --yes")
	}
	if code, _, _ := f.run("repo", "enable", "luis-mc/missing"); code != ExitNotFound {
		t.Fatalf("missing repo: %d", code)
	}
	if code, _, _ := f.run("repo", "enable", "noslash"); code != ExitUsage {
		t.Fatal("bad name")
	}
}

func TestRepoPlanOutcomes(t *testing.T) {
	f := newFixture(t)
	f.repos(repoJSON(10, "luis-mc/app", false))
	f.reply("POST /api/v1/repository-plans", 201, planJSON("planned", false))
	f.reply("POST /api/v1/repository-plans/p1/apply", 202, planJSON("applying", false))
	var n atomic.Int32
	f.handle("GET /api/v1/repository-plans/p1", func(w http.ResponseWriter, r *http.Request) {
		switch n.Add(1) {
		case 1:
			writeJSON(w, 200, planJSON("stale", false))
		}
	})
	code, _, e := f.run("repo", "enable", "luis-mc/app")
	if code != ExitConflict || !strings.Contains(e, "Nothing changed") {
		t.Fatalf("%d %q", code, e)
	}
}

func TestForkWarning(t *testing.T) {
	f := newFixture(t)
	f.repos(repoJSON(10, "luis-mc/app", true))
	var body map[string]any
	f.handle("PATCH /api/v1/repositories/10/policy", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		p := repoJSON(10, "luis-mc/app", true)["policy"].(map[string]any)
		p["allow_fork_pull_requests"] = true
		writeJSON(w, 200, p)
	})
	code, _, errOut := f.run("repo", "policy", "luis-mc/app", "--allow-fork-prs")
	if code != ExitNotConfirmed || !strings.Contains(errOut, "Fork pull requests run code from people outside your team") || f.called("PATCH") {
		t.Fatalf("%d %q", code, errOut)
	}
	code, out, _ := f.run("repo", "policy", "luis-mc/app", "--allow-fork-prs", "--yes", "--labels", "self-hosted,linux", "--max-parallel", "3", "--priority", "-5")
	if code != 0 || body["confirm_fork_risk"] != true || body["allow_fork_pull_requests"] != true || body["max_parallel_jobs"] != float64(3) || body["priority"] != float64(-5) || !strings.Contains(out, "Fork PRs:        on") {
		t.Fatalf("%d %v %q", code, body, out)
	}
	// Without flags it only shows.
	n := f.count("PATCH")
	if code, out, _ := f.run("repo", "policy", "luis-mc/app"); code != 0 || f.count("PATCH") != n || !strings.Contains(out, "Max parallel:    2") {
		t.Fatalf("show: %d %q", code, out)
	}
}

func TestRepoPackages(t *testing.T) { // runners R5.51
	f := newFixture(t)
	f.repos(repoJSON(10, "luis-mc/app", true))
	var put map[string]any
	f.handle("GET /api/v1/repositories/10/extra-packages", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"packages": []string{"zlib1g-dev"}, "text": "zlib1g-dev"})
	})
	f.handle("PUT /api/v1/repositories/10/extra-packages", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&put)
		writeJSON(w, 200, map[string]any{"packages": []string{"libc++-dev", "zlib1g-dev"}, "text": "libc++-dev\nzlib1g-dev"})
	})
	if code, out, _ := f.run("repo", "packages", "luis-mc/app"); code != 0 || !strings.Contains(out, "zlib1g-dev") || f.called("PUT") {
		t.Fatalf("show: %d %q", code, out)
	}
	code, out, errOut := f.run("repo", "packages", "luis-mc/app", "--set", "zlib1g-dev, libc++-dev")
	if code != 0 || put["text"] != "zlib1g-dev\nlibc++-dev" || !strings.Contains(out, "libc++-dev, zlib1g-dev") || !strings.Contains(errOut, "saved") {
		t.Fatalf("set: %d %v %q %q", code, put, out, errOut)
	}
}

func runnerJSON(name, machine string, job map[string]any) map[string]any {
	r := map[string]any{"id": "r_" + name, "name": name, "machine_id": machine, "os": "linux", "arch": "x64", "kind": "ephemeral", "backend": "docker", "isolation": "container", "labels": []string{"self-hosted"},
		"scope": map[string]any{"kind": "repository", "repository_id": 10}, "state": "idle", "managed": true, "keeps_state": false, "created_at": "2026-11-14T08:00:00Z"}
	if job != nil {
		r["current_job"] = job
	}
	return r
}

func TestRunnerListAndRemove(t *testing.T) {
	f := newFixture(t)
	f.machines(machineJSON("build-01", "active", true))
	f.reply("GET /api/v1/runners", 200, map[string]any{"items": []any{runnerJSON("linux-03", "m_build-01", jobJSON(7, "luis-mc/app", "test", "running"))}})
	code, out, _ := f.run("runner", "list")
	if code != 0 || !strings.Contains(out, "linux-03") || !strings.Contains(out, "build-01") || !strings.Contains(out, "test") || !strings.Contains(out, "4 h") {
		t.Fatalf("%d %q", code, out)
	}
	f.reply("DELETE /api/v1/runners/r_linux-03", 202, runnerJSON("linux-03", "m_build-01", nil))
	var gets atomic.Int32
	f.handle("GET /api/v1/runners/r_linux-03", func(w http.ResponseWriter, r *http.Request) {
		if gets.Add(1) > 1 {
			writeJSON(w, 404, apiErr("not_found", "No such runner."))
			return
		}
		writeJSON(w, 200, runnerJSON("linux-03", "m_build-01", nil))
	})
	if code, _, e := f.run("runner", "remove", "linux-03"); code != ExitNotConfirmed || !strings.Contains(e, "runner linux-03") || f.called("DELETE") {
		t.Fatalf("%d %q", code, e)
	}
	code, _, e := f.run("runner", "remove", "linux-03", "--yes")
	if code != 0 || !strings.Contains(e, "Runner linux-03 removed.") || !f.called("DELETE /api/v1/runners/r_linux-03?confirm=true") {
		t.Fatalf("%d %q %v", code, e, f.calls)
	}
	if code, _, _ := f.run("runner", "remove", "nope", "--yes"); code != ExitNotFound {
		t.Fatal("missing runner")
	}
}

func TestRunnerCreateRefusesUnsupported(t *testing.T) {
	f := newFixture(t)
	win := machineJSON("win-01", "active", true)
	win["os"] = "windows"
	f.machines(machineJSON("build-01", "active", true), win)
	f.repos(repoJSON(10, "luis-mc/app", true))
	code, _, e := f.run("runner", "add", "--machine", "build-01", "--os", "windows", "--repo", "luis-mc/app")
	if code != ExitUsage || !strings.Contains(e, "These do: win-01") || f.called("POST /api/v1/runner-plans") {
		t.Fatalf("%d %q", code, e)
	}
	code, _, e = f.run("runner", "add", "--machine", "build-01", "--os", "linux", "--backend", "kvm", "--repo", "luis-mc/app")
	if code != ExitUsage || !strings.Contains(e, "No machine supports it") {
		t.Fatalf("%d %q", code, e)
	}
	// A supported request plans, then applies.
	var req map[string]any
	f.handle("POST /api/v1/runner-plans", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&req)
		p := planJSON("planned", false)
		p["area"] = "runners"
		writeJSON(w, 201, p)
	})
	f.reply("POST /api/v1/runner-plans/p1/apply", 202, planJSON("done", false))
	code, _, e = f.run("runner", "add", "--machine", "build-01", "--os", "linux", "--backend", "docker", "--persistent", "--count", "2", "--repo", "luis-mc/app")
	if code != 0 || req["kind"] != "persistent" || req["count"] != float64(2) || req["machine_id"] != "m_build-01" || !strings.Contains(e, "Runners added") {
		t.Fatalf("%d %v %q", code, req, e)
	}
	// OPS23: no repository asked, none sent; the controller adds pool runners.
	req = nil
	code, _, e = f.run("runner", "add", "--machine", "build-01", "--os", "linux", "--backend", "docker", "--persistent")
	if _, has := req["scope"]; code != 0 || has {
		t.Fatalf("without --repo the request has no scope: %d %v %q", code, req, e)
	}
	if code, _, _ := f.run("runner", "add", "--os", "linux"); code != ExitUsage {
		t.Fatal("missing --machine")
	}
	// The verb is add, never create.
	if code, _, _ := f.run("runner", "create"); code != ExitUsage {
		t.Fatal("create must not exist")
	}
}

func TestJobListFilters(t *testing.T) {
	f := newFixture(t)
	f.repos(repoJSON(10, "luis-mc/app", true))
	f.machines(machineJSON("build-01", "active", true))
	f.reply("GET /api/v1/jobs", 200, map[string]any{"items": []any{jobJSON(1, "luis-mc/app", "test", "queued")}, "stale": map[string]any{"is_stale": false}})
	code, out, _ := f.run("job", "list", "--repo", "luis-mc/app", "--machine", "build-01", "--result", "failure", "--since", "24h", "--state", "done", "--limit", "20")
	if code != 0 || !strings.Contains(out, "luis-mc/app") {
		t.Fatalf("%d %q", code, out)
	}
	var q string
	for _, c := range f.calls {
		if strings.HasPrefix(c, "GET /api/v1/jobs") {
			q = c
		}
	}
	for _, want := range []string{"repository_id=10", "machine_id=m_build-01", "result=failure", "state=done", "limit=20", "from=2026-"} {
		if !strings.Contains(q, want) {
			t.Errorf("query %q lacks %s", q, want)
		}
	}
	if code, _, _ := f.run("job", "list", "--limit", "0"); code != ExitUsage {
		t.Fatal("limit")
	}
}

func TestJobList1000(t *testing.T) {
	f := newFixture(t)
	var all []any
	for i := 0; i < 1000; i++ {
		all = append(all, jobJSON(i, "luis-mc/app", fmt.Sprintf("job-%d", i), "done"))
	}
	f.reply("GET /api/v1/jobs", 200, map[string]any{"items": all, "stale": map[string]any{"is_stale": false}})
	code, out, _ := f.run("job", "list", "--limit", "500", "--json")
	if code != 0 || !strings.Contains(out, "job-999") {
		t.Fatalf("%d", code)
	}
}

func TestWatchNDJSON(t *testing.T) {
	f := newFixture(t)
	var conn atomic.Int32
	var lastID atomic.Value
	f.handle("GET /api/v1/events", func(w http.ResponseWriter, r *http.Request) {
		n := conn.Add(1)
		lastID.Store(r.Header.Get("Last-Event-ID"))
		w.Header().Set("Content-Type", "text/event-stream")
		ev := func(id, typ string, j map[string]any) {
			b, _ := json.Marshal(map[string]any{"id": id, "type": typ, "at": "2026-11-14T10:00:05Z", "job": j})
			_, _ = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", id, typ, b)
		}
		if n == 1 {
			_, _ = fmt.Fprint(w, ": keep-alive\n\n")
			ev("1", "job.queued", jobJSON(1, "luis-mc/app", "test", "queued"))
			return // the stream drops
		}
		ev("2", "job.started", jobJSON(1, "luis-mc/app", "test", "running"))
		w.(http.Flusher).Flush()
		<-r.Context().Done() // the CLI ends the stream after Ctrl+C
	})
	code, out, errOut := f.runUntilPrinted(`"job.started"`, "job", "watch", "--json")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if code != 0 || len(lines) != 2 || !strings.Contains(lines[0], `"job.queued"`) || !strings.Contains(lines[1], `"job.started"`) {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	for _, l := range lines {
		var v map[string]any
		if json.Unmarshal([]byte(l), &v) != nil {
			t.Fatalf("not one JSON value per line: %q", l)
		}
	}
	if lastID.Load() != "1" || conn.Load() != 2 {
		t.Fatalf("reconnect with Last-Event-ID: %v after %d connections", lastID.Load(), conn.Load())
	}
}

func TestWatchReconnects(t *testing.T) {
	f := newFixture(t)
	var conn atomic.Int32
	f.handle("GET /api/v1/events", func(w http.ResponseWriter, r *http.Request) {
		if conn.Add(1) == 1 {
			return
		}
		b, _ := json.Marshal(map[string]any{"id": "9", "type": "job.finished", "at": "2026-11-14T10:00:05Z", "job": jobJSON(1, "luis-mc/app", "test", "done")})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		w.(http.Flusher).Flush()
		<-r.Context().Done() // the CLI ends the stream after Ctrl+C
	})
	code, out, errOut := f.runUntilPrinted("job.finished", "job", "watch")
	if code != 0 || !strings.Contains(errOut, "Reconnecting") || !strings.Contains(out, "job.finished") || !strings.Contains(out, "luis-mc/app test") {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	// A refusal does not loop.
	f2 := newFixture(t)
	f2.reply("GET /api/v1/events", 403, apiErr("forbidden", "no"))
	if code, _, _ := f2.run("job", "watch"); code != ExitAuth {
		t.Fatalf("403: %d", code)
	}
}

func doctorRun(state string, sev ...string) map[string]any {
	fs := []any{}
	for i, s := range sev {
		fs = append(fs, map[string]any{"check": "c", "checked_at": "2026-11-14T10:00:00Z", "fix": map[string]any{"text": "Free some disk space.", "command": "docker system prune"}, "milestone": 2, "needs_admin": false, "severity": s,
			"subject": map[string]any{"id": "m1", "kind": "machine", "name": fmt.Sprintf("build-%d", i)}, "title": "Disk space"})
	}
	return map[string]any{"id": "d1", "state": state, "trigger": "manual", "started_at": "2026-11-14T10:00:00Z", "counts": map[string]int{}, "findings": fs}
}

func TestFindingsAndExitCode(t *testing.T) {
	f := newFixture(t)
	f.machines(machineJSON("build-01", "active", true))
	var scope map[string]any
	f.handle("POST /api/v1/doctor/runs", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&scope)
		writeJSON(w, 202, doctorRun("running"))
	})
	f.reply("GET /api/v1/doctor/runs/d1", 200, doctorRun("done", "ok", "warn"))
	code, out, errOut := f.run("doctor", "--machine", "build-01")
	if code != ExitFindings || !strings.Contains(out, "Disk space") || !strings.Contains(errOut, "Free some disk space. Run: docker system prune") || scope["machines"].([]any)[0] != "m_build-01" {
		t.Fatalf("%d %q %q %v", code, out, errOut, scope)
	}
	f.reply("GET /api/v1/doctor/runs/d1", 200, doctorRun("done", "ok", "info"))
	if code, _, e := f.run("doctor"); code != 0 || !strings.Contains(e, "All checks passed") {
		t.Fatalf("clean: %d %q", code, e)
	}
	if code, out, _ := f.run("doctor", "--json"); code != 0 || !strings.Contains(out, `"findings"`) {
		t.Fatalf("json: %d", code)
	}
}

func TestBackupRestoreOwnerOnly(t *testing.T) {
	f := newFixture(t)
	f.reply("POST /api/v1/backups", 202, map[string]any{"id": "b1", "file": "crsp-2026-11-14.db", "state": "writing", "created_at": "2026-11-14T10:00:00Z"})
	if code, _, e := f.run("backup"); code != 0 || !strings.Contains(e, "b1") {
		t.Fatalf("%d %q", code, e)
	}
	f.reply("GET /api/v1/backups", 200, map[string]any{"items": []any{map[string]any{"id": "b1", "file": "crsp-2026-11-14.db", "state": "good", "created_at": "2026-11-14T10:00:00Z"}}})
	f.reply("POST /api/v1/backups/b1/restore", 403, apiErr("forbidden", "Only the owner can restore a backup."))
	if code, _, e := f.run("restore", "b1"); code != ExitNotConfirmed || !strings.Contains(e, "backup b1") || f.called("POST /api/v1/backups/b1/restore") {
		t.Fatalf("%d %q", code, e)
	}
	code, _, e := f.run("restore", "crsp-2026-11-14.db", "--yes")
	if code != ExitAuth || !strings.Contains(e, "Only the owner") || !f.called("POST /api/v1/backups/b1/restore?confirm=true") {
		t.Fatalf("%d %q", code, e)
	}
	f.reply("POST /api/v1/backups/b1/restore", 202, map[string]any{"id": "b1", "file": "x", "state": "good", "created_at": "2026-11-14T10:00:00Z"})
	if code, _, _ := f.run("restore", "b1", "--yes"); code != 0 {
		t.Fatalf("owner restore: %d", code)
	}
	if code, _, _ := f.run("restore", "nope", "--yes"); code != ExitNotFound {
		t.Fatal("unknown backup")
	}
}

func TestRunnerEnvShowsMaskedMergedValues(t *testing.T) { // runners R5.75, R5.76
	f := newFixture(t)
	f.machines(machineJSON("build-01", "active", true))
	f.reply("GET /api/v1/runners", 200, map[string]any{"items": []any{runnerJSON("linux-03", "m_build-01", nil)}})
	f.reply("GET /api/v1/runners/r_linux-03/env", 200, map[string]any{"runner_id": "r_linux-03", "vars": []any{
		map[string]any{"name": "DOTNET_ROOT", "value": "/opt/dotnet", "sensitive": false, "source": "set:dotnet-8"},
		map[string]any{"name": "FEED_LOGIN", "value": "***", "sensitive": true, "source": "set:dotnet-8"},
	}})
	code, out, _ := f.run("runner", "env", "linux-03")
	if code != 0 || !strings.Contains(out, "DOTNET_ROOT") || !strings.Contains(out, "set:dotnet-8") || !strings.Contains(out, "***") {
		t.Fatalf("%d %q", code, out)
	}
	if code, _, e := f.run("runner", "env", "nope"); code != ExitNotFound || !strings.Contains(e, "No runner") {
		t.Fatalf("%d %q", code, e)
	}
	if code, _, _ := f.run("runner", "env"); code != ExitUsage {
		t.Fatalf("usage: %d", code)
	}
}

func TestCompareTableAndJSON(t *testing.T) {
	f := newFixture(t)
	side := func(q, r float64) map[string]any {
		st := func(v float64) map[string]any {
			return map[string]any{"count": 3, "median_seconds": v, "p95_seconds": v + 10}
		}
		return map[string]any{"queue": st(q), "run": st(r)}
	}
	f.reply("GET /api/v1/compare", 200, map[string]any{
		"repository": "o/r", "days": 7, "since": "2026-11-08T00:00:00Z", "excluded": false, "unmatched_jobs": 0,
		"rows":                []any{map[string]any{"job": "build", "crsp": side(5, 100), "hosted": side(20, 200), "minutes_saved": 4.5, "cost_avoided_usd": 0.12}},
		"total_minutes_saved": 4.5, "total_cost_avoided_usd": 0.12,
	})
	code, out, _ := f.run("compare", "o/r", "--days", "7")
	if code != 0 || !strings.Contains(out, "build") || !strings.Contains(out, "1m40s") || !strings.Contains(out, "$0.12") {
		t.Fatalf("%d %q", code, out)
	}
	if code, out, _ := f.run("compare", "o/r", "--days", "7", "--json"); code != 0 || !strings.Contains(out, `"cost_avoided_usd"`) {
		t.Fatalf("json %d %q", code, out)
	}
	for _, args := range [][]string{{"compare"}, {"compare", "nope"}, {"compare", "o/r", "--days", "0"}, {"compare", "o/r", "--days", "91"}} {
		if code, _, _ := f.run(args...); code != ExitUsage {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

// R5.33: delete-offline names the runner, needs a yes, and calls the registration route with confirm.
func TestRunnerDeleteOffline(t *testing.T) {
	f := newFixture(t)
	f.reply("GET /api/v1/runners", 200, map[string]any{"items": []any{runnerJSON("crsp-job-1", "m_gone", nil)}})
	f.reply("DELETE /api/v1/runners/r_crsp-job-1/registration", 200, runnerJSON("crsp-job-1", "m_gone", nil))
	if code, _, e := f.run("runner", "delete-offline", "crsp-job-1"); code != ExitNotConfirmed || !strings.Contains(e, "offline runner crsp-job-1") || f.called("DELETE") {
		t.Fatalf("%d %q", code, e)
	}
	code, _, e := f.run("runner", "delete-offline", "crsp-job-1", "--yes")
	if code != 0 || !strings.Contains(e, "Runner crsp-job-1 deleted.") || !f.called("DELETE /api/v1/runners/r_crsp-job-1/registration?confirm=true") {
		t.Fatalf("%d %q %v", code, e, f.calls)
	}
	f.reply("DELETE /api/v1/runners/r_crsp-job-1/registration", 409, apiErr("conflict", "runner crsp-job-1 is online; remove it instead"))
	if code, _, e := f.run("runner", "delete-offline", "crsp-job-1", "--yes"); code != ExitConflict || !strings.Contains(e, "is online") {
		t.Fatalf("%d %q", code, e)
	}
	if code, _, _ := f.run("runner", "delete-offline", "nope", "--yes"); code != ExitNotFound {
		t.Fatal("missing runner")
	}
}
