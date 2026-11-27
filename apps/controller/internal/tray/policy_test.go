package tray

import (
	"context"
	"strings"
	"testing"
	"time"
)

func policyMenu(p MachinePolicy) map[string]MenuItem {
	st := Status{State: StateHealthy, Runners: 1, Policy: p}
	out := map[string]MenuItem{}
	for _, it := range BuildMenu("1.4.0", st, false) {
		out[it.ID] = it
	}
	return out
}

// TRAY3: the Mode submenu has Automatic and Always as radio items, and the one in
// force is ticked. Paused is no mode of the tray.
func TestModeSubmenuIsTwoRadioItemsWithOneTick(t *testing.T) {
	for _, mode := range []string{ModeAutomatic, ModeAlways} {
		m := policyMenu(MachinePolicy{Known: true, Mode: mode, Accepting: true})
		sub := map[string]string{IDModeAuto: ModeAutomatic, IDModeAlw: ModeAlways}
		ticked := 0
		for id, want := range sub {
			it := m[id]
			if it.Parent != IDMode || !it.Radio || it.Hidden {
				t.Errorf("%s: parent %q radio %v hidden %v", id, it.Parent, it.Radio, it.Hidden)
			}
			if it.Checked {
				ticked++
				if want != mode {
					t.Errorf("mode %s ticks %s", mode, want)
				}
			}
		}
		if ticked != 1 {
			t.Errorf("mode %s: %d items ticked, want 1", mode, ticked)
		}
	}
	if got := policyMenu(MachinePolicy{Known: true, Mode: ModeAlways, Accepting: true})[IDMode].Label; got != "Mode: Always" {
		t.Errorf("label %q", got)
	}
	// A machine paused from the console ticks neither mode.
	m := policyMenu(MachinePolicy{Known: true, Mode: ModePaused, PauseReason: "paused"})
	if m[IDModeAuto].Checked || m[IDModeAlw].Checked {
		t.Error("Paused is not a mode of the tray")
	}
}

// TRAY3: the Pause submenu shows while running; Paused ... Resume now shows while the user holds a pause.
func TestPauseAndResumeItems(t *testing.T) {
	run := policyMenu(MachinePolicy{Known: true, Mode: ModeAutomatic, Accepting: true})
	if run[IDPause].Hidden || run[IDPauseHour].Label != "for 1 hour" || run[IDPauseTill].Label != "until I resume" || !run[IDResume].Hidden {
		t.Errorf("running: pause %+v resume %+v", run[IDPause], run[IDResume])
	}
	for name, p := range map[string]MachinePolicy{
		"timed pause":    {Known: true, Mode: ModeAutomatic, PauseUntil: time.Now().Add(time.Hour), PauseReason: "paused"},
		"until resumed":  {Known: true, Mode: ModeAutomatic, UntilResumed: true, PauseReason: "paused"},
		"paused by mode": {Known: true, Mode: ModePaused, PauseReason: "paused"},
	} {
		m := policyMenu(p)
		if !m[IDPause].Hidden || m[IDResume].Hidden || !strings.HasSuffix(m[IDResume].Label, "Resume now") {
			t.Errorf("%s: pause hidden %v, resume %+v", name, m[IDPause].Hidden, m[IDResume])
		}
	}
}

// An older service says nothing about a policy: the tray offers no mode.
func TestNoPolicyHidesTheModeItems(t *testing.T) {
	m := policyMenu(MachinePolicy{})
	for _, id := range []string{IDMode, IDModeAuto, IDModeAlw, IDPause, IDPauseHour, IDPauseTill, IDResume} {
		if !m[id].Hidden {
			t.Errorf("%s is shown without a policy", id)
		}
	}
}

