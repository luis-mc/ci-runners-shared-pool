package tray

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// labels lists the visible menu as text: a separator is "---", a submenu item is indented.
func labels(items []MenuItem) []string {
	var out []string
	for _, it := range Visible(items) {
		switch {
		case it.Separator:
			out = append(out, "---")
		case it.Parent != "":
			out = append(out, "  "+it.Label)
		default:
			out = append(out, it.Label)
		}
	}
	return out
}

// TRAY3: the same menu on every OS, in this order, for each state.
func TestMenuOrderPerState(t *testing.T) {
	healthy := Status{State: StateHealthy, Running: 2, Waiting: 1, Runners: 3, Version: "1.4.0", Update: UpdateInfo{State: "idle"},
		Policy: MachinePolicy{Known: true, Mode: ModeAutomatic, Accepting: true}}
	tail := []string{
		"---", "Restart the service", "Open log folder", "Open Doctor",
		"---", "Start at login", "Quit tray",
	}
	build := func(head []string, pause ...string) []string {
		out := append([]string{}, head...)
		out = append(out, "---", "Open console", "Open on GitHub", "Look for updates", "---")
		out = append(out, pause...)
		return append(out, tail...)
	}
	runPause := []string{"Pause", "  for 1 hour", "  until I resume", "Mode: Automatic", "  Automatic", "  Always"}
	cases := []struct {
		name string
		st   func() Status
		want []string
	}{
		{"up to date", func() Status { return healthy },
			build([]string{"CI Runners Shared Pool 1.4.0 - up to date", "Running - 2 jobs - 3 runners"}, runPause...)},
		{"update available", func() Status {
			s := healthy
			s.Update = UpdateInfo{State: "idle", Available: true, Latest: "1.5.0"}
			return s
		}, build([]string{"CI Runners Shared Pool 1.4.0", "\u2191 Update to 1.5.0", "Running - 2 jobs - 3 runners"}, runPause...)},
		{"paused 1 hour", func() Status {
			s := healthy
			s.Policy.PauseUntil = time.Date(2026, 11, 14, 15, 30, 0, 0, time.Local)
			s.Policy.PauseReason = "paused"
			s.Policy.Accepting = false
			return s
		}, build([]string{"CI Runners Shared Pool 1.4.0 - up to date · Paused", "Running - 2 jobs - 3 runners"},
			"Paused until 15:30 - Resume now", "Mode: Automatic", "  Automatic", "  Always")},
		{"paused until resume", func() Status {
			s := healthy
			s.Policy.UntilResumed = true
			s.Policy.PauseReason = "paused"
			s.Policy.Accepting = false
			return s
		}, build([]string{"CI Runners Shared Pool 1.4.0 - up to date · Paused", "Running - 2 jobs - 3 runners"},
			"Paused - Resume now", "Mode: Automatic", "  Automatic", "  Always")},
		{"no runners", func() Status {
			s := healthy
			s.Running, s.Waiting, s.Runners = 0, 0, 0
			return s
		}, build([]string{"CI Runners Shared Pool 1.4.0 - up to date", "Running - no runners yet"}, runPause...)},
	}
	for _, c := range cases {
		got := labels(BuildMenu("1.4.0", c.st(), true))
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s:\n got  %q\n want %q", c.name, got, c.want)
		}
	}
}

func TestMenuKinds(t *testing.T) {
	st := Status{State: StateHealthy, Runners: 1, Update: UpdateInfo{State: "idle", Available: true, Latest: "1.5.0"}, Policy: MachinePolicy{Known: true, Accepting: true}}
	by := map[string]MenuItem{}
	for _, it := range BuildMenu("1.4.0", st, true) {
		by[it.ID] = it
	}
	if by[IDHeader].Action || !by[IDUpdate].Action || !by[IDStatus].Action {
		t.Error("the header is grey text; Update and the status line press")
	}
	if !by[IDLogin].Switch || !by[IDLogin].Checked {
		t.Error("Start at login is a ticked switch")
	}
	if by[IDPauseHour].Parent != IDPause || by[IDModeAuto].Parent != IDMode || !by[IDModeAuto].Checked || by[IDModeAlw].Checked {
		t.Error("submenu parents and the ticked mode")
	}
}

