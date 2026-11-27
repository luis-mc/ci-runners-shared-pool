package hooks_test

import (
	"bytes"
	"context"
	"go/parser"
	"go/token"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/hooks"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

// CRSP38, lessons 17 and 18 of the old pool: a hook never asks for a login,
// never waits on the agent without a limit, and writes its decision before any
// call that may be slow (scheduling R5.62, R5.63).

// R5.62: every hook turns prompts off first, whatever it does next.
func TestEveryHookTurnsPromptsOff(t *testing.T) {
	for _, hook := range []string{"job-started", "job-completed"} {
		for _, probe := range []string{"", "1"} {
			set := map[string]string{}
			var out bytes.Buffer
			e := deadEnv(t, &out, t.TempDir())
			e.Setenv = func(k, v string) { set[k] = v }
			getenv := e.Getenv
			e.Getenv = func(k string) string {
				if k == hooks.ProbeEnv {
					return probe
				}
				return getenv(k)
			}
			e.Sleep = func(context.Context, time.Duration) {}
			if code := hooks.Run(t.Context(), []string{hook}, e); code != 0 {
				t.Fatalf("%s exit %d\n%s", hook, code, out.String())
			}
			if set["GIT_TERMINAL_PROMPT"] != "0" || set["GCM_INTERACTIVE"] != "never" {
				t.Errorf("%s probe=%q: env %v", hook, probe, set)
			}
		}
	}
	// a hook with a bad flag still turns them off
	set := map[string]string{}
	var out bytes.Buffer
	e := deadEnv(t, &out, "")
	e.Setenv = func(k, v string) { set[k] = v }
	_ = hooks.Run(t.Context(), []string{"job-completed", "--no-such-flag"}, e)
	if set["GIT_TERMINAL_PROMPT"] != "0" {
		t.Errorf("bad flag: env %v", set)
	}
}

// R5.62: the hook package has no way to reach a remote host or start git. The
// agent socket is its only link, and it is a local socket with a limit.
func TestHookPackageHasNoNetworkOrGitPath(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob: %v %d", err, len(files))
	}
	banned := map[string]bool{"net/http": true, "net/smtp": true, "net/rpc": true, "os/exec": true, "crypto/tls": true}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range af.Imports {
			p := strings.Trim(im.Path.Value, `"`)
			if banned[p] || strings.Contains(p, "go-git") || strings.Contains(p, "internal/github") {
				t.Errorf("%s imports %s: a hook does only local work (R5.62)", f, p)
			}
		}
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), `"git"`) {
			t.Errorf("%s starts git", f)
		}
	}
}

func TestCallLimitsAreShortAndExplicit(t *testing.T) {
	if hooks.CallTimeout <= 0 || hooks.CallTimeout > 10*time.Second {
		t.Fatalf("hook call limit %s: seconds, not minutes", hooks.CallTimeout)
	}
	if hooks.AgentForwardTimeout >= hooks.CallTimeout {
		t.Fatalf("the agent must answer before the hook gives up: %s >= %s", hooks.AgentForwardTimeout, hooks.CallTimeout)
	}
}

// silentAgent accepts connections and never answers, like an agent that is stuck.
func silentAgent(t *testing.T) (path string, accepted *atomic.Int32) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "slow.sock")
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	if err != nil {
		t.Skipf("no unix socket here: %v", err)
	}
	accepted = new(atomic.Int32)
	done := make(chan struct{})
	t.Cleanup(func() { close(done); _ = ln.Close() })
	go func() {
		// Ends when the cleanup closes the listener.
		for c, err := ln.Accept(); err == nil; c, err = ln.Accept() {
			accepted.Add(1)
			go func() { <-done; _ = c.Close() }()
		}
	}()
	return path, accepted
}