// CRSP75: the header says Paused or On battery when the machine takes no new job.
func TestHeaderNamesWhyTheMachineTakesNoJobs(t *testing.T) {
	cases := []struct {
		name string
		p    MachinePolicy
		want string
	}{
		{"accepting", MachinePolicy{Known: true, Accepting: true}, "CI Runners Shared Pool 1.4.0 - up to date"},
		{"paused", MachinePolicy{Known: true, PauseReason: "paused"}, "CI Runners Shared Pool 1.4.0 - up to date · Paused"},
		{"on battery", MachinePolicy{Known: true, PauseReason: "on_battery", OnBattery: true}, "CI Runners Shared Pool 1.4.0 - up to date · On battery"},
		{"over budget is not a pause", MachinePolicy{Known: true, PauseReason: "budget"}, "CI Runners Shared Pool 1.4.0 - up to date"},
		{"no policy", MachinePolicy{}, "CI Runners Shared Pool 1.4.0 - up to date"},
	}
	for _, c := range cases {
		st := Status{State: StateHealthy, Runners: 1, Update: UpdateInfo{State: "idle"}, Policy: c.p}
		if got := Header("1.4.0", st); got != c.want {
			t.Errorf("%s: %q want %q", c.name, got, c.want)
		}
	}
	// The tooltip says it too.
	st := Status{State: StateHealthy, Running: 1, Runners: 1, Policy: MachinePolicy{Known: true, PauseReason: "on_battery"}}
	if !strings.HasSuffix(st.Text(), "· on battery") {
		t.Errorf("tooltip text %q", st.Text())
	}
}

// CRSP75: the client sends the mode, the pause and the resume as calls, and
// reads the policy of the answer.
func TestClientSetsModePauseAndResume(t *testing.T) {
	var last string
	c := &Client{Timeout: time.Second, Dial: fakeChannel(func(req string) string {
		last = req
		return `{"ok":true,"state":"ok","policy":{"mode":"paused","accepting":false,"pause_reason":"paused","until_resumed":true,"pause_until_ms":1800000000000}}`
	})}
	ctx := context.Background()
	st, err := c.SetMode(ctx, ModePaused)
	if err != nil || !strings.Contains(last, `"call":"mode"`) || !strings.Contains(last, `"mode":"paused"`) {
		t.Fatalf("mode: %v %q", err, last)
	}
	if !st.Policy.Known || st.Policy.Mode != ModePaused || st.Policy.Accepting || !st.Policy.UntilResumed || st.Policy.PauseUntil.IsZero() {
		t.Fatalf("policy %+v", st.Policy)
	}
	if _, err = c.Pause(ctx, PauseOneHour); err != nil || !strings.Contains(last, `"call":"pause"`) || !strings.Contains(last, `"pause":"one_hour"`) {
		t.Fatalf("pause: %v %q", err, last)
	}
	if _, err = c.Resume(ctx); err != nil || !strings.Contains(last, `"call":"resume"`) {
		t.Fatalf("resume: %v %q", err, last)
	}
}

type fakePolicy struct {
	mode, pause string
	resumed     bool
	err         error
}

func (f *fakePolicy) SetMode(_ context.Context, m string) (Status, error) {
	f.mode = m
	return Status{Policy: MachinePolicy{Known: true, Mode: m}}, f.err
}
func (f *fakePolicy) Pause(_ context.Context, k string) (Status, error) {
	f.pause = k
	return Status{}, f.err
}
func (f *fakePolicy) Resume(context.Context) (Status, error) {
	f.resumed = true
	return Status{}, f.err
}

// CRSP75: a menu click changes the mode and hands the new status to the menu; a
// failure becomes one notice and changes nothing.
func TestOpenerChangesPolicyAndNoticesAFailure(t *testing.T) {
	var notices []string
	f := &fakePolicy{}
	o := &Opener{Policy: f, Notify: func(title, body string) { notices = append(notices, title+": "+body) }}
	var shown []Status
	show := func(s Status) { shown = append(shown, s) }
	if err := o.SetMode(context.Background(), ModeAlways, show); err != nil || f.mode != ModeAlways || len(shown) != 1 || shown[0].Policy.Mode != ModeAlways {
		t.Fatalf("set mode: %v %+v %+v", err, f, shown)
	}
	if err := o.PauseFor(context.Background(), PauseOneHour, show); err != nil || f.pause != PauseOneHour {
		t.Fatalf("pause: %v %+v", err, f)
	}
	if err := o.Resume(context.Background(), show); err != nil || !f.resumed {
		t.Fatalf("resume: %v %+v", err, f)
	}
	f.err = context.DeadlineExceeded
	before := len(shown)
	if err := o.SetMode(context.Background(), ModePaused, show); err == nil || len(notices) != 1 || len(shown) != before {
		t.Fatalf("failure: %v notices %v shown %d", err, notices, len(shown))
	}
	if err := (&Opener{Notify: func(string, string) {}}).Resume(context.Background(), nil); err == nil {
		t.Error("an opener without a policy setter must say so")
	}
}
