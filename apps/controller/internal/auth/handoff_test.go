package auth

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

const lanConsole = "https://192.168.1.162:8460"

// probeGH answers the dry sign-in with a fixed result.
type probeGH struct {
	*github.REST
	mu    sync.Mutex
	known map[string]bool
	err   error
	calls []string
}

func (p *probeGH) CheckRedirectURI(_ context.Context, uri string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, uri)
	return p.known[uri], p.err
}

// withProbe makes the service ask probe, and keep the registered list in memory.
func (e *env) withProbe(known ...string) (*probeGH, *[]string) {
	p := &probeGH{REST: e.gh, known: map[string]bool{}}
	for _, k := range known {
		p.known[k] = true
	}
	var saved []string
	e.svc.cfg.GitHub = p
	e.svc.cfg.LoadRegistered = func(context.Context) ([]string, error) { return saved, nil }
	e.svc.cfg.SaveRegistered = func(_ context.Context, o []string) error { saved = append([]string(nil), o...); return nil }
	return p, &saved
}

func TestRedirectChoice(t *testing.T) { // OPS16
	ctx := context.Background()
	t.Run("a registered address keeps its own callback", func(t *testing.T) {
		e := newEnv(t)
		e.withApp()
		s, err := e.svc.StartSignIn(ctx, SignInOptions{Origin: devConsole})
		if err != nil || s.HandoffBind != "" {
			t.Fatalf("%v %q", err, s.HandoffBind)
		}
		if got := mustQuery(t, s.RedirectURL, "redirect_uri"); got != devConsole+CallbackPath {
			t.Fatal(got)
		}
	})
	t.Run("a new address on this PC goes through the registered loopback", func(t *testing.T) {
		e := newEnv(t)
		e.withApp()
		p, _ := e.withProbe()
		s, err := e.svc.StartSignIn(ctx, SignInOptions{Origin: lanConsole, SameMachine: true})
		if err != nil {
			t.Fatal(err)
		}
		if got := mustQuery(t, s.RedirectURL, "redirect_uri"); got != devConsole+CallbackPath {
			t.Fatalf("redirect_uri %q is not the registered one", got)
		}
		if s.HandoffBind == "" || len(p.calls) != 0 {
			t.Fatalf("handoff bind %q, GitHub asked %v", s.HandoffBind, p.calls)
		}
	})
	t.Run("a browser on another machine is never sent to an unknown callback", func(t *testing.T) {
		e := newEnv(t)
		e.withApp()
		p, _ := e.withProbe()
		_, err := e.svc.StartSignIn(ctx, SignInOptions{Origin: lanConsole})
		var nr *NotRegisteredError
		if !errors.As(err, &nr) {
			t.Fatalf("want NotRegisteredError, got %v", err)
		}
		if nr.CallbackURL != lanConsole+CallbackPath || nr.Address != lanConsole {
			t.Fatalf("%+v", nr)
		}
		if !strings.HasSuffix(nr.AppURL, "/settings/apps/"+e.apps[0].Slug) {
			t.Fatalf("app page %q", nr.AppURL)
		}
		if len(p.calls) != 1 {
			t.Fatalf("the dry sign-in runs once: %v", p.calls)
		}
	})
	t.Run("an address added to the App since is found by the dry sign-in and kept", func(t *testing.T) {
		e := newEnv(t)
		e.withApp()
		_, saved := e.withProbe(lanConsole + CallbackPath)
		s, err := e.svc.StartSignIn(ctx, SignInOptions{Origin: lanConsole})
		if err != nil {
			t.Fatal(err)
		}
		if got := mustQuery(t, s.RedirectURL, "redirect_uri"); got != lanConsole+CallbackPath || s.HandoffBind != "" {
			t.Fatalf("redirect_uri %q", got)
		}
		if !e.svc.isRegistered(ctx, lanConsole) || len(*saved) == 0 {
			t.Fatalf("not kept: %v", *saved)
		}
	})
	t.Run("an App made before the list was kept knows loopback only", func(t *testing.T) {
		e := newEnv(t)
		e.consoles = []string{devConsole}
		e.withApp()
		e.withProbe()
		if e.svc.isRegistered(ctx, lanConsole) {
			t.Fatal("the chosen address is not registered by itself")
		}
	})
}

// handoffRun signs in through the loopback callback for a sign-in that started
// on lanConsole, and returns what the redirect to the callback carries.
func (e *env) handoffRun(t *testing.T) (start SignInStart, si SignedIn, state string) {
	t.Helper()
	ctx := context.Background()
	e.withApp()
	e.withProbe()
	e.fake.AddUser(1, "alice")
	setup, _ := e.svc.NewSetupCode(ctx)
	cookie, err := e.svc.RedeemSetupCode(ctx, setup)
	if err != nil {
		t.Fatal(err)
	}
	start, err = e.svc.StartSignIn(ctx, SignInOptions{Origin: lanConsole, SameMachine: true, ReturnTo: "/machines", SetupCookie: cookie})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(start.RedirectURL)
	state = u.Query().Get("state")
	e.fake.IssueOAuthCode("hc", 1, u.Query().Get("code_challenge"))
	si, err = e.svc.CompleteSignIn(ctx, "hc", state, devConsole)
	if err != nil {
		t.Fatal(err)
	}
	return start, si, state
}