// R5.62: a slow agent costs one limit, then the job goes on with a ::warning
// annotation and a problem record; the hook never waits on it again.
func TestSlowAgentFailsOpenWithAWarningAndOneLimit(t *testing.T) {
	path, accepted := silentAgent(t)
	for _, hook := range []string{"job-started", "job-completed"} {
		accepted.Store(0)
		dir := t.TempDir()
		var out bytes.Buffer
		e := deadEnv(t, &out, dir)
		e.Client = &hooks.SocketClient{Path: path, Timeout: 150 * time.Millisecond}
		e.Sleep = func(context.Context, time.Duration) {}
		begun := time.Now()
		if code := hooks.Run(t.Context(), []string{hook}, e); code != 0 {
			t.Fatalf("%s exit %d\n%s", hook, code, out.String())
		}
		if took := time.Since(begun); took > 3*time.Second {
			t.Errorf("%s took %s", hook, took)
		}
		if !strings.Contains(out.String(), "::warning title=crsp::crsp could not reach its agent for "+hook) {
			t.Errorf("%s: no annotation:\n%s", hook, out.String())
		}
		if p := hooks.ReadProblems(dir); len(p) != 1 {
			t.Errorf("%s: problems %q", hook, p)
		}
		// job-started asks hello once (kept), job-completed asks hello then release
		if n := accepted.Load(); n > 2 {
			t.Errorf("%s asked the slow agent %d times", hook, n)
		}
	}
}

// R5.63: the decision is in the log before the call that may be slow, so a hang
// is never silence. The client records what the log said when each call began.
type spyClient struct {
	seen      func() string
	atHello   string
	atGate    string
	atRel     string
	cancelled bool
}

func (s *spyClient) Hello(context.Context) (hooks.Hello, error) {
	s.atHello = s.seen()
	return hooks.Hello{MachineID: machine, Settings: slots.Defaults()}, nil
}

func (s *spyClient) Gate(context.Context, scheduler.GateRequest) (scheduler.Decision, error) {
	s.atGate = s.seen()
	return scheduler.Decision{}, hooks.ErrUnreachable
}

func (s *spyClient) Beat(context.Context, string, string, scheduler.Load) (scheduler.Decision, error) {
	return scheduler.Decision{}, hooks.ErrUnreachable
}

func (s *spyClient) Cancel(context.Context, string, string) error { s.cancelled = true; return nil }

func (s *spyClient) Release(context.Context, string, scheduler.JobRef, []string) ([]scheduler.Freed, error) {
	s.atRel = s.seen()
	return nil, nil
}

func TestDecisionIsLoggedBeforeTheSlowCall(t *testing.T) {
	w := newWorld(t)
	spy := &spyClient{seen: w.text}
	cfg := w.cfg(spy, gate, quiet, "g", 1)
	if code := hooks.RunGate(t.Context(), cfg); code != 0 {
		t.Fatalf("exit %d\n%s", code, w.text())
	}
	if !strings.Contains(spy.atHello, "asking the agent") || !strings.Contains(spy.atHello, "limit 5 s") {
		t.Errorf("before hello the log said:\n%s", spy.atHello)
	}
	if !strings.Contains(spy.atGate, "asking the agent for a slot") {
		t.Errorf("before the slot request the log said:\n%s", spy.atGate)
	}

	w2 := newWorld(t)
	spy2 := &spyClient{seen: w2.text}
	done := hooks.DoneConfig{Client: spy2, MachineID: machine, Job: scheduler.JobRef{Repository: "o/r", RunID: 1}, Log: func(l string) { w2.lines = append(w2.lines, l) }}
	if code := hooks.RunJobDone(t.Context(), done); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(spy2.atRel, "freeing this job's slots") {
		t.Errorf("before release the log said:\n%s", spy2.atRel)
	}
}

// R5.62: a job that goes on without the queue does not also keep a place in it.
func TestUnreachableMidQueueCancelsTheTicket(t *testing.T) {
	w := newWorld(t)
	c := &ticketThenGone{spyClient: spyClient{seen: w.text}}
	cfg := w.cfg(c, gate, quiet, "g", 1)
	if code := hooks.RunGate(t.Context(), cfg); code != 0 {
		t.Fatalf("exit %d\n%s", code, w.text())
	}
	if !c.cancelled {
		t.Errorf("the ticket was left in the queue:\n%s", w.text())
	}
}

type ticketThenGone struct{ spyClient }

func (c *ticketThenGone) Gate(context.Context, scheduler.GateRequest) (scheduler.Decision, error) {
	return scheduler.Decision{WaitText: "waiting for a slot"}, nil
}
