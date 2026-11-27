package tray

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image/color"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatusTextPerState(t *testing.T) {
	at := time.Date(2026, 11, 14, 14, 30, 0, 0, time.UTC)
	cases := map[string]Status{
		"✓ ok · 2 running · 1 waiting":      {State: StateHealthy, Running: 2, Waiting: 1, Runners: 1},
		"◷ waiting · 0 running · 4 waiting": {State: StateWaiting, Waiting: 4, Runners: 1},
		"✕ failed · GitHub not reachable":   {State: StateFailed, Fault: "GitHub not reachable"},
		"✕ stopped at 14:30 · exit code 3":  {State: StateStopped, StoppedAt: at, ExitCode: 3},
		"▶ starting":                        {State: StateStarting},
		"✓ ok · no runners yet":             {State: StateHealthy},
	}
	for want, s := range cases {
		if got := s.Text(); got != want {
			t.Errorf("got %q want %q", got, want)
		}
		if got := s.Tooltip(); got != "crsp: "+want {
			t.Errorf("tooltip %q", got)
		}
	}
}

func TestTooltipCutAt127(t *testing.T) {
	s := Status{State: StateFailed, Fault: strings.Repeat("é", 300)}
	tt := s.Tooltip()
	if n := len([]rune(tt)); n != MaxTooltip || !strings.HasSuffix(tt, "…") {
		t.Fatalf("len %d %q", n, tt)
	}
}

func TestIconIsAlwaysTheDiscDifferingOnlyInColour(t *testing.T) {
	states := []State{StateHealthy, StateWaiting, StateFailed, StateStopped, StateStarting}
	seen := map[color.NRGBA]State{}
	for _, s := range states {
		c := DiscColour(s)
		if o, dup := seen[c]; dup {
			t.Errorf("%s and %s share a colour", s, o)
		}
		seen[c] = s
		if c != DiscColour(s) || !bytes.HasPrefix(IconICO(s), []byte{0, 0, 1, 0, 3, 0}) {
			t.Errorf("%s: bad ICO header", s)
		}
	}
	if DiscColour(StateHealthy) != (color.NRGBA{R: 0x1a, G: 0x7f, B: 0x37, A: 0xff}) {
		t.Error("healthy disc is not green 1a7f37")
	}
	// Same shape and letters in every state: the alpha channel and the letter (white) pixels match.
	ref := DrawIcon(32, DiscColour(StateHealthy))
	for _, s := range states {
		img := DrawIcon(32, DiscColour(s))
		for y := 0; y < 32; y++ {
			for x := 0; x < 32; x++ {
				a, b := ref.NRGBAAt(x, y), img.NRGBAAt(x, y)
				if a.A != b.A {
					t.Fatalf("%s: shape differs at %d,%d", s, x, y)
				}
			}
		}
	}
	// Letters are drawn: white pixels inside the disc at every tray size.
	for _, n := range IconSizes {
		img := DrawIcon(n, DiscColour(StateHealthy))
		white := 0
		for y := 0; y < n; y++ {
			for x := 0; x < n; x++ {
				if c := img.NRGBAAt(x, y); c.A == 255 && c.R > 200 && c.G > 200 && c.B > 200 {
					white++
				}
			}
		}
		if white < n*n/40 {
			t.Errorf("size %d: only %d letter pixels", n, white)
		}
	}
}

// The committed .ico under cmd/crsp must be what AppICO draws; `go generate ./apps/controller/cmd/crsp` rewrites it.
func TestCommittedAppIconMatches(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "cmd", "crsp", "winres", "crsp.ico"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, AppICO()) {
		t.Fatal("apps/controller/cmd/crsp/winres/crsp.ico is stale: run go generate ./apps/controller/cmd/crsp")
	}
	n := int(binary.LittleEndian.Uint16(b[4:6]))
	if n != 7 {
		t.Fatalf("want 7 sizes, got %d", n)
	}
}

type fakeSource struct {
	st  Status
	err error
}

func (f *fakeSource) Status(context.Context) (Status, error) { return f.st, f.err }

func TestPollerUpdatesWithin10s(t *testing.T) {
	now := time.Unix(1000, 0)
	src := &fakeSource{st: Status{State: StateHealthy, Running: 1}}
	p := &Poller{Source: src, Now: func() time.Time { return now }}
	cur, good := p.Poll(context.Background(), Status{}, time.Time{})
	if cur.Running != 1 {
		t.Fatal("no first reading")
	}
	// A change shows at the next poll; the interval is 5 s, so within 10 s.
	if PollInterval*2 > 10*time.Second {
		t.Fatal("poll interval too long")
	}
	src.st = Status{State: StateWaiting, Waiting: 3}
	now = now.Add(PollInterval)
	cur, _ = p.Poll(context.Background(), cur, good)
	if cur.State != StateWaiting {
		t.Fatalf("got %v", cur.State)
	}
}