func TestHandoffReturnsToStartAddress(t *testing.T) { // OPS16
	e := newEnv(t)
	start, si, state := e.handoffRun(t)
	if si.Handoff == nil || si.Handoff.Origin != lanConsole || si.Handoff.State != state || si.Handoff.Code == "" {
		t.Fatalf("handoff %+v", si.Handoff)
	}
	if si.Cookie != "" {
		t.Fatal("no session cookie is handed out on the loopback address")
	}
	got, err := e.svc.RedeemHandoff(context.Background(), si.Handoff.Code, state, lanConsole, start.HandoffBind)
	if err != nil {
		t.Fatal(err)
	}
	if got.ReturnTo != "/machines" || got.Cookie == "" {
		t.Fatalf("%+v", got)
	}
	if p, err := e.svc.Authenticate(context.Background(), got.Cookie); err != nil || p.Login != "alice" {
		t.Fatalf("the cookie must be a valid session: %v %+v", err, p)
	}
}

func TestHandoffRefusals(t *testing.T) { // OPS16
	ctx := context.Background()
	cases := []struct {
		name string
		run  func(e *env, start SignInStart, si SignedIn, state string) error
	}{
		{"reuse", func(e *env, start SignInStart, si SignedIn, state string) error {
			if _, err := e.svc.RedeemHandoff(ctx, si.Handoff.Code, state, lanConsole, start.HandoffBind); err != nil {
				t.Fatal(err)
			}
			_, err := e.svc.RedeemHandoff(ctx, si.Handoff.Code, state, lanConsole, start.HandoffBind)
			return err
		}},
		{"expired", func(e *env, start SignInStart, si SignedIn, state string) error {
			e.advance(HandoffTTL + time.Second)
			_, err := e.svc.RedeemHandoff(ctx, si.Handoff.Code, state, lanConsole, start.HandoffBind)
			return err
		}},
		{"wrong host", func(e *env, start SignInStart, si SignedIn, state string) error {
			_, err := e.svc.RedeemHandoff(ctx, si.Handoff.Code, state, "https://192.168.1.99:8460", start.HandoffBind)
			return err
		}},
		{"loopback is not the start address", func(e *env, start SignInStart, si SignedIn, state string) error {
			_, err := e.svc.RedeemHandoff(ctx, si.Handoff.Code, state, devConsole, start.HandoffBind)
			return err
		}},
		{"wrong state", func(e *env, start SignInStart, si SignedIn, state string) error {
			_, err := e.svc.RedeemHandoff(ctx, si.Handoff.Code, "other", lanConsole, start.HandoffBind)
			return err
		}},
		{"other browser", func(e *env, start SignInStart, si SignedIn, state string) error {
			_, err := e.svc.RedeemHandoff(ctx, si.Handoff.Code, state, lanConsole, "")
			if err == nil {
				t.Fatal("a missing bind cookie must be refused")
			}
			e2 := newEnv(t)
			_, err = e2.svc.RedeemHandoff(ctx, si.Handoff.Code, state, lanConsole, "forged")
			return err
		}},
		{"forged code", func(e *env, start SignInStart, si SignedIn, state string) error {
			_, err := e.svc.RedeemHandoff(ctx, "nope", state, lanConsole, start.HandoffBind)
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			start, si, state := e.handoffRun(t)
			if err := c.run(e, start, si, state); !errors.Is(err, ErrBadCode) {
				t.Fatalf("want ErrBadCode, got %v", err)
			}
		})
	}
}

func TestHandoffMismatchEndsSession(t *testing.T) { // OPS16
	e := newEnv(t)
	ctx := context.Background()
	start, si, state := e.handoffRun(t)
	data, _ := e.svc.states.peek("handoff", si.Handoff.Code)
	cookie := data["cookie"]
	if _, err := e.svc.Authenticate(ctx, cookie); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RedeemHandoff(ctx, si.Handoff.Code, state, "https://192.168.1.99:8460", start.HandoffBind); err == nil {
		t.Fatal("wrong host")
	}
	if _, err := e.svc.Authenticate(ctx, cookie); err == nil {
		t.Fatal("a refused handoff must not leave a live session behind")
	}
}

func TestCheckSignInAddress(t *testing.T) { // OPS16
	e := newEnv(t)
	e.withApp()
	ctx := context.Background()
	p, _ := e.withProbe()
	got, err := e.svc.CheckSignInAddress(ctx, lanConsole, true)
	if err != nil || got.Registered || got.CallbackURL != lanConsole+CallbackPath || got.AppURL == "" {
		t.Fatalf("%+v %v", got, err)
	}
	p.known[lanConsole+CallbackPath] = true
	got, err = e.svc.CheckSignInAddress(ctx, lanConsole, true)
	if err != nil || !got.Registered {
		t.Fatalf("%+v %v", got, err)
	}
	// Known now: no second question to GitHub.
	n := len(p.calls)
	if got, _ = e.svc.CheckSignInAddress(ctx, lanConsole, true); !got.Registered || len(p.calls) != n {
		t.Fatalf("asked GitHub again: %v", p.calls)
	}
	if _, err := e.svc.CheckSignInAddress(ctx, "http://192.168.1.4:8460", true); err == nil {
		t.Fatal("plain http off loopback is refused")
	}
	p.err = github.ErrUnavailable
	if _, err := e.svc.CheckSignInAddress(ctx, "https://other.lan:8460", true); !errors.Is(err, github.ErrUnavailable) {
		t.Fatalf("%v", err)
	}
}

func TestManifestKeepsRegisteredList(t *testing.T) { // OPS16
	e := newEnv(t)
	var saved []string
	e.svc.cfg.SaveRegistered = func(_ context.Context, o []string) error { saved = o; return nil }
	e.consoles = []string{devConsole, "https://pool.lan:8460"}
	e.withApp()
	if len(saved) != 2 || saved[0] != devConsole || saved[1] != "https://pool.lan:8460" {
		t.Fatalf("saved %v", saved)
	}
}
