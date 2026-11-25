package windows

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/sys"
)

// hangingListener answers every call from the rig's fake, except that a
// Runner.Listener call never ends on its own: it prints a line and waits for
// its context, the way a configure that hangs on the real machine does.
type hangingListener struct {
	inner sys.Commands
	calls int
}

func (h *hangingListener) Run(ctx context.Context, c sys.Command) (sys.Result, error) {
	if !isListener(c) {
		return h.inner.Run(ctx, c)
	}
	h.calls++
	<-ctx.Done()
	return sys.Result{Stdout: "Connecting to GitHub\r\nAuthentication\r\nConnected to GitHub\r\n", ExitCode: 1}, nil
}

// OPS96: Runner.Listener is called directly, never through PowerShell or
// config.cmd: no script on stdin (so its stdin is the null device and a
// prompt reads end of input), the runner folder as working directory, the
// non-interactive flags, and the token only in SecretEnv.
func TestListenerCalledDirectlyWithNoStdin(t *testing.T) {
	r := newRig(t)
	if err := start(t, r, r.spec(), backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	i := listenerIdx(r)
	if i < 0 {
		t.Fatal("no Runner.Listener call")
	}
	c := r.cmd.Calls[i]
	if c.Name != `C:\ProgramData\crsp\runners\box-1\bin\Runner.Listener.exe` || c.Dir != `C:\ProgramData\crsp\runners\box-1` {
		t.Errorf("call = %s in %s", c.Name, c.Dir)
	}
	if c.Stdin != "" {
		t.Errorf("the listener gets no stdin, got %q", c.Stdin)
	}
	if len(c.Env) != 0 {
		t.Errorf("the listener needs no extra environment, got %v", c.Env)
	}
	if c.SecretEnv["ACTIONS_RUNNER_INPUT_TOKEN"] != "REGTOKEN" || strings.Contains(strings.Join(c.Args, " "), "REGTOKEN") {
		t.Error("the token is only in the secret environment")
	}
	for _, want := range []string{"--unattended", "--replace"} {
		found := false
		for _, a := range c.Args {
			found = found || a == want
		}
		if !found {
			t.Errorf("configure lacks %s: %q", want, c.Args)
		}
	}
	for _, a := range c.Args {
		if a == "--runasservice" {
			t.Error("the runner's own service setup is never asked for; CRSP registers the service")
		}
	}
}

// OPS96: a configure that never ends is stopped after the limit, and the add
// fails with that reason and the listener's last output, instead of hanging.
func TestConfigureHangFailsWithReason(t *testing.T) {
	r := newRig(t)
	h := &hangingListener{inner: r.cmd}
	r.b.d.Cmd = h
	r.b.cfg.RunnerTimeout = 50 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- start(t, r, r.spec(), backend.StartConfig{Registration: "REGTOKEN"}) }()
	var err error
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Start did not return after the configure limit")
	}
	var te *RunnerTimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v", err)
	}
	if te.Step != "register" || te.Verb != "configure" || te.Limit != 50*time.Millisecond {
		t.Errorf("timeout = %+v", te)
	}
	text := err.Error()
	for _, want := range []string{"Runner.Listener configure did not finish within 50ms", "Connected to GitHub"} {
		if !strings.Contains(text, want) {
			t.Errorf("error lacks %q: %s", want, text)
		}
	}
	if h.calls != 1 {
		t.Errorf("listener calls = %d", h.calls)
	}
}

// OPS96: the default limit is used when none is set, and a parent context
// that ends first is reported as that, not as a listener timeout.
func TestRunnerParentCancelIsNotTimeout(t *testing.T) {
	r := newRig(t)
	r.b.d.Cmd = &hangingListener{inner: r.cmd}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := r.b.runner(ctx, "register", `C:\x`, []string{"configure"}, "T")
	var te *RunnerTimeoutError
	if errors.As(err, &te) || err == nil {
		t.Fatalf("err = %v", err)
	}
}

func TestLastLines(t *testing.T) {
	if got := lastLines("a\r\n\r\nb\nc\rd\n", 2); got != "c | d" {
		t.Errorf("got %q", got)
	}
	if got := lastLines(" \n", 3); got != "" {
		t.Errorf("got %q", got)
	}
}
