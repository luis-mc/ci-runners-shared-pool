package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// maintainFixture is a fake controller with one active machine and a drain that
// is done after two polls. exec answers the host command.
func maintainFixture(t *testing.T, exec http.HandlerFunc) *fixture {
	t.Helper()
	f := newFixture(t)
	job := jobJSON(7, "luis-mc/app", "test", "running")
	f.machines(machineJSON("build-01", "active", true, job))
	f.reply("POST /api/v1/machines/m_build-01/drain", 202, change("waiting", job))
	var polls atomic.Int32
	f.handle("GET /api/v1/machines/m_build-01/changes/c1", func(w http.ResponseWriter, _ *http.Request) {
		if polls.Add(1) >= 2 {
			writeJSON(w, 200, change("done"))
			return
		}
		writeJSON(w, 200, change("waiting", job))
	})
	f.reply("POST /api/v1/machines/m_build-01/resume", 200, machineJSON("build-01", "active", true))
	if exec != nil {
		f.handle("POST /api/v1/machines/m_build-01/command", exec)
	}
	return f
}

func execOK(body *map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if body != nil {
			_ = json.NewDecoder(r.Body).Decode(body)
		}
		writeJSON(w, 200, map[string]any{"ok": true, "exit_code": 0, "output": "compacted 120 GB\n"})
	}
}

// indexOf returns the position of the first recorded call with the prefix, or -1.
func (f *fixture) indexOf(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

// Drain wait, then run, then release; no job is touched (cli R5.29).
func TestMaintainDrainsRunsAndReleases(t *testing.T) {
	var body map[string]any
	f := maintainFixture(t, execOK(&body))
	code, out, errOut := f.run("machine", "maintain", "build-01", "--yes", "--command-timeout", "5m", "--", "docker-compact", "--force", "--yes")
	if code != 0 {
		t.Fatalf("%d %q", code, errOut)
	}
	if !strings.Contains(errOut, "waits for running job: luis-mc/app ci.yml test") || !strings.Contains(errOut, "Waiting for 1 jobs to end on build-01") || !strings.Contains(errOut, "drain timeout: 2 minutes") {
		t.Fatalf("it must show what it waits for and the timeout: %q", errOut)
	}
	if out != "compacted 120 GB\n" || !strings.Contains(errOut, "takes jobs again") {
		t.Fatalf("out %q err %q", out, errOut)
	}
	// The command goes as one argv, flags after -- are the command's own.
	argv, _ := body["command"].([]any)
	if len(argv) != 3 || argv[0] != "docker-compact" || argv[1] != "--force" || argv[2] != "--yes" || body["timeout_seconds"] != float64(300) {
		t.Fatalf("body %v", body)
	}
	drain, lastPoll, run, resume := f.indexOf("POST /api/v1/machines/m_build-01/drain"), f.indexOf("GET /api/v1/machines/m_build-01/changes/c1"), f.indexOf("POST /api/v1/machines/m_build-01/command"), f.indexOf("POST /api/v1/machines/m_build-01/resume")
	if drain < 0 || drain >= lastPoll || lastPoll >= run || run >= resume {
		t.Fatalf("order drain %d poll %d run %d resume %d: %v", drain, lastPoll, run, resume, f.calls)
	}
	if f.count("GET /api/v1/machines/m_build-01/changes/c1") != 2 {
		t.Fatalf("it must wait for the drain: %v", f.calls)
	}
	// Queued and running jobs are untouched: the only calls are the five of the flow.
	for _, c := range f.calls {
		method, path, _ := strings.Cut(c, " ")
		path, _, _ = strings.Cut(path, "?")
		if method == "DELETE" || strings.Contains(path, "/jobs") || strings.Contains(path, "/runners") || strings.Contains(path, "/decision") {
			t.Fatalf("a job or runner call: %s", c)
		}
	}
	if !f.called("POST /api/v1/machines/m_build-01/resume?confirm=true") {
		t.Fatalf("resume must carry the yes for a waiting change: %v", f.calls)
	}
}

func TestMaintainJSON(t *testing.T) {
	f := maintainFixture(t, execOK(nil))
	code, out, errOut := f.run("machine", "maintain", "build-01", "--yes", "--json", "--", "true")
	var doc maintainResult
	if code != 0 || json.Unmarshal([]byte(out), &doc) != nil || !doc.OK || !doc.Released || doc.Machine != "build-01" || doc.Output != "compacted 120 GB\n" {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
}

// A command that fails still lifts the hold; exit 1 and the output are shown.
func TestMaintainReleasesWhenTheCommandFails(t *testing.T) {
	f := maintainFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": false, "exit_code": 3, "output": "disk busy\n", "message": "the command exited with code 3"})
	})
	code, out, errOut := f.run("machine", "maintain", "build-01", "--yes", "--", "compact")
	if code != ExitFailed || out != "disk busy\n" || !strings.Contains(errOut, "exit code 3") || !strings.Contains(errOut, "no job was stopped") {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	if f.count("POST /api/v1/machines/m_build-01/resume") != 1 {
		t.Fatalf("hold not lifted: %v", f.calls)
	}
}

// The controller refuses the command (409): the hold is still lifted.
func TestMaintainReleasesWhenTheControllerRefusesTheCommand(t *testing.T) {
	f := maintainFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 409, apiErr("conflict", "drain the machine first"))
	})
	code, _, errOut := f.run("machine", "maintain", "build-01", "--yes", "--", "compact")
	if code != ExitConflict || !strings.Contains(errOut, "drain the machine first") || f.count("POST /api/v1/machines/m_build-01/resume") != 1 {
		t.Fatalf("%d %q %v", code, errOut, f.calls)
	}
}