func TestPollerKeepsLastThenAsksOS(t *testing.T) {
	now := time.Unix(1000, 0)
	src := &fakeSource{st: Status{State: StateHealthy, Running: 1}}
	p := &Poller{Source: src, Now: func() time.Time { return now },
		Manager: func(context.Context) (Status, bool) { return Status{State: StateStopped, ExitCode: 3}, true }}
	cur, good := p.Poll(context.Background(), Status{}, time.Time{})
	src.err = errors.New("down")
	now = now.Add(10 * time.Second)
	cur, good = p.Poll(context.Background(), cur, good)
	if cur.State != StateHealthy {
		t.Fatal("must keep last state for 15 s")
	}
	now = now.Add(10 * time.Second)
	cur, _ = p.Poll(context.Background(), cur, good)
	if cur.State != StateStopped || cur.ExitCode != 3 {
		t.Fatalf("got %+v", cur)
	}
	p.Manager = nil
	cur, _ = p.Poll(context.Background(), cur, good)
	if cur.State != StateStarting {
		t.Fatalf("got %+v", cur)
	}
}

func TestStoppedStateWithExitCode(t *testing.T) {
	if runtimeGOOS() != "linux" {
		t.Skip("parser test runs on Linux")
	}
	st, ok := parseSystemctlForTest("ActiveState=failed\nExecMainStatus=3\nExecMainExitTimestamp=Sat 2026-11-14 14:30:00 UTC\n")
	if !ok || st.State != StateStopped || st.ExitCode != 3 || st.StoppedAt.Format("15:04") != "14:30" {
		t.Fatalf("%+v %v", st, ok)
	}
	if _, ok := parseSystemctlForTest("ActiveState=active\n"); ok {
		t.Fatal("active service is not stopped")
	}
}

// serve answers one JSON line per connection on a pipe pair.
func fakeChannel(handler func(req string) string) Dialer {
	return func(ctx context.Context) (net.Conn, error) {
		c, s := net.Pipe()
		go func() {
			defer func() { _ = s.Close() }()
			buf := make([]byte, 512)
			n, _ := s.Read(buf)
			_, _ = s.Write([]byte(handler(string(buf[:n])) + "\n"))
		}()
		return c, nil
	}
}

func TestClientStatusAndLink(t *testing.T) {
	c := &Client{Timeout: time.Second, Dial: fakeChannel(func(req string) string {
		if strings.Contains(req, "link") {
			return `{"ok":true,"url":"https://h:8460/auth/link?code=abc"}`
		}
		return `{"ok":true,"state":"waiting","running":1,"waiting":2,"runners":1}`
	})}
	st, err := c.Status(context.Background())
	if err != nil || st.State != StateWaiting || st.Waiting != 2 {
		t.Fatalf("%+v %v", st, err)
	}
	u, err := c.Link(context.Background())
	if err != nil || !strings.Contains(u, "code=abc") {
		t.Fatalf("%q %v", u, err)
	}
}

func TestSlowServiceTimesOutIn3s(t *testing.T) {
	c := &Client{Timeout: 100 * time.Millisecond, Dial: func(ctx context.Context) (net.Conn, error) {
		cc, _ := net.Pipe() // never answers
		return cc, nil
	}}
	start := time.Now()
	if _, err := c.Status(context.Background()); err == nil {
		t.Fatal("want timeout")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("blocked too long")
	}
	if NewClient().Timeout != 3*time.Second {
		t.Fatal("default timeout must be 3 s")
	}
}

type fakeLinks struct {
	url string
	err error
}

func (f fakeLinks) Link(context.Context) (string, error) { return f.url, f.err }

func TestOpenAsksLinkAndLaunchesBrowser(t *testing.T) {
	var opened string
	o := &Opener{Links: fakeLinks{url: "https://x/auth/link?code=1"}, Browser: func(u string) error { opened = u; return nil }}
	if err := o.Open(context.Background()); err != nil || opened != "https://x/auth/link?code=1" {
		t.Fatalf("%q %v", opened, err)
	}
}

func TestOpenWhileStoppedShowsProblem(t *testing.T) {
	var body string
	called := false
	o := &Opener{Links: fakeLinks{url: "u"}, Browser: func(string) error { called = true; return nil },
		Current: func() Status { return Status{State: StateStopped, ExitCode: 3} },
		Notify:  func(_, b string) { body = b }}
	if err := o.Open(context.Background()); err == nil || called {
		t.Fatal("must refuse and not open a dead link")
	}
	if !strings.Contains(body, "exit code 3") || !strings.Contains(body, "log folder") {
		t.Fatalf("notice %q", body)
	}
}

func TestOpenNoBrowserShowsLink(t *testing.T) {
	var body string
	o := &Opener{Links: fakeLinks{url: "https://x/l"}, Browser: func(string) error { return errors.New("none") },
		Notify: func(_, b string) { body = b }}
	_ = o.Open(context.Background())
	if !strings.Contains(body, "https://x/l") {
		t.Fatalf("notice %q", body)
	}
}