func TestHeaderPerUpdateState(t *testing.T) {
	ok := func(u UpdateInfo) Status { return Status{State: StateHealthy, Runners: 1, Update: u} }
	cases := []struct {
		name string
		st   Status
		want string
	}{
		{"up to date", ok(UpdateInfo{State: "idle"}), "CI Runners Shared Pool 1.4.0 - up to date"},
		{"available", ok(UpdateInfo{Latest: "1.5.0", Available: true, State: "idle"}), "CI Runners Shared Pool 1.4.0"},
		{"draining", ok(UpdateInfo{Latest: "1.5.0", Available: true, State: "waiting_for_jobs"}), "CI Runners Shared Pool 1.4.0 - updating…"},
		{"installing", ok(UpdateInfo{Latest: "1.5.0", Available: true, State: "installing"}), "CI Runners Shared Pool 1.4.0 - updating…"},
		{"auto off, up to date", ok(UpdateInfo{State: "idle", AutoOff: true}), "CI Runners Shared Pool 1.4.0 - up to date · auto-update off"},
		{"auto off, available", ok(UpdateInfo{Latest: "1.5.0", Available: true, AutoOff: true}), "CI Runners Shared Pool 1.4.0 · auto-update off"},
		{"failed", ok(UpdateInfo{State: "failed"}), "CI Runners Shared Pool 1.4.0 - update failed"},
		{"service stopped: no claim", Status{State: StateStopped}, "CI Runners Shared Pool 1.4.0"},
		{"service starting: no claim", Status{State: StateStarting}, "CI Runners Shared Pool 1.4.0"},
	}
	for _, c := range cases {
		if got := Header("1.4.0", c.st); got != c.want {
			t.Errorf("%s: %q want %q", c.name, got, c.want)
		}
	}
}

