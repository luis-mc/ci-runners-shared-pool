package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// Every refusal exits non-zero with the code of cli R5.12, and changes nothing.
// One case per refusal path of every command that can refuse.

func TestRefusalsExitNonZero(t *testing.T) {
	job := jobJSON(7, "luis-mc/app", "test", "running")
	type tc struct {
		name  string
		args  []string
		setup func(f *fixture)
		want  int
		in    string // text the error must hold
		never string // a call that must not happen
	}
	machine := func(f *fixture) { f.machines(machineJSON("build-01", "active", true, job)) }
	cases := []tc{
		{"drain without yes, no terminal", []string{"machine", "drain", "build-01"}, machine, ExitNotConfirmed, "Nothing changed", "POST /api/v1/machines/m_build-01/drain"},
		{"remove machine without yes", []string{"machine", "remove", "build-01"}, machine, ExitNotConfirmed, "Nothing changed", "DELETE"},
		{"remove runner without yes", []string{"runner", "remove", "linux-03"}, func(f *fixture) {
			f.reply("GET /api/v1/runners", 200, map[string]any{"items": []any{runnerJSON("linux-03", "m_build-01", nil)}})
		}, ExitNotConfirmed, "Nothing changed", "DELETE"},
		{"context remove without yes", []string{"context", "remove", "home"}, func(*fixture) {}, ExitNotConfirmed, "Nothing changed", ""},
		{"restore without yes", []string{"restore", "b1"}, func(f *fixture) {
			f.reply("GET /api/v1/backups", 200, map[string]any{"items": []any{map[string]any{"id": "b1", "file": "x.db", "state": "good", "created_at": "2026-11-14T10:00:00Z"}}})
		}, ExitNotConfirmed, "Nothing changed", "POST /api/v1/backups/b1/restore"},
		{"drain not allowed (viewer)", []string{"machine", "drain", "build-01", "--yes"}, func(f *fixture) {
			machine(f)
			f.reply("POST /api/v1/machines/m_build-01/drain", 403, apiErr("forbidden", "Draining a machine needs the admin role."))
		}, ExitAuth, "admin role", ""},
		{"drain precondition failed (already drained)", []string{"machine", "drain", "build-01", "--yes"}, func(f *fixture) {
			machine(f)
			f.reply("POST /api/v1/machines/m_build-01/drain", 409, apiErr("conflict", "the machine is already drained"))
		}, ExitConflict, "already drained", ""},
		{"drain controller needs confirmation (409 needs_confirmation)", []string{"machine", "remove", "build-01", "--yes"}, func(f *fixture) {
			machine(f)
			f.reply("DELETE /api/v1/machines/m_build-01", 409, apiErr("needs_confirmation", "removing build-01 drains it first"))
		}, ExitConflict, "drains it first", ""},
		{"unknown machine", []string{"machine", "drain", "nope", "--yes"}, machine, ExitNotFound, "nope", "POST"},
		{"unknown runner", []string{"runner", "remove", "nope", "--yes"}, func(f *fixture) {
			f.reply("GET /api/v1/runners", 200, map[string]any{"items": []any{}})
		}, ExitNotFound, "nope", "DELETE"},
		{"unknown context", []string{"context", "use", "nope"}, func(*fixture) {}, ExitNotFound, "nope", ""},
		{"drain timed out is not an option: --stop-jobs is gone", []string{"machine", "drain", "build-01", "--yes", "--stop-jobs"}, machine, ExitUsage, "", "POST"},
		{"drain-timeout flag is gone", []string{"machine", "drain", "build-01", "--yes", "--drain-timeout", "1m"}, machine, ExitUsage, "", "POST"},
		{"missing argument", []string{"machine", "drain"}, machine, ExitUsage, "Usage", ""},
		{"change cancelled while waiting", []string{"machine", "drain", "build-01", "--yes"}, func(f *fixture) {
			machine(f)
			f.reply("POST /api/v1/machines/m_build-01/drain", 202, change("waiting", job))
			f.reply("GET /api/v1/machines/m_build-01/changes/c1", 200, change("cancelled"))
		}, ExitFailed, "cancelled", ""},
		{"change needs you, jobs not stopped", []string{"machine", "drain", "build-01", "--yes"}, func(f *fixture) {
			machine(f)
			f.reply("POST /api/v1/machines/m_build-01/drain", 202, change("waiting", job))
			f.reply("GET /api/v1/machines/m_build-01/changes/c1", 200, change("needs_you", job))
		}, ExitFailed, "not stopped", "POST /api/v1/machines/m_build-01/changes"},
		{"unbuilt command", []string{"config", "export"}, func(*fixture) {}, ExitUsage, "not built yet", ""},
		{"unknown command", []string{"frobnicate"}, func(*fixture) {}, ExitUsage, "", ""},
		{"unreachable controller", []string{"machine", "list"}, func(f *fixture) { f.srv.Close() }, ExitUnreachable, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			c.setup(f)
			code, _, errOut := f.run(c.args...)
			if code != c.want {
				t.Fatalf("exit %d, want %d; stderr %q", code, c.want, errOut)
			}
			if code == 0 {
				t.Fatal("a refusal exited 0")
			}
			if c.in != "" && !strings.Contains(errOut, c.in) {
				t.Fatalf("stderr %q does not hold %q", errOut, c.in)
			}
			if c.never != "" && f.called(c.never) {
				t.Fatalf("%s was called: %v", c.never, f.calls)
			}
		})
	}
}

