package tray

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDecideAfterUpdate(t *testing.T) {
	st := func(by string) *StageInfo { return &StageInfo{Name: "done", StartedBy: by} }
	for _, c := range []struct {
		name   string
		stage  *StageInfo
		native bool
		want   AfterAction
	}{
		{"automatic update: one toast", st("auto"), true, AfterToast},
		{"older service: one toast", nil, true, AfterToast},
		{"no origin in the stage: one toast", st(""), true, AfterToast},
		{"the window started it: the window", st("tray"), true, AfterWindow},
		{"the window started it, no native window here: a toast", st("tray"), false, AfterToast},
		{"the console started it: nothing", st("console"), true, AfterNothing},
	} {
		if got := DecideAfterUpdate(c.stage, c.native); got != c.want {
			t.Errorf("%s: %d", c.name, got)
		}
	}
}

type afterRig struct {
	toasts   []string
	clicks   []func()
	windows  int
	winErr   error
	statuses []Status
	i        int
	due      bool
}

func (r *afterRig) env(native bool) AfterEnv {
	e := AfterEnv{
		Status: func(context.Context) (Status, error) {
			if r.i >= len(r.statuses) {
				return r.statuses[len(r.statuses)-1], nil
			}
			s := r.statuses[r.i]
			r.i++
			return s, nil
		},
		Toast: func(title, body string, click func()) {
			r.toasts = append(r.toasts, title+"|"+body)
			r.clicks = append(r.clicks, click)
		},
		Due:     func(string) bool { return r.due },
		Version: "0.1.13", Interval: time.Millisecond, Limit: time.Second,
		Sleep: func(context.Context, time.Duration) {},
	}
	if native {
		e.OpenWindow = func(context.Context) error { r.windows++; return r.winErr }
	}
	return e
}

func stageStatus(name, by string) Status {
	return Status{Update: UpdateInfo{Stage: &StageInfo{Name: name, StartedBy: by, Version: "0.1.13"}}}
}

// D13: an automatic update ends with one toast, and the toast opens the window on Done.
func TestAutomaticUpdateEndsWithOneToastThatOpensTheWindow(t *testing.T) {
	r := &afterRig{due: true, statuses: []Status{{}, stageStatus("restarting", "auto"), stageStatus("done", "auto")}}
	if got := AfterUpdate(context.Background(), r.env(true)); got != AfterToast {
		t.Fatal(got)
	}
	if len(r.toasts) != 1 || r.toasts[0] != "CI Runners Shared Pool|Now running 0.1.13." {
		t.Fatalf("%v", r.toasts)
	}
	if r.windows != 0 {
		t.Fatal("a toast opens nothing by itself")
	}
	r.clicks[0]()
	if r.windows != 1 {
		t.Fatal("a click on the toast opens the window")
	}
}

func TestWindowStartedUpdateReopensTheWindowNotAToast(t *testing.T) {
	r := &afterRig{due: true, statuses: []Status{stageStatus("done", "tray")}}
	if got := AfterUpdate(context.Background(), r.env(true)); got != AfterWindow || r.windows != 1 || len(r.toasts) != 0 {
		t.Fatalf("%d windows %d toasts %d", got, r.windows, len(r.toasts))
	}
	r2 := &afterRig{due: true, winErr: errors.New("no"), statuses: []Status{stageStatus("done", "tray")}}
	AfterUpdate(context.Background(), r2.env(true))
	if len(r2.toasts) != 1 {
		t.Fatal("a window that cannot open falls back to the toast")
	}
}

func TestConsoleStartedAndAlreadySaidAndFailedSayNothing(t *testing.T) {
	for name, r := range map[string]*afterRig{
		"console": {due: true, statuses: []Status{stageStatus("done", "console")}},
		"said":    {due: false, statuses: []Status{stageStatus("done", "auto")}},
		"failed":  {due: true, statuses: []Status{stageStatus("failed", "auto")}},
	} {
		if got := AfterUpdate(context.Background(), r.env(true)); got != AfterNothing || len(r.toasts) != 0 || r.windows != 0 {
			t.Errorf("%s: %d", name, got)
		}
	}
}

func TestNoStageEverGivesTheToast(t *testing.T) {
	r := &afterRig{due: true, statuses: []Status{{}}}
	if got := AfterUpdate(context.Background(), r.env(false)); got != AfterToast || len(r.toasts) != 1 {
		t.Fatal(got)
	}
}