func TestStatusLinePerState(t *testing.T) {
	at := time.Date(2026, 11, 14, 14, 30, 0, 0, time.UTC)
	cases := map[string]Status{
		"Running - 2 jobs - 3 runners":   {State: StateHealthy, Running: 2, Waiting: 1, Runners: 3},
		"Running - 1 job - 1 runner":     {State: StateHealthy, Running: 1, Runners: 1},
		"Waiting - 0 jobs - 1 runner":    {State: StateWaiting, Waiting: 4, Runners: 1},
		"Failed - GitHub not reachable":  {State: StateFailed, Fault: "GitHub not reachable"},
		"Stopped at 14:30 - exit code 3": {State: StateStopped, StoppedAt: at, ExitCode: 3},
		"Starting":                       {State: StateStarting},
		"Running - no runners yet":       {State: StateHealthy},
	}
	for want, s := range cases {
		if got := s.StatusLine(); got != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
}

// The Update line shows when a newer release exists and no update runs.
func TestUpdateItemOnlyWhenNewer(t *testing.T) {
	has := func(st Status) bool {
		for _, it := range Visible(BuildMenu("1.4.0", st, false)) {
			if it.ID == IDUpdate {
				return it.Label == "\u2191 Update to 1.5.0"
			}
		}
		return false
	}
	base := Status{State: StateHealthy, Runners: 1}
	for name, tc := range map[string]struct {
		u    UpdateInfo
		show bool
	}{
		"newer":           {UpdateInfo{Latest: "1.5.0", Available: true, State: "idle"}, true},
		"newer, auto off": {UpdateInfo{Latest: "1.5.0", Available: true, AutoOff: true, State: "idle"}, true},
		"none":            {UpdateInfo{State: "idle"}, false},
		"updating":        {UpdateInfo{Latest: "1.5.0", Available: true, State: "waiting_for_jobs"}, false},
	} {
		st := base
		st.Update = tc.u
		if has(st) != tc.show {
			t.Errorf("%s: shown=%v want %v", name, has(st), tc.show)
		}
	}
}

func TestClientCarriesUpdateFieldsAndTarget(t *testing.T) {
	var last string
	c := &Client{Timeout: time.Second, Dial: fakeChannel(func(req string) string {
		last = req
		return `{"ok":true,"state":"ok","running":1,"runners":1,"version":"1.4.0","auto_update":false,"latest_version":"1.5.0","update_available":true,"update_state":"idle"}`
	})}
	st, err := c.Check(context.Background())
	if err != nil || !strings.Contains(last, `"check"`) {
		t.Fatalf("%v %q", err, last)
	}
	if st.Version != "1.4.0" || !st.Update.AutoOff || !st.Update.Available || st.Update.Latest != "1.5.0" {
		t.Fatalf("%+v", st)
	}
	// An older service says nothing about auto-update: it counts as on.
	c = &Client{Timeout: time.Second, Dial: fakeChannel(func(string) string { return `{"ok":true,"state":"ok"}` })}
	if st, _ := c.Status(context.Background()); st.Update.AutoOff {
		t.Fatal("a missing auto_update field must read as on")
	}
	c = &Client{Timeout: time.Second, Dial: fakeChannel(func(req string) string { last = req; return `{"ok":true,"url":"u"}` })}
	if _, err := c.LinkTo(context.Background(), ReviewRestartPath); err != nil || !strings.Contains(last, `"target":"/settings/updates/review?kind=restart"`) {
		t.Fatalf("%v %q", err, last)
	}
}

type fakeTargets struct{ got string }

func (f *fakeTargets) LinkTo(_ context.Context, t string) (string, error) {
	f.got = t
	return "https://x/auth/link?code=1", nil
}

// Restart and Update never ask in the tray: they open the console Review step (R5.15, R5.18).
func TestRestartAndUpdateOpenTheReviewStep(t *testing.T) {
	for _, path := range []string{ReviewRestartPath, ReviewUpdatePath} {
		ft := &fakeTargets{}
		var opened string
		o := &Opener{Targets: ft, Browser: func(u string) error { opened = u; return nil }}
		if err := o.OpenReview(context.Background(), path); err != nil || ft.got != path || opened == "" {
			t.Fatalf("%s: %q %q %v", path, ft.got, opened, err)
		}
	}
	// A stopped service gets the Problem notice, not a dead link.
	called := false
	o := &Opener{Targets: &fakeTargets{}, Browser: func(string) error { called = true; return nil },
		Current: func() Status { return Status{State: StateStopped, ExitCode: 3} }, Notify: func(string, string) {}}
	if err := o.OpenReview(context.Background(), ReviewRestartPath); err == nil || called {
		t.Fatal("must refuse while stopped")
	}
}

type fakeChecker struct {
	st  Status
	err error
}

func (f fakeChecker) Check(context.Context) (Status, error) { return f.st, f.err }

func TestLookForUpdatesSaysTheResult(t *testing.T) {
	run := func(c fakeChecker) (title, body string) {
		o := &Opener{Checker: c, Version: "1.4.0", Notify: func(t, b string) { title, body = t, b }}
		_ = o.LookForUpdates(context.Background())
		return
	}
	if title, _ := run(fakeChecker{st: Status{Update: UpdateInfo{}}}); title != "Up to date" {
		t.Errorf("%q", title)
	}
	if title, _ := run(fakeChecker{err: errors.New("no answer")}); title != "Could not look for updates" {
		t.Errorf("%q", title)
	}
}

// R5.18: after the update the tray comes back on the new version.
func TestTrayRelaunchesWhenServiceIsNewer(t *testing.T) {
	for _, c := range []struct {
		own, svc string
		want     bool
	}{
		{"1.4.0", "1.5.0", true},
		{"1.5.0", "1.5.0", false},
		{"1.5.0", "1.4.0", false}, // a rollback never relaunches
		{"1.4.0", "", false},      // an old service that says no version
		{"dev", "1.5.0", false},   // a developer build stays
	} {
		if got := NeedsRelaunch(c.own, c.svc); got != c.want {
			t.Errorf("%v: %v", c, got)
		}
	}
}

func TestOpenOnGitHubOpensTheRepository(t *testing.T) {
	var opened string
	o := &Opener{Browser: func(u string) error { opened = u; return nil }}
	if err := o.OpenGitHub(); err != nil || opened != "https://github.com/luis-mc/ci-runners-shared-pool" {
		t.Fatalf("%q %v", opened, err)
	}
}

// The status line and Open Doctor open the console at their page.
func TestOpenPageUsesTheTargetLink(t *testing.T) {
	for _, path := range []string{JobsPath, DoctorPath} {
		ft := &fakeTargets{}
		var opened string
		o := &Opener{Targets: ft, Browser: func(u string) error { opened = u; return nil }}
		if err := o.OpenPage(context.Background(), path); err != nil || ft.got != path || opened == "" {
			t.Fatalf("%s: %q %q %v", path, ft.got, opened, err)
		}
	}
}

// CIX23 (D18): the digest link shows only when a weekly digest file exists.
func TestDigestItemOnlyWithAFile(t *testing.T) {
	st := Status{State: StateHealthy, Version: "1.4.0", Update: UpdateInfo{State: "idle"}}
	for _, l := range labels(Visible(BuildMenu("1.4.0", st, false))) {
		if l == LabelOpenDigest {
			t.Fatal("no digest file: the link must be hidden")
		}
	}
	st.DigestPath = "/data/ci-data/digests/2026-W47.md"
	found := false
	for _, l := range labels(Visible(BuildMenu("1.4.0", st, false))) {
		found = found || l == LabelOpenDigest
	}
	if !found {
		t.Fatal("a digest file exists: the menu must link to it")
	}
}