func TestRefusalWrongAnswerAndNotSignedIn(t *testing.T) {
	f := newFixture(t)
	f.machines(machineJSON("build-01", "active", true))
	f.terminal, f.stdin = true, "no\n"
	if code, _, _ := f.run("machine", "drain", "build-01"); code != ExitNotConfirmed || f.called("POST") {
		t.Fatalf("wrong answer: %d %v", code, f.calls)
	}
	// Logout with no token is refused with exit 3, like every command that needs a sign-in.
	f.reply("DELETE /api/v1/auth/cli/tokens/tok1", 204, nil)
	if code, _, _ := f.run("logout"); code != 0 {
		t.Fatalf("first logout: %d", code)
	}
	if code, _, e := f.run("logout"); code != ExitAuth || !strings.Contains(e, "Not signed in") {
		t.Fatalf("second logout: %d %q", code, e)
	}
	if code, _, _ := f.run("machine", "list"); code != ExitAuth {
		t.Fatalf("machine list without a token: %d", code)
	}
}

// --json prints the error envelope of a refusal and still exits non-zero.
func TestRefusalJSONEnvelope(t *testing.T) {
	f := newFixture(t)
	f.machines(machineJSON("build-01", "active", true))
	code, _, errOut := f.run("machine", "drain", "build-01", "--json")
	if code != ExitNotConfirmed || !strings.Contains(errOut, `"needs_confirmation"`) {
		t.Fatalf("%d %q", code, errOut)
	}
}

// Past the machine's drain timeout the command keeps waiting, warns, and never
// asks the controller to stop a job. The test clock moves 1 minute a reading
// and the machine's timeout is 2 minutes.
func TestDrainPastTimeoutWarnsAndKeepsWaiting(t *testing.T) {
	f := newFixture(t)
	job := jobJSON(7, "luis-mc/app", "test", "running")
	f.machines(machineJSON("build-01", "active", true, job))
	f.reply("POST /api/v1/machines/m_build-01/drain", 202, change("waiting", job))
	var polls atomic.Int32
	f.handle("GET /api/v1/machines/m_build-01/changes/c1", func(w http.ResponseWriter, r *http.Request) {
		if polls.Add(1) > 12 {
			writeJSON(w, 200, change("done"))
			return
		}
		writeJSON(w, 200, change("waiting", job))
	})
	f.handle("POST /api/v1/machines/m_build-01/changes/c1", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		t.Errorf("a decision was sent: %v", b)
		writeJSON(w, 200, change("done"))
	})
	code, _, errOut := f.run("machine", "drain", "build-01", "--yes")
	if code != 0 || !strings.Contains(errOut, "Still draining build-01: 1 jobs running, waited") || !strings.Contains(errOut, "Nothing is stopped") {
		t.Fatalf("%d %q", code, errOut)
	}
	if f.called("POST /api/v1/machines/m_build-01/changes") {
		t.Fatalf("calls %v", f.calls)
	}
}