func TestQuitLeavesServiceRunning(t *testing.T) {
	// The tray has no call that stops the service: the channel client offers only status and link.
	var req string
	c := &Client{Timeout: time.Second, Dial: fakeChannel(func(r string) string { req = r; return `{"ok":true}` })}
	_, _ = c.Status(context.Background())
	if strings.Contains(req, "stop") {
		t.Fatal("tray must never send stop")
	}
}

func TestLogFolderPathPerOS(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if got := logDirFor("linux", env(nil), nil); got != "/var/log/crsp" {
		t.Error(got)
	}
	if got := logDirFor("darwin", env(nil), nil); got != "/Library/Logs/crsp" {
		t.Error(got)
	}
	if got := logDirFor("windows", env(map[string]string{"ProgramData": `D:\PD`}), nil); !strings.HasSuffix(got, "logs") || !strings.Contains(got, "crsp") {
		t.Error(got)
	}
	if got := logDirFor("linux", env(map[string]string{"CRSP_LOG_DIR": "/x"}), nil); got != "/x" {
		t.Error(got)
	}
	var opened string
	o := &Opener{Files: func(p string) error { opened = p; return nil }, LogDir: func() string { return "/l" }}
	if err := o.OpenLogFolder(); err != nil || opened != "/l" {
		t.Fatal(opened, err)
	}
}

func TestAutostartOnOff(t *testing.T) {
	dir := t.TempDir()
	for name, a := range map[string]Autostart{
		"xdg":   &XDGAutostart{Dir: filepath.Join(dir, "a"), Exec: "/opt/my crsp/crsp"},
		"agent": &LaunchAgent{Dir: filepath.Join(dir, "b"), Exec: "/usr/local/bin/crsp"},
	} {
		if on, _ := a.Enabled(); on {
			t.Fatalf("%s: on before set", name)
		}
		if err := a.Set(true); err != nil {
			t.Fatal(err)
		}
		if on, _ := a.Enabled(); !on {
			t.Fatalf("%s: not on", name)
		}
		if err := a.Set(false); err != nil {
			t.Fatal(err)
		}
		if on, _ := a.Enabled(); on {
			t.Fatalf("%s: still on", name)
		}
		if err := a.Set(false); err != nil {
			t.Fatalf("%s: off twice: %v", name, err)
		}
	}
	x := &XDGAutostart{Dir: dir, Exec: "/opt/my crsp/crsp"}
	_ = x.Set(true)
	b, _ := os.ReadFile(filepath.Join(dir, "crsp-tray.desktop"))
	if !strings.Contains(string(b), `Exec="/opt/my crsp/crsp" tray`) {
		t.Errorf("desktop file:\n%s", b)
	}
	l := &LaunchAgent{Dir: dir, Exec: "/b/crsp"}
	_ = l.Set(true)
	b, _ = os.ReadFile(filepath.Join(dir, LaunchAgentLabel+".plist"))
	if !strings.Contains(string(b), "<key>RunAtLoad</key><true/>") {
		t.Errorf("plist:\n%s", b)
	}
}

func TestNoticeOncePerVersion(t *testing.T) {
	p := filepath.Join(t.TempDir(), "crsp", "tray.json")
	if !noticeDue(p, "1.0") || noticeDue(p, "1.0") || !noticeDue(p, "1.1") {
		t.Fatal("notice must show once per version")
	}
}

func TestTrayStateHoldsNoSecret(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tray.json")
	noticeDue(p, "1.0")
	b, _ := os.ReadFile(p)
	for _, w := range []string{"token", "secret", "code", "password"} {
		if strings.Contains(strings.ToLower(string(b)), w) {
			t.Errorf("state file mentions %q: %s", w, b)
		}
	}
}

func TestOpenCommandPrintsLink(t *testing.T) {
	t.Setenv("CRSP_TRAY_SOCKET", filepath.Join(t.TempDir(), "none.sock"))
	// Never reach a crsp service installed on the test machine.
	prev := newClient
	newClient = func() *Client {
		return &Client{Timeout: time.Second, Dial: func(context.Context) (net.Conn, error) { return nil, errors.New("no service") }}
	}
	t.Cleanup(func() { newClient = prev })
	var out, errb bytes.Buffer
	if code := OpenCommand(&out, &errb); code != 1 || !strings.Contains(errb.String(), "service running") {
		t.Fatalf("code %d %q", code, errb.String())
	}
}

// CRSP147: "Updated to vX" shows once per version, however many trays start after the install.
func TestUpdatedNoticeOncePerVersion(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tray.json")
	if !updatedDue(p, "1.5.0") || updatedDue(p, "1.5.0") || !updatedDue(p, "1.6.0") {
		t.Fatal("once per version")
	}
	// The no-tray-area notice keeps its own record.
	if !noticeDue(p, "1.6.0") {
		t.Fatal("the two notices are independent")
	}
}