// Ctrl-C while it waits for the drain: the hold is lifted on a fresh context,
// the command never runs, no job is stopped.
func TestMaintainReleasesWhenCancelledWhileDraining(t *testing.T) {
	f := maintainFixture(t, execOK(nil))
	job := jobJSON(7, "luis-mc/app", "test", "running")
	// The handler answers only after the run has returned, so the client can
	// never see the answer: the cancel is the only way out of the call. (The
	// server's request context is no proof the client saw the cancel.)
	release := make(chan struct{})
	f.handle("GET /api/v1/machines/m_build-01/changes/c1", func(w http.ResponseWriter, _ *http.Request) {
		f.cancel() // Ctrl-C
		<-release
		writeJSON(w, 200, change("waiting", job))
	})
	code, _, errOut := f.run("machine", "maintain", "build-01", "--yes", "--", "compact")
	close(release)
	if code != ExitFailed {
		t.Fatalf("%d %q", code, errOut)
	}
	if f.count("POST /api/v1/machines/m_build-01/command") != 0 {
		t.Fatalf("the command ran: %v", f.calls)
	}
	if !f.called("POST /api/v1/machines/m_build-01/resume?confirm=true") || !strings.Contains(errOut, "Hold lifted on build-01") {
		t.Fatalf("hold not lifted: %v %q", f.calls, errOut)
	}
}

// Ctrl-C while the command runs: the hold is lifted and the user is told the
// command may still run on the machine.
func TestMaintainReleasesWhenCancelledWhileRunning(t *testing.T) {
	var f *fixture
	f = maintainFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		f.cancel() // Ctrl-C
		// Never answer: drop the connection. The client can then only fail with
		// the cancel; with an answer on the wire it could, by a race inside the
		// transport, still return the answer and the cancel would go unseen.
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.Close()
	})
	code, _, errOut := f.run("machine", "maintain", "build-01", "--yes", "--", "compact")
	if code == 0 || !strings.Contains(errOut, "may still run on the machine") || f.count("POST /api/v1/machines/m_build-01/resume") != 1 {
		t.Fatalf("%d %q %v", code, errOut, f.calls)
	}
}

func TestMaintainRefusals(t *testing.T) {
	// Unknown machine: exit 4, nothing held.
	f := maintainFixture(t, execOK(nil))
	if code, _, errOut := f.run("machine", "maintain", "nope", "--yes", "--", "compact"); code != ExitNotFound || !strings.Contains(errOut, `No machine "nope"`) {
		t.Fatalf("unknown: %d %q", code, errOut)
	}
	if f.called("POST") {
		t.Fatalf("unknown machine changed something: %v", f.calls)
	}
	// Already drained (someone else's hold), draining, offline: exit 7, nothing touched.
	for state, online := range map[string]bool{"drained": true, "draining": true, "active": false} {
		f := newFixture(t)
		f.machines(machineJSON("build-01", state, online))
		code, _, errOut := f.run("machine", "maintain", "build-01", "--yes", "--", "compact")
		if code != ExitConflict || f.called("POST") {
			t.Fatalf("%s online=%v: %d %q %v", state, online, code, errOut, f.calls)
		}
	}
	// No command, no confirmation: exit 2 and 5, nothing held.
	f = maintainFixture(t, execOK(nil))
	if code, _, _ := f.run("machine", "maintain", "build-01", "--yes"); code != ExitUsage {
		t.Fatalf("no command: %d", code)
	}
	if code, _, _ := f.run("machine", "maintain", "build-01", "--", "compact"); code != ExitNotConfirmed {
		t.Fatalf("no yes: %d", code)
	}
	if code, _, _ := f.run("machine", "maintain", "build-01", "--yes", "--command-timeout", "0s", "--", "compact"); code != ExitUsage {
		t.Fatalf("bad timeout: %d", code)
	}
	if f.called("POST") {
		t.Fatalf("a refusal changed something: %v", f.calls)
	}
}
