//go:build linux

package trayserver

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tray"
)

type fakeBackend struct {
	checks   int
	installs int
	caller   Caller
	target   string
	linkErr  error
	// The policy calls (CRSP75): the last mode and pause the tray sent.
	mode, pause string
	resumed     int
	cancelled   int
}

func (f *fakeBackend) policy() tray.Response {
	r, _ := f.Status(context.Background())
	mode := f.mode
	if mode == "" {
		mode = "automatic"
	}
	r.Policy = &tray.PolicyInfo{Mode: mode, Accepting: mode != "paused" && f.pause == "", PauseReason: "paused", UntilResumed: f.pause == "until_resumed"}
	return r
}
func (f *fakeBackend) SetMode(_ context.Context, c Caller, mode string) (tray.Response, error) {
	f.caller, f.mode = c, mode
	return f.policy(), nil
}
func (f *fakeBackend) Pause(_ context.Context, c Caller, kind string) (tray.Response, error) {
	f.caller, f.pause = c, kind
	return f.policy(), nil
}
func (f *fakeBackend) Resume(_ context.Context, c Caller) (tray.Response, error) {
	f.caller, f.pause, f.mode = c, "", ""
	f.resumed++
	return f.policy(), nil
}

func (f *fakeBackend) Cancel(_ context.Context, c Caller) (tray.Response, error) {
	f.caller = c
	f.cancelled++
	return tray.Response{State: tray.StateHealthy, Stage: &tray.StageInfo{Name: "waiting_for_jobs", Percent: -1, StartedBy: "tray"}}, nil
}

func (f *fakeBackend) Status(context.Context) (tray.Response, error) {
	on := true
	return tray.Response{State: tray.StateHealthy, Running: 2, Version: "1.4.0", AutoUpdate: &on, LatestVersion: "1.5.0", UpdateAvailable: true, UpdateState: "idle"}, nil
}
func (f *fakeBackend) Check(ctx context.Context) (tray.Response, error) {
	f.checks++
	return f.Status(ctx)
}
func (f *fakeBackend) Install(_ context.Context, c Caller) (tray.Response, error) {
	f.caller = c
	f.installs++
	return f.Status(context.Background())
}
func (f *fakeBackend) Link(_ context.Context, c Caller, target string) (string, error) {
	f.caller, f.target = c, target
	if f.linkErr != nil {
		return "", f.linkErr
	}
	return "http://127.0.0.1:8460/auth/link?code=abc&t=" + target, nil
}

func start(t *testing.T, b Backend) (*tray.Client, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "run", "tray.sock")
	ln, err := Listen(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = (&Server{Backend: b}).Serve(ctx, ln); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	c := &tray.Client{Timeout: 3 * time.Second, Dial: func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}}
	return c, path
}

func TestChannelStatusCheckAndLink(t *testing.T) {
	fb := &fakeBackend{}
	c, _ := start(t, fb)
	ctx := context.Background()
	st, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != tray.StateHealthy || st.Running != 2 || st.Version != "1.4.0" || !st.Update.Available || st.Update.Latest != "1.5.0" || st.Update.AutoOff {
		t.Fatalf("%+v", st)
	}
	if _, err := c.Check(ctx); err != nil || fb.checks != 1 || fb.installs != 0 {
		t.Fatalf("check: %v %d", err, fb.checks)
	}
	u, err := c.LinkTo(ctx, "/settings/updates/review")
	if err != nil || u == "" {
		t.Fatalf("%q %v", u, err)
	}
	if fb.target != "/settings/updates/review" {
		t.Fatalf("target %q", fb.target)
	}
}

// The caller is read from the OS, never from the request (tray R5.8).
func TestChannelReadsPeerUserFromTheOS(t *testing.T) {
	fb := &fakeBackend{}
	c, path := start(t, fb)
	// A hand-made request that claims to be someone else: the extra field is ignored.
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = conn.Write([]byte(`{"call":"link","os_user":"0","user":"root","target":"/x"}` + "\n"))
	buf := make([]byte, 512)
	n, _ := conn.Read(buf)
	_ = conn.Close()
	if n == 0 {
		t.Fatal("no answer")
	}
	want := strconv.Itoa(os.Getuid())
	if fb.caller.OSUser != want {
		t.Fatalf("caller %q, want the OS uid %q", fb.caller.OSUser, want)
	}
	_, _ = c.LinkTo(context.Background(), "")
	if fb.caller.OSUser != want {
		t.Fatalf("caller %q", fb.caller.OSUser)
	}
}

func TestChannelClosedWhenCallerUnknownAndBadCalls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	ln, err := Listen(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fb := &fakeBackend{}
	go func() {
		_ = (&Server{Backend: fb, Peer: func(net.Conn) (Caller, error) { return Caller{}, errors.New("no creds") }}).Serve(ctx, ln)
	}()
	c := &tray.Client{Timeout: 2 * time.Second, Dial: func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}}
	if _, err := c.LinkTo(ctx, ""); err == nil {
		t.Fatal("an unknown caller must get no link")
	}
	if fb.target != "" || fb.caller.OSUser != "" {
		t.Fatal("the backend must not be asked")
	}

	c2, _ := start(t, &fakeBackend{linkErr: errors.New("sign-in is not set up")})
	if _, err := c2.LinkTo(ctx, ""); err == nil || err.Error() != "local channel: sign-in is not set up" {
		t.Fatalf("%v", err)
	}
	if _, err := c2.Call(ctx, "nope"); err == nil {
		t.Fatal("unknown call must fail")
	}
}

func TestSocketMode0660(t *testing.T) {
	_, path := start(t, &fakeBackend{})
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o660 || fi.Mode()&os.ModeSocket == 0 {
		t.Fatalf("%v", fi.Mode())
	}
	// A stale socket file from a crashed service is replaced; a regular file is not.
	reg := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(reg, []byte("x"), 0o600)
	if _, err := Listen(reg, nil); err == nil {
		t.Fatal("must not remove a regular file")
	}
}

// CRSP75: the tray changes the mode and the pause over the channel, and the
// answer carries the policy it shows.
func TestChannelModePauseAndResume(t *testing.T) {
	fb := &fakeBackend{}
	c, _ := start(t, fb)
	ctx := context.Background()
	st, err := c.SetMode(ctx, tray.ModePaused)
	if err != nil || fb.mode != "paused" || !st.Policy.Known || st.Policy.Mode != "paused" || st.Policy.Accepting {
		t.Fatalf("set mode: %v %+v %q", err, st.Policy, fb.mode)
	}
	if st, err = c.Pause(ctx, tray.PauseUntilResumed); err != nil || fb.pause != "until_resumed" || !st.Policy.UntilResumed {
		t.Fatalf("pause: %v %+v", err, st.Policy)
	}
	if st, err = c.Resume(ctx); err != nil || fb.resumed != 1 || st.Policy.Mode != "automatic" {
		t.Fatalf("resume: %v %+v", err, st.Policy)
	}
}

// D15: Cancel is its own call and the answer carries the stage.
func TestChannelCancelIsItsOwnCall(t *testing.T) {
	fb := &fakeBackend{}
	c, _ := start(t, fb)
	st, err := c.Cancel(context.Background())
	if err != nil || fb.cancelled != 1 || st.Update.Stage == nil || st.Update.Stage.StartedBy != "tray" {
		t.Fatalf("cancel: %v %d %+v", err, fb.cancelled, st.Update.Stage)
	}
}

// CRSP147: Install now is its own call; check never reaches it.
func TestChannelInstallIsItsOwnCall(t *testing.T) {
	fb := &fakeBackend{}
	c, _ := start(t, fb)
	if _, err := c.Check(context.Background()); err != nil || fb.installs != 0 {
		t.Fatalf("check must not install: %v installs=%d", err, fb.installs)
	}
	if _, err := c.Install(context.Background()); err != nil || fb.installs != 1 {
		t.Fatalf("%v installs=%d", err, fb.installs)
	}
}
